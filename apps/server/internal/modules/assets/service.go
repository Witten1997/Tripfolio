package assets

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/write"
)

// Service 实现资产用例（接口设计 2.7、3.7；数据库设计表 14）。
//
// 上传状态机（唯一一份实现，sync/push 走同一入口）：
//
//	uploading --confirm--> processing --worker--> ready | failed
//	failed | 过期的 uploading --新尝试--> uploading（attempt+1，新暂存键）
//	未过期的 uploading --续签--> uploading（同暂存键，仅延长截止时间）
//	processing | ready --任何尝试--> 409 UPLOAD_NOT_ALLOWED
type Service struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	keys    ObjectKeys
	objects ObjectAuthorizer
	cursors paging.Codec
	clock   clock.Clock
	limits  UploadLimits
}

// Deps 是服务依赖。Keys 与 Objects 为 nil 时说明未配置对象存储，
// 所有涉及授权的用例返回 503 DEPENDENCY_UNAVAILABLE。
type Deps struct {
	UnitOfWork write.UnitOfWork[Repo]
	Reader     Reader
	Keys       ObjectKeys
	Objects    ObjectAuthorizer
	Cursors    paging.Codec
	Clock      clock.Clock
	Limits     UploadLimits
}

// NewService 创建服务。
func NewService(d Deps) *Service {
	return &Service{
		uow: d.UnitOfWork, reader: d.Reader, keys: d.Keys, objects: d.Objects,
		cursors: d.Cursors, clock: d.Clock, limits: d.Limits,
	}
}

// storageReady 判断对象存储是否已装配。
func (s *Service) storageReady() error {
	if s.keys == nil || s.objects == nil {
		return apperr.Dependency(errors.New("对象存储未配置"))
	}
	return nil
}

func assetGone() *apperr.Error {
	return apperr.Gone("RESOURCE_GONE", "资产已删除")
}

// CreateInput 是登记资产的输入（接口设计 3.7 AssetCreate）。
type CreateInput struct {
	ID                uuid.UUID
	Scope             Scope
	TripID            *uuid.UUID
	OriginalName      string
	ExpectedSize      int64
	DeclaredMediaType string
	ClientSHA256      *string
}

// createFingerprint 是创建命令的规范化形式：字段顺序固定，类型已小写归一，供幂等指纹计算。
type createFingerprint struct {
	Scope             Scope      `json:"scope"`
	TripID            *uuid.UUID `json:"trip_id"`
	OriginalName      string     `json:"original_name"`
	ExpectedSize      int64      `json:"expected_size"`
	DeclaredMediaType string     `json:"declared_media_type"`
	ClientSHA256      *string    `json:"client_sha256"`
}

// Create 登记资产与首个上传尝试，并直接返回暂存对象的直传授权。
// 不接受客户端提供的对象键；键由账号、资产与尝试序号推导。
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID uuid.UUID, in CreateInput) (write.Result, *UploadAuthorization, error) {
	if err := s.storageReady(); err != nil {
		return write.Result{}, nil, err
	}
	if err := s.validateCreate(in); err != nil {
		return write.Result{}, nil, err
	}

	now := s.clock.Now()
	expiresAt := now.Add(UploadWindow)
	keys := s.keys.KeysFor(a.AccountID, in.ID, 1)
	name := strings.TrimSpace(in.OriginalName)
	attempt := AttemptInfo{ExpectedSize: in.ExpectedSize, DeclaredMediaType: normalizeMediaType(in.DeclaredMediaType)}
	if in.ClientSHA256 != nil {
		attempt.ClientSHA256 = decodeHex(*in.ClientSHA256)
	}
	fp := createFingerprint{
		Scope: in.Scope, TripID: in.TripID, OriginalName: name, ExpectedSize: attempt.ExpectedSize,
		DeclaredMediaType: attempt.DeclaredMediaType, ClientSHA256: in.ClientSHA256,
	}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "asset.create",
		Fingerprint: write.Fingerprint("asset.create", in.ID.String(), nil, fp),
	}

	var created Resource
	result, err := s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		exists, err := repo.IDExists(ctx, in.ID)
		if err != nil {
			return apperr.Internal(err)
		}
		if exists {
			return apperr.Conflicted("ID_ALREADY_USED", "该 ID 已被使用")
		}
		// trip 范围必须落在本人未删除的旅行上；回收站中的旅行不接受新文件。
		if in.Scope == ScopeTrip {
			info, found, err := repo.Trip(ctx, a.AccountID, *in.TripID)
			if err != nil {
				return apperr.Internal(err)
			}
			if !found {
				return apperr.NotFound()
			}
			if info.DeletedAt != nil {
				return apperr.Gone("TRIP_DELETED", "旅行在回收站，不能添加文件")
			}
		}

		res := Resource{
			ID:              in.ID,
			TripID:          in.TripID,
			Scope:           in.Scope,
			OriginalName:    name,
			Status:          StatusUploading,
			UploadAttempt:   1,
			UploadExpiresAt: &expiresAt,
			ThumbnailStatus: ThumbnailNone,
			Version:         1,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		created, err = repo.Insert(ctx, a.AccountID, res, attempt, keys.Staging)
		if err != nil {
			return apperr.Internal(err)
		}
		recordWrite(scope, created, nil)
		return nil
	}, func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.GetForUpdate(ctx, a.AccountID, in.ID)
		if err != nil || !found {
			return nil, err
		}
		return r, nil
	})
	if err != nil {
		return write.Result{}, nil, err
	}

	// 重放时资产已存在，仍按当前尝试重新签发授权：客户端可能正是因为丢了授权才重试。
	// 幂等键相同即指纹相同，声明信息与本次输入一致，可直接复用。
	current, ok := result.Data.(Resource)
	if !ok {
		current = created
	}
	auth, err := s.authorizeUploadFor(ctx, a.AccountID, current, attempt)
	if err != nil {
		return write.Result{}, nil, err
	}
	return result, auth, nil
}

