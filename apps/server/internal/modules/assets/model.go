// Package assets 是私有文件资产业务：登记资产、签发上传授权、确认上传、签发下载授权。
// 本包不依赖 HTTP、pgx、sqlc 或对象存储 SDK；外部能力通过 ports.go 的窄接口注入。
//
// 核心约束（接口设计 3.7、数据库设计第 14 节）：
//   - 对象键由服务端推导，客户端既不能提供也读不到。
//   - 确认接口不信任客户端的「上传成功」声明，只转入 processing 并入队校验。
//   - 引用不要求 ready：资料、照片、票据可在任何状态建立引用，下载只对 ready 有效。
package assets

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// EntityType 是资产的同步实体类型。
const EntityType = "asset"

// Scope 是资产范围。
type Scope string

const (
	// ScopeTrip 是属于某次旅行的文件：照片、资料、票据。
	ScopeTrip Scope = "trip"
	// ScopeAvatar 是账号头像，不属于任何旅行，也不进入旅行同步流。
	ScopeAvatar Scope = "avatar"
)

// Status 是上传与校验状态。删除只用 DeletedAt 表达，不在此枚举内。
type Status string

const (
	StatusUploading  Status = "uploading"
	StatusProcessing Status = "processing"
	StatusReady      Status = "ready"
	StatusFailed     Status = "failed"
)

// ThumbnailStatus 是缩略图状态；缩略图失败不影响原图可用。
type ThumbnailStatus string

const (
	ThumbnailNone       ThumbnailStatus = "none"
	ThumbnailProcessing ThumbnailStatus = "processing"
	ThumbnailReady      ThumbnailStatus = "ready"
	ThumbnailFailed     ThumbnailStatus = "failed"
)

// Variant 是下载针对的对象变体。
type Variant string

const (
	VariantOriginal  Variant = "original"
	VariantThumbnail Variant = "thumbnail"
)

// Disposition 是下载响应的 Content-Disposition。
type Disposition string

const (
	DispositionInline     Disposition = "inline"
	DispositionAttachment Disposition = "attachment"
)

// UploadWindow 是一次上传尝试的确认窗口：创建后多久内必须确认。
const UploadWindow = 30 * time.Minute

// 下载授权有效期：原图与 PDF 短，缩略图长以便客户端缓存（接口设计 3.7）。
const (
	OriginalDownloadTTL  = 60 * time.Second
	ThumbnailDownloadTTL = 15 * time.Minute
)

// MaxDownloadBatch 是批量下载授权的资产数上限。
const MaxDownloadBatch = 100

// MaxListIDs 是 listTripAssets 的 ids 过滤上限。
const MaxListIDs = 100

// Resource 是资产的对外资源，也是同步日志与快照中的表示（接口设计 3.7）。
// 暂存键、最终对象键与回收字段不在此结构内，不会泄漏给客户端。
type Resource struct {
	ID              uuid.UUID            `json:"id"`
	TripID          *uuid.UUID           `json:"trip_id"`
	Scope           Scope                `json:"scope"`
	OriginalName    string               `json:"original_name"`
	Status          Status               `json:"status"`
	UploadAttempt   int32                `json:"upload_attempt"`
	UploadExpiresAt *time.Time           `json:"upload_expires_at"`
	MediaType       *string              `json:"media_type"`
	ByteSize        *int64               `json:"byte_size"`
	SHA256          *string              `json:"sha256"`
	Width           *int32               `json:"width"`
	Height          *int32               `json:"height"`
	ExifTakenAt     *types.LocalDateTime `json:"exif_taken_at_local"`
	ExifLatitude    *float64             `json:"exif_latitude"`
	ExifLongitude   *float64             `json:"exif_longitude"`
	ThumbnailStatus ThumbnailStatus      `json:"thumbnail_status"`
	ErrorCode       *string              `json:"error_code"`
	Version         types.Version        `json:"version"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	DeletedAt       *time.Time           `json:"deleted_at"`
}

// UploadAuthorization 是暂存对象的直传授权。
type UploadAuthorization struct {
	UploadAttempt   int32             `json:"upload_attempt"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	RequiredHeaders map[string]string `json:"required_headers"`
	ExpiresAt       time.Time         `json:"expires_at"`
}

// DownloadAuthorization 是单个资产的下载授权。非 ready 或变体不可用时 URL 为空。
type DownloadAuthorization struct {
	AssetID         uuid.UUID       `json:"asset_id"`
	Status          Status          `json:"status"`
	ThumbnailStatus ThumbnailStatus `json:"thumbnail_status"`
	URL             *string         `json:"url"`
	ExpiresAt       *time.Time      `json:"expires_at"`
	FileName        string          `json:"file_name"`
	MediaType       *string         `json:"media_type"`
	ByteSize        *int64          `json:"byte_size"`
}

// 错误代码，全部取自接口设计 1.4，不新增。
const (
	CodeUploadNotAllowed = "UPLOAD_NOT_ALLOWED"
	CodeUploadExpired    = "UPLOAD_EXPIRED"
	CodeAssetNotReady    = "ASSET_NOT_READY"
	CodeRequestTooLarge  = "REQUEST_TOO_LARGE"
	CodeUnsupportedType  = "UNSUPPORTED_MEDIA_TYPE"
)

// AcceptsUploadAttempt 判断当前状态是否还接受上传尝试（续签或新尝试）。
// processing 与 ready 都不接受，返回 UPLOAD_NOT_ALLOWED（接口设计 3.7）。
func (r Resource) AcceptsUploadAttempt() bool {
	return r.Status == StatusUploading || r.Status == StatusFailed
}

// AttemptExpired 判断当前尝试是否已超过确认窗口。
// 未过期的 uploading 可续签同一暂存键；已过期或 failed 需要开新尝试。
func (r Resource) AttemptExpired(now time.Time) bool {
	return r.UploadExpiresAt == nil || now.After(*r.UploadExpiresAt)
}

// recordWrite 把资产写入登记为主资源，并在资产属于旅行时写入变更日志。
// avatar 资产不写变更日志（数据库设计表 14、16）：sync_changes 的 CHECK 只允许 expense_category 的
// trip_id 为空；头像由其他设备通过 GET /account 与下载授权获取，不依赖同步流。
func recordWrite(scope write.Scope, res Resource, changedFields []string) {
	scope.SetPrimary(write.Ref(EntityType, res.ID, int64(res.Version)))
	if res.TripID == nil {
		return
	}
	scope.Record(write.Change{
		EntityType: EntityType, EntityID: res.ID, TripID: res.TripID,
		Version: int64(res.Version), Kind: write.ChangeUpsert, Snapshot: res, ChangedFields: changedFields,
	})
}
