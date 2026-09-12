package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/transport/httpapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

func clientInfo(in generated.ClientInfo) account.ClientInfo {
	return account.ClientInfo{Kind: actor.ClientKind(in.Kind), DeviceID: uuid.UUID(in.DeviceId), DeviceName: in.DeviceName}
}

// authResponse 把业务结果转为契约模型；网页会话把刷新与 CSRF 令牌放 Cookie，原生会话放正文。
func (h *Handler) authResponse(res account.AuthResult, web bool) (generated.AuthResponse, []*http.Cookie) {
	body := generated.AuthResult{
		AccessToken: res.Tokens.AccessToken, TokenType: generated.Bearer,
		ExpiresInSeconds: int(res.Tokens.AccessExpiresIn.Seconds()), Account: res.Account.Resource(),
	}
	if !web {
		token := res.Tokens.RefreshToken
		body.RefreshToken = &token
		return generated.AuthResponse{Data: body}, nil
	}
	csrf := res.Tokens.CSRFToken
	body.CsrfToken = &csrf
	return generated.AuthResponse{Data: body}, []*http.Cookie{
		h.cookies.refreshCookie(res.Tokens.RefreshToken, res.Tokens.RefreshExpiresAt),
		h.cookies.csrfCookie(csrf, res.Tokens.RefreshExpiresAt),
	}
}

type loginResponse struct {
	withCookies[generated.Login200JSONResponse]
}

func (r loginResponse) VisitLoginResponse(w http.ResponseWriter) error { return r.apply(w) }

type registerResponse struct {
	withCookies[generated.RegisterAccount201JSONResponse]
}

func (r registerResponse) VisitRegisterAccountResponse(w http.ResponseWriter) error {
	return r.apply(w)
}

type refreshResponse struct {
	withCookies[generated.RefreshSession200JSONResponse]
}

func (r refreshResponse) VisitRefreshSessionResponse(w http.ResponseWriter) error { return r.apply(w) }

type logoutResponse struct {
	withCookies[generated.Logout204Response]
}

func (r logoutResponse) VisitLogoutResponse(w http.ResponseWriter) error { return r.apply(w) }

// RequestEmailChallenge 实现 POST /auth/email-challenges。
func (h *Handler) RequestEmailChallenge(ctx context.Context, req generated.RequestEmailChallengeRequestObject) (generated.RequestEmailChallengeResponseObject, error) {
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	r := middleware.RequestFrom(ctx)
	if err := checkOrigin(r, h.corsOrigins); err != nil {
		return nil, err
	}
	res, err := h.identity.RequestEmailChallenge(ctx, account.Purpose(req.Body.Purpose), req.Body.Email, clientIP(r))
	if err != nil {
		return nil, err
	}
	return generated.RequestEmailChallenge202JSONResponse{Data: generated.EmailChallengeResult{
		ChallengeId: res.ChallengeID, ExpiresInSeconds: res.ExpiresInSeconds, RetryAfterSeconds: res.RetryAfterSeconds,
	}}, nil
}

// RegisterAccount 实现 POST /auth/register。
func (h *Handler) RegisterAccount(ctx context.Context, req generated.RegisterAccountRequestObject) (generated.RegisterAccountResponseObject, error) {
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	r := middleware.RequestFrom(ctx)
	if err := checkOrigin(r, h.corsOrigins); err != nil {
		return nil, err
	}
	res, err := h.identity.Register(ctx, account.RegisterInput{
		ChallengeID: uuid.UUID(req.Body.ChallengeId), Email: req.Body.Email, Code: req.Body.Code,
		Password: req.Body.Password, Nickname: req.Body.Nickname, Client: clientInfo(req.Body.Client),
	})
	if err != nil {
		return nil, err
	}
	body, cookies := h.authResponse(res, req.Body.Client.Kind == generated.Web)
	return registerResponse{withCookies[generated.RegisterAccount201JSONResponse]{
		inner: generated.RegisterAccount201JSONResponse(body), cookies: cookies,
		visit: func(v generated.RegisterAccount201JSONResponse, w http.ResponseWriter) error {
			return v.VisitRegisterAccountResponse(w)
		},
	}}, nil
}

// Login 实现 POST /auth/login。
func (h *Handler) Login(ctx context.Context, req generated.LoginRequestObject) (generated.LoginResponseObject, error) {
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	r := middleware.RequestFrom(ctx)
	if err := checkOrigin(r, h.corsOrigins); err != nil {
		return nil, err
	}
	res, err := h.identity.Login(ctx, req.Body.Email, req.Body.Password, clientInfo(req.Body.Client), clientIP(r))
	if err != nil {
		return nil, err
	}
	body, cookies := h.authResponse(res, req.Body.Client.Kind == generated.Web)
	return loginResponse{withCookies[generated.Login200JSONResponse]{
		inner: generated.Login200JSONResponse(body), cookies: cookies,
		visit: func(v generated.Login200JSONResponse, w http.ResponseWriter) error { return v.VisitLoginResponse(w) },
	}}, nil
}