// validateCreate 校验创建输入：范围与 trip_id 配对、名称、大小与类型上限。
func (s *Service) validateCreate(in CreateInput) error {
	var fields []apperr.FieldError

	switch in.Scope {
	case ScopeTrip:
		if in.TripID == nil {
			fields = append(fields, apperr.Field("trip_id", "REQUIRED", "trip 范围必须指定旅行"))
		}
	case ScopeAvatar:
		if in.TripID != nil {
			fields = append(fields, apperr.Field("trip_id", "NOT_ALLOWED", "avatar 范围不允许指定旅行"))
		}
	default:
		fields = append(fields, apperr.Field("scope", "INVALID", "scope 必须是 trip 或 avatar"))
	}

	if in.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "REQUIRED", "必须提供资源 ID"))
	}
	name := strings.TrimSpace(in.OriginalName)
	if name == "" {
		fields = append(fields, apperr.Field("original_name", "REQUIRED", "文件名不能为空"))
	} else if len([]rune(name)) > 255 {
		fields = append(fields, apperr.Field("original_name", "TOO_LONG", "文件名最多 255 个字符"))
	}
	if in.ExpectedSize <= 0 {
		fields = append(fields, apperr.Field("expected_size", "INVALID", "声明大小必须为正整数"))
	}
	if in.ClientSHA256 != nil && !isHexSHA256(*in.ClientSHA256) {
		fields = append(fields, apperr.Field("client_sha256", "INVALID", "摘要必须是 64 位小写十六进制"))
	}
	if len(fields) > 0 {
		return apperr.Validation(fields...)
	}

	// 类型与大小分别对应 415 与 413，不并入 422：它们是独立的协议级拒绝。
	mediaType := normalizeMediaType(in.DeclaredMediaType)
	isImage := slices.Contains(s.limits.ImageMediaTypes, mediaType)
	isPDF := slices.Contains(s.limits.PDFMediaTypes, mediaType)
	switch {
	case in.Scope == ScopeAvatar && !isImage:
		// 头像只接受图片：PDF 头像没有意义，且缩略图链路只处理图片。
		return apperr.New(http.StatusUnsupportedMediaType, CodeUnsupportedType,
			fmt.Sprintf("头像只接受图片类型 %s", strings.Join(s.limits.ImageMediaTypes, "、")))
	case !isImage && !isPDF:
		return apperr.New(http.StatusUnsupportedMediaType, CodeUnsupportedType,
			fmt.Sprintf("只接受 %s 与 %s", strings.Join(s.limits.ImageMediaTypes, "、"), strings.Join(s.limits.PDFMediaTypes, "、")))
	}
	limit := s.limits.ImageMaxBytes
	if isPDF {
		limit = s.limits.PDFMaxBytes
	}
	if in.ExpectedSize > limit {
		return apperr.New(http.StatusRequestEntityTooLarge, CodeRequestTooLarge,
			fmt.Sprintf("%s 最大 %d MiB", mediaType, limit/(1024*1024)))
	}
	return nil
}

