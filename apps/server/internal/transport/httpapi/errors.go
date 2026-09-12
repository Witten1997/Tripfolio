package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/transport/httpapi/middleware"
	"tripfolio/server/internal/transport/httpapi/problem"
)

// WriteProblem 写出 problem+json 错误响应，请求编号从上下文取得。
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	problem.Write(w, status, code, detail, middleware.RequestIDFrom(r.Context()))
}

// writeAppError 把业务错误映射为 problem+json，含字段明细、冲突上下文与附加头。
func writeAppError(w http.ResponseWriter, r *http.Request, e *apperr.Error) {
	for k, v := range e.Headers {
		w.Header().Set(k, v)
	}
	body := problem.Body{
		Status: e.Status, Code: e.Code, Detail: e.Detail, RequestID: middleware.RequestIDFrom(r.Context()),
	}
	for _, f := range e.Fields {
		body.Errors = append(body.Errors, problem.FieldError{Field: f.Field, Code: f.Code, Message: f.Message})
	}
	if e.Conflict != nil {
		body.Conflict = &problem.Conflict{
			EntityType: e.Conflict.EntityType, EntityID: e.Conflict.EntityID.String(),
			ExpectedVersion: strconv.FormatInt(e.Conflict.ExpectedVersion, 10), CurrentVersion: strconv.FormatInt(e.Conflict.CurrentVersion, 10),
			ConflictingFields: e.Conflict.ConflictingFields, Current: e.Conflict.Current, DeletionContext: e.Conflict.DeletionContext,
		}
	}
	problem.WriteBody(w, body)
}

// notFound 是未匹配路由的响应：不区分“不存在”与“非本人”，统一 404 RESOURCE_NOT_FOUND。
func notFound(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "")
}

// methodNotAllowed 是路径存在但方法不支持时的响应。
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "")
}

// requestError 处理生成代码在解析参数或正文时的错误。
func requestError(w http.ResponseWriter, r *http.Request, err error) {
	WriteProblem(w, r, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
}

// mustActor 从上下文取 Actor；受保护操作由中间件保证存在，缺失时返回 401。
func mustActor(ctx context.Context) (actor.Actor, error) {
	a, ok := actor.FromContext(ctx)
	if !ok {
		return actor.Actor{}, apperr.Unauthorized("AUTH_REQUIRED", "请先登录")
	}
	return a, nil
}

// parseIfMatch 解析 If-Match: "7"，不接受 * 或弱 ETag；缺失返回 428。
func parseIfMatch(header *string) (int64, error) {
	if header == nil {
		return 0, apperr.VersionRequired()
	}
	h := strings.TrimSpace(*header)
	if h == "" {
		return 0, apperr.VersionRequired()
	}
	if strings.HasPrefix(h, "W/") || h == "*" {
		return 0, apperr.BadRequest("MALFORMED_REQUEST", "If-Match 不接受弱 ETag 或 *")
	}
	h = strings.Trim(h, `"`)
	if h == "" || h[0] == '0' {
		return 0, apperr.BadRequest("MALFORMED_REQUEST", "If-Match 必须是正整数版本")
	}
	v, err := strconv.ParseInt(h, 10, 64)
	if err != nil || v <= 0 {
		return 0, apperr.BadRequest("MALFORMED_REQUEST", "If-Match 必须是正整数版本")
	}
	return v, nil
}

// etag 把版本格式化为强 ETag。
func etag(version fmt.Stringer) string { return `"` + version.String() + `"` }

// parseUUID 解析路径或正文中的 UUID，无效时返回 422。
func parseUUID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperr.Validation(apperr.Field(field, "INVALID", "必须是 UUID"))
	}
	return id, nil
}