// RefreshSession 实现 POST /auth/refresh：正文含 refresh_token 走原生路径，否则走 Cookie + CSRF + Origin 路径。
func (h *Handler) RefreshSession(ctx context.Context, req generated.RefreshSessionRequestObject) (generated.RefreshSessionResponseObject, error) {
	r := middleware.RequestFrom(ctx)
	if req.Body != nil && req.Body.RefreshToken != nil && *req.Body.RefreshToken != "" {
		res, err := h.sessions.Refresh(ctx, *req.Body.RefreshToken)
		if err != nil {
			return nil, err
		}
		body, _ := h.authResponse(res, false)
		return generated.RefreshSession200JSONResponse(body), nil
	}

	raw := h.cookies.readRefreshCookie(r)
	if raw == "" {
		return nil, apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录")
	}
	if err := checkOrigin(r, h.corsOrigins); err != nil {
		return nil, err
	}
	sessionID, _, ok := account.ParseRefreshToken(raw)
	if !ok {
		return nil, apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录")
	}
	headerToken := r.Header.Get("X-CSRF-Token")
	if headerToken == "" || headerToken != h.cookies.readCSRFCookie(r) {
		return nil, apperr.Forbidden("CSRF_FAILED", "跨站请求校验失败")
	}
	if err := h.sessions.VerifyCSRF(ctx, sessionID, headerToken); err != nil {
		return nil, err
	}
	res, err := h.sessions.Refresh(ctx, raw)
	if err != nil {
		return nil, err
	}
	body, cookies := h.authResponse(res, true)
	return refreshResponse{withCookies[generated.RefreshSession200JSONResponse]{
		inner: generated.RefreshSession200JSONResponse(body), cookies: cookies,
		visit: func(v generated.RefreshSession200JSONResponse, w http.ResponseWriter) error {
			return v.VisitRefreshSessionResponse(w)
		},
	}}, nil
}

// Logout 实现 POST /auth/logout：撤销当前会话并清除浏览器 Cookie。
func (h *Handler) Logout(ctx context.Context, _ generated.LogoutRequestObject) (generated.LogoutResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.sessions.Logout(ctx, a.SessionID); err != nil {
		return nil, err
	}
	return logoutResponse{withCookies[generated.Logout204Response]{
		inner: generated.Logout204Response{}, cookies: h.cookies.clearCookies(),
		visit: func(v generated.Logout204Response, w http.ResponseWriter) error { return v.VisitLogoutResponse(w) },
	}}, nil
}

// ResetPassword 实现 POST /auth/password-reset。
func (h *Handler) ResetPassword(ctx context.Context, req generated.ResetPasswordRequestObject) (generated.ResetPasswordResponseObject, error) {
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	if err := checkOrigin(middleware.RequestFrom(ctx), h.corsOrigins); err != nil {
		return nil, err
	}
	if err := h.identity.ResetPassword(ctx, uuid.UUID(req.Body.ChallengeId), req.Body.Email, req.Body.Code, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return generated.ResetPassword204Response{}, nil
}

// Reauthenticate 实现 POST /auth/reauthenticate。
func (h *Handler) Reauthenticate(ctx context.Context, req generated.ReauthenticateRequestObject) (generated.ReauthenticateResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	if err := h.identity.Reauthenticate(ctx, a, req.Body.Password); err != nil {
		return nil, err
	}
	return generated.Reauthenticate204Response{}, nil
}

// ChangePassword 实现 POST /account/password。
func (h *Handler) ChangePassword(ctx context.Context, req generated.ChangePasswordRequestObject) (generated.ChangePasswordResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	if err := h.identity.ChangePassword(ctx, a, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return generated.ChangePassword204Response{}, nil
}

// GetAccount 实现 GET /account。
func (h *Handler) GetAccount(ctx context.Context, _ generated.GetAccountRequestObject) (generated.GetAccountResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	acc, err := h.profile.Get(ctx, a)
	if err != nil {
		return nil, err
	}
	res := acc.Resource()
	tag := etag(res.Version)
	return generated.GetAccount200JSONResponse{Body: generated.AccountResponse{Data: res}, Headers: generated.GetAccount200ResponseHeaders{ETag: &tag}}, nil
}

// UpdateAccount 实现 PATCH /account。资料不进入旅行同步流，但同样返回 WriteResult。
func (h *Handler) UpdateAccount(ctx context.Context, req generated.UpdateAccountRequestObject) (generated.UpdateAccountResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	patch := account.ProfilePatch{Nickname: req.Body.Nickname, DefaultTimezone: req.Body.DefaultTimezone}
	if req.Body.AvatarAssetId.IsSpecified() {
		patch.AvatarSet = true
		if !req.Body.AvatarAssetId.IsNull() {
			id, err := req.Body.AvatarAssetId.Get()
			if err != nil {
				return nil, apperr.Validation(apperr.Field("avatar_asset_id", "INVALID", "必须是 UUID"))
			}
			u := uuid.UUID(id)
			patch.AvatarAssetID = &u
		}
	}
	updated, warnings, err := h.profile.Update(ctx, a, base, patch)
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []string{}
	}
	ref := account.ResultRef(updated)
	return generated.UpdateAccount200JSONResponse{Data: write.Result{
		OperationID: uuid.UUID(req.Params.IdempotencyKey), Primary: &ref, Affected: []write.EntityRef{ref},
		Warnings: warnings, Data: updated.Resource(),
	}}, nil
}

// ListSessions 实现 GET /account/sessions。
func (h *Handler) ListSessions(ctx context.Context, _ generated.ListSessionsRequestObject) (generated.ListSessionsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := h.sessions.List(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make([]generated.Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.Resource(a.SessionID))
	}
	return generated.ListSessions200JSONResponse{Data: out}, nil
}

// RevokeSession 实现 DELETE /account/sessions/{session_id}。
func (h *Handler) RevokeSession(ctx context.Context, req generated.RevokeSessionRequestObject) (generated.RevokeSessionResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.sessions.Revoke(ctx, a, uuid.UUID(req.SessionId)); err != nil {
		return nil, err
	}
	return generated.RevokeSession204Response{}, nil
}

func clientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	return middleware.ClientIP(r)
}

var _ = types.Version(0)