// AuthorizeUpload 续签当前尝试或开启新尝试。
// 未过期的 uploading 续签同一暂存键；failed 与已过期的 uploading 开新尝试；
// processing 与 ready 返回 409 UPLOAD_NOT_ALLOWED。
func (s *Service) AuthorizeUpload(ctx context.Context, a actor.Actor, operationID, assetID uuid.UUID) (write.Result, *UploadAuthorization, error) {
	if err := s.storageReady(); err != nil {
		return write.Result{}, nil, err
	}
	now := s.clock.Now()
	// 没有请求正文，指纹只绑定目标资产：同一操作编号重复调用即重放。
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "asset.authorize_upload",
		Fingerprint: write.Fingerprint("asset.authorize_upload", assetID.String(), nil, nil),
	}

	var updated Resource
	result, err := s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, assetID)
		if err != nil {
			return apperr.Internal(err)
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return assetGone()
		}
		if !current.AcceptsUploadAttempt() {
			return apperr.Conflicted(CodeUploadNotAllowed,
				fmt.Sprintf("%s 状态的资产不接受上传尝试；替换文件需新建资产", current.Status))
		}

		expiresAt := now.Add(UploadWindow)
		if current.Status == StatusUploading && !current.AttemptExpired(now) {
			// 续签：暂存键与尝试序号都不变，客户端可以继续用原地址重传。
			updated, err = repo.RenewAttempt(ctx, a.AccountID, assetID, expiresAt, now)
		} else {
			attempt := current.UploadAttempt + 1
			keys := s.keys.KeysFor(a.AccountID, assetID, int(attempt))
			updated, err = repo.StartAttempt(ctx, a.AccountID, assetID, attempt, keys.Staging, expiresAt, now)
		}
		if err != nil {
			return apperr.Internal(err)
		}

		recordWrite(scope, updated, []string{"status", "upload_attempt", "upload_expires_at"})
		return nil
	}, func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.GetForUpdate(ctx, a.AccountID, assetID)
		if err != nil || !found {
			return nil, err
		}
		return r, nil
	})
	if err != nil {
		return write.Result{}, nil, err
	}

	current, ok := result.Data.(Resource)
	if !ok {
		current = updated
	}
	// 声明信息在创建时固定，后续尝试沿用；读不到说明资产在提交后被并发清理，按 404 处理。
	attempt, found, err := s.reader.Attempt(ctx, a.AccountID, assetID)
	if err != nil {
		return write.Result{}, nil, apperr.Internal(err)
	}
	if !found {
		return write.Result{}, nil, apperr.NotFound()
	}
	auth, err := s.authorizeUploadFor(ctx, a.AccountID, current, attempt)
	if err != nil {
		return write.Result{}, nil, err
	}
	return result, auth, nil
}

// authorizeUploadFor 为资产的当前尝试签发直传授权。
// 签名里带上声明大小与类型，客户端必须原样发送；实际内容由 worker 嗅探判定。
func (s *Service) authorizeUploadFor(ctx context.Context, accountID uuid.UUID, res Resource, attempt AttemptInfo) (*UploadAuthorization, error) {
	// ready 后不再签发授权（接口设计 3.7）。
	if res.Status == StatusReady {
		return nil, nil
	}
	keys := s.keys.KeysFor(accountID, res.ID, int(res.UploadAttempt))
	// 授权有效期与确认窗口对齐：窗口过了授权也没用。
	ttl := UploadWindow
	if res.UploadExpiresAt != nil {
		if remain := res.UploadExpiresAt.Sub(s.clock.Now()); remain > 0 {
			ttl = remain
		}
	}
	auth, err := s.objects.AuthorizeUpload(ctx, keys.Staging, attempt.DeclaredMediaType, attempt.ExpectedSize, ttl)
	if err != nil {
		return nil, apperr.Dependency(err)
	}
	auth.UploadAttempt = res.UploadAttempt
	return &auth, nil
}

// Confirm 按尝试序号确认上传并入队校验任务。
// 不信任客户端的成功声明：只转入 processing，由 worker 校验后才可能进入 ready。
func (s *Service) Confirm(ctx context.Context, a actor.Actor, operationID, assetID uuid.UUID, uploadAttempt int32) (write.Result, error) {
	if uploadAttempt < 1 {
		return write.Result{}, apperr.Validation(
			apperr.Field("upload_attempt", "INVALID", "尝试序号必须是正整数"))
	}
	now := s.clock.Now()
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "asset.confirm",
		Fingerprint: write.Fingerprint("asset.confirm", assetID.String(), nil, struct {
			UploadAttempt int32 `json:"upload_attempt"`
		}{uploadAttempt}),
	}

	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, assetID)
		if err != nil {
			return apperr.Internal(err)
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return assetGone()
		}
		// 序号不是当前尝试：可能是过期授权的迟到确认，或客户端串了尝试。
		if uploadAttempt != current.UploadAttempt {
			return apperr.Conflicted(CodeUploadNotAllowed,
				fmt.Sprintf("尝试序号 %d 不是当前尝试 %d", uploadAttempt, current.UploadAttempt))
		}
		if current.Status != StatusUploading {
			return apperr.Conflicted(CodeUploadNotAllowed,
				fmt.Sprintf("%s 状态的资产不接受确认", current.Status))
		}
		// 超过确认窗口：暂存对象可能已被生命周期规则清掉，必须重新申请尝试。
		if current.AttemptExpired(now) {
			return apperr.Gone(CodeUploadExpired, "上传尝试已超过确认窗口，请重新申请上传授权")
		}

		updated, err := repo.MarkProcessing(ctx, a.AccountID, assetID, now)
		if err != nil {
			return apperr.Internal(err)
		}
		recordWrite(scope, updated, []string{"status"})
		// 事务入队：校验任务与状态变更同时提交，不会出现「已 processing 但没有任务」。
		return scope.Enqueue(VerifyJobArgs{
			AccountID: a.AccountID, AssetID: assetID, UploadAttempt: current.UploadAttempt,
		})
	}, func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.GetForUpdate(ctx, a.AccountID, assetID)
		if err != nil || !found {
			return nil, err
		}
		return r, nil
	})
}

// Get 返回资产的公开元数据；不存在或非本人 404，已删除 410。
func (s *Service) Get(ctx context.Context, a actor.Actor, assetID uuid.UUID) (Resource, error) {
	r, found, err := s.reader.Get(ctx, a.AccountID, assetID)
	if err != nil {
		return Resource{}, apperr.Internal(err)
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if r.DeletedAt != nil {
		return Resource{}, assetGone()
	}
	return r, nil
}

// AvatarUsable 实现 account.AvatarChecker：头像引用要求资产属于本账号、scope=avatar 且未删除。
// 引用不要求 ready（接口设计 3.7）：上传中的头像也可先绑定，状态随资产可见。
func (s *Service) AvatarUsable(ctx context.Context, accountID, assetID uuid.UUID) (bool, error) {
	r, found, err := s.reader.Get(ctx, accountID, assetID)
	if err != nil {
		return false, err
	}
	return found && r.DeletedAt == nil && r.Scope == ScopeAvatar, nil
}

// DownloadRequest 是单个资产的下载授权请求。
type DownloadRequest struct {
	AssetID uuid.UUID
	Variant Variant
}

// AuthorizeDownload 为单个资产签发下载授权；仅 ready 且变体可用时返回 URL。
func (s *Service) AuthorizeDownload(ctx context.Context, a actor.Actor, in DownloadRequest, disposition Disposition) (DownloadAuthorization, error) {
	if err := s.storageReady(); err != nil {
		return DownloadAuthorization{}, err
	}
	if err := validateVariant(in.Variant); err != nil {
		return DownloadAuthorization{}, err
	}
	if err := validateDisposition(disposition); err != nil {
		return DownloadAuthorization{}, err
	}

	res, found, err := s.reader.Get(ctx, a.AccountID, in.AssetID)
	if err != nil {
		return DownloadAuthorization{}, apperr.Internal(err)
	}
	if !found {
		return DownloadAuthorization{}, apperr.NotFound()
	}
	if res.DeletedAt != nil {
		return DownloadAuthorization{}, assetGone()
	}
	// 单个授权对未就绪资产报 409；批量授权则逐项返回状态而不报错。
	if res.Status != StatusReady {
		return DownloadAuthorization{}, apperr.Conflicted(CodeAssetNotReady,
			fmt.Sprintf("资产处于 %s 状态，尚不能下载", res.Status))
	}
	if in.Variant == VariantThumbnail && res.ThumbnailStatus != ThumbnailReady {
		return DownloadAuthorization{}, apperr.Conflicted(CodeAssetNotReady,
			fmt.Sprintf("缩略图处于 %s 状态，尚不能下载", res.ThumbnailStatus))
	}

	auth, err := s.authorizeDownloadFor(ctx, a.AccountID, res, in.Variant, disposition)
	if err != nil {
		return DownloadAuthorization{}, err
	}
	return auth, nil
}

// AuthorizeDownloads 批量签发下载授权。任一资产非本人时整批 404，
// 避免通过逐项状态探测他人资产是否存在。
func (s *Service) AuthorizeDownloads(ctx context.Context, a actor.Actor, items []DownloadRequest, disposition Disposition) ([]DownloadAuthorization, error) {
	if err := s.storageReady(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, apperr.Validation(apperr.Field("items", "REQUIRED", "至少需要一个资产"))
	}
	if len(items) > MaxDownloadBatch {
		return nil, apperr.Validation(apperr.Field("items", "TOO_MANY",
			fmt.Sprintf("一次最多 %d 个资产", MaxDownloadBatch)))
	}
	if err := validateDisposition(disposition); err != nil {
		return nil, err
	}
	for i := range items {
		if err := validateVariant(items[i].Variant); err != nil {
			return nil, err
		}
	}

	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.AssetID)
	}
	found, err := s.reader.GetMany(ctx, a.AccountID, ids)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	byID := make(map[uuid.UUID]Resource, len(found))
	for _, r := range found {
		byID[r.ID] = r
	}

	out := make([]DownloadAuthorization, 0, len(items))
	for _, it := range items {
		res, ok := byID[it.AssetID]
		// 缺失或已删除都按整批 404：不区分「不存在」与「他人的」。
		if !ok || res.DeletedAt != nil {
			return nil, apperr.NotFound()
		}
		auth := DownloadAuthorization{
			AssetID: res.ID, Status: res.Status, ThumbnailStatus: res.ThumbnailStatus,
			FileName: res.OriginalName, MediaType: res.MediaType, ByteSize: res.ByteSize,
		}
		// 只有 ready 且变体可用才签发 URL，其余项保留状态供客户端展示。
		variantReady := res.Status == StatusReady &&
			(it.Variant == VariantOriginal || res.ThumbnailStatus == ThumbnailReady)
		if variantReady {
			signed, err := s.authorizeDownloadFor(ctx, a.AccountID, res, it.Variant, disposition)
			if err != nil {
				return nil, err
			}
			auth = signed
		}
		out = append(out, auth)
	}
	return out, nil
}

// authorizeDownloadFor 签发单个变体的下载授权。
func (s *Service) authorizeDownloadFor(ctx context.Context, accountID uuid.UUID, res Resource, variant Variant, disposition Disposition) (DownloadAuthorization, error) {
	keys := s.keys.KeysFor(accountID, res.ID, int(res.UploadAttempt))
	key, ttl := keys.Final, OriginalDownloadTTL
	fileName := res.OriginalName
	mediaType := res.MediaType
	if variant == VariantThumbnail {
		key, ttl = keys.Thumbnail, ThumbnailDownloadTTL
		fileName = thumbnailFileName(res.OriginalName)
		jpeg := "image/jpeg"
		mediaType = &jpeg
	}
	contentType := ""
	if mediaType != nil {
		contentType = *mediaType
	}

	signed, err := s.objects.AuthorizeDownload(ctx, key, contentType, fileName, disposition, ttl)
	if err != nil {
		return DownloadAuthorization{}, apperr.Dependency(err)
	}
	return DownloadAuthorization{
		AssetID: res.ID, Status: res.Status, ThumbnailStatus: res.ThumbnailStatus,
		URL: signed.URL, ExpiresAt: signed.ExpiresAt,
		FileName: fileName, MediaType: mediaType, ByteSize: res.ByteSize,
	}, nil
}

// ListFilters 是 listTripAssets 的原始查询参数。
type ListFilters struct {
	Status string
	IDs    string
	Limit  int
	Cursor string
}

// List 返回一页旅行资产（接口设计 3.9 AssetFilters）：按 created_at、id 均降序。
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f ListFilters) (paging.Page[Resource], error) {
	info, found, err := s.reader.Trip(ctx, a.AccountID, tripID)
	if err != nil {
		return paging.Page[Resource]{}, apperr.Internal(err)
	}
	if !found {
		return paging.Page[Resource]{}, apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return paging.Page[Resource]{}, apperr.Gone("TRIP_DELETED", "旅行在回收站")
	}

	var fields []apperr.FieldError
	q := ListQuery{}
	if f.Status != "" {
		status, ferr := parseStatus(f.Status)
		if ferr != nil {
			fields = append(fields, *ferr)
		} else {
			q.Status = &status
		}
	}
	if f.IDs != "" {
		ids, ferr := parseIDList(f.IDs)
		if ferr != nil {
			fields = append(fields, *ferr)
		} else {
			q.IDs = ids
		}
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		if e, ok := apperr.As(err); ok {
			fields = append(fields, e.Fields...)
		} else {
			return paging.Page[Resource]{}, err
		}
	}
	q.Limit = limit
	if len(fields) > 0 {
		return paging.Page[Resource]{}, apperr.Validation(fields...)
	}

	scope := listScope(tripID, q)
	if f.Cursor != "" {
		var pos ListPosition
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &pos); err != nil {
			return paging.Page[Resource]{}, err
		}
		q.After = &pos
	}

	// 多取一条判断是否还有下一页。
	q.Limit = limit + 1
	rows, err := s.reader.ListByTrip(ctx, a.AccountID, tripID, q)
	if err != nil {
		return paging.Page[Resource]{}, apperr.Internal(err)
	}

	page := paging.Page[Resource]{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		cursor, err := s.cursors.Encode(a.AccountID, scope, ListPosition{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return paging.Page[Resource]{}, apperr.Internal(err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

// listScope 把筛选条件绑进游标签名，避免换条件后继续用旧游标。
func listScope(tripID uuid.UUID, q ListQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "assets|trip=%s", tripID)
	if q.Status != nil {
		fmt.Fprintf(&b, "|status=%s", *q.Status)
	}
	for _, id := range q.IDs {
		fmt.Fprintf(&b, "|id=%s", id)
	}
	return b.String()
}

func parseStatus(raw string) (Status, *apperr.FieldError) {
	switch Status(raw) {
	case StatusUploading, StatusProcessing, StatusReady, StatusFailed:
		return Status(raw), nil
	}
	f := apperr.Field("status", "INVALID", "status 必须是 uploading、processing、ready 或 failed")
	return "", &f
}

func parseIDList(raw string) ([]uuid.UUID, *apperr.FieldError) {
	parts := strings.Split(raw, ",")
	ids := make([]uuid.UUID, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			f := apperr.Field("ids", "INVALID", "ids 必须是逗号分隔的 UUID")
			return nil, &f
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		f := apperr.Field("ids", "INVALID", "ids 不能为空")
		return nil, &f
	}
	if len(ids) > MaxListIDs {
		f := apperr.Field("ids", "TOO_MANY", fmt.Sprintf("最多 %d 个 ID", MaxListIDs))
		return nil, &f
	}
	return ids, nil
}

func validateVariant(v Variant) error {
	switch v {
	case VariantOriginal, VariantThumbnail:
		return nil
	}
	return apperr.Validation(apperr.Field("variant", "INVALID", "variant 必须是 original 或 thumbnail"))
}

func validateDisposition(d Disposition) error {
	switch d {
	case DispositionInline, DispositionAttachment:
		return nil
	}
	return apperr.Validation(apperr.Field("disposition", "INVALID", "disposition 必须是 inline 或 attachment"))
}

// thumbnailFileName 把原名换成 .jpg：缩略图一律是 JPEG。
func thumbnailFileName(original string) string {
	ext := path.Ext(original)
	base := strings.TrimSuffix(original, ext)
	if base == "" {
		base = "thumbnail"
	}
	return base + "_thumb.jpg"
}

// normalizeMediaType 去掉参数并小写，例如 "image/JPEG; charset=x" → "image/jpeg"。
func normalizeMediaType(raw string) string {
	base, _, _ := strings.Cut(raw, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// decodeHex 把已校验的 64 位十六进制摘要转为 32 字节；调用前须通过 isHexSHA256。
func decodeHex(s string) []byte {
	out, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return out
}
