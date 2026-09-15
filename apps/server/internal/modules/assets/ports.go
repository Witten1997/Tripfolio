package assets

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// Keys 是一次上传尝试的三个对象键。由 ObjectKeys 推导，业务代码不拼接字符串。
type Keys struct {
	Staging   string
	Final     string
	Thumbnail string
}

// ObjectKeys 按账号、资产与尝试序号推导对象键。
// 由 adapters/objectstore 实现（其 KeysFor 的结构与 Keys 一致）。
type ObjectKeys interface {
	KeysFor(accountID, assetID uuid.UUID, uploadAttempt int) Keys
}

// ObjectAuthorizer 签发直传与下载授权。实现在 adapters/objectstore。
type ObjectAuthorizer interface {
	AuthorizeUpload(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (UploadAuthorization, error)
	AuthorizeDownload(ctx context.Context, key, contentType, fileName string, disposition Disposition, ttl time.Duration) (DownloadAuthorization, error)
}

// ObjectProcessor 是 worker 校验与复制暂存对象所需的能力。
type ObjectProcessor interface {
	Head(ctx context.Context, key string) (ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	CopyIfMatch(ctx context.Context, srcKey, dstKey, srcETag, contentType string) error
	Put(ctx context.Context, key, contentType string, body io.Reader, contentLength int64) error
	Delete(ctx context.Context, keys ...string) error
}

// ObjectInfo 是对象的校验信息。
type ObjectInfo struct {
	ETag        string
	ByteSize    int64
	ContentType string
}

// UploadLimits 是按类型的大小与类型白名单，来自 modules/metadata。
type UploadLimits struct {
	ImageMaxBytes   int64
	PDFMaxBytes     int64
	ImageMediaTypes []string
	PDFMediaTypes   []string
}

// TripInfo 是资产所属旅行的窄只读视图：只需确认存在与是否在回收站。
type TripInfo struct {
	DeletedAt *time.Time
}

// AttemptInfo 是上传尝试的私有信息：客户端在创建时声明，不进入公开模型与同步快照。
// 直传签名与 worker 校验都以它为依据；ClientSHA256 为 nil 表示客户端未提供摘要。
type AttemptInfo struct {
	ExpectedSize      int64
	DeclaredMediaType string
	ClientSHA256      []byte
}

// ImageInfo 是 worker 从图片内容读出的尺寸与 EXIF 建议值。
type ImageInfo struct {
	Width     int32
	Height    int32
	TakenAt   *types.LocalDateTime
	Latitude  *float64
	Longitude *float64
}

// 图片处理的分类错误，由 adapters/imaging 的适配层映射，模块据此决定失败代码。
var (
	// ErrNotImage 表示内容不是可解码的图片。
	ErrNotImage = errors.New("内容不是可解码的图片")
	// ErrImageTooLarge 表示图片像素数超过解码上限。
	ErrImageTooLarge = errors.New("图片尺寸超过解码上限")
)

// ImageProcessor 是 worker 需要的图片能力。
type ImageProcessor interface {
	// SniffMediaType 按内容魔数识别类型；不认识返回空串。
	SniffMediaType(data []byte) string
	// Inspect 只解码头部读取尺寸与 EXIF；超限返回 ErrImageTooLarge，不可解码返回 ErrNotImage。
	Inspect(data []byte) (ImageInfo, error)
	// Thumbnail 生成 JPEG 缩略图。
	Thumbnail(data []byte) ([]byte, error)
}

// VerifiedInfo 是 worker 校验通过后写回资产的元数据。
type VerifiedInfo struct {
	MediaType string
	ByteSize  int64
	SHA256    []byte
	FinalKey  string
	Image     *ImageInfo
}

// Reader 是资产的事务外只读查询。
type Reader interface {
	// Get 读取单个资产；不存在或非本人返回 found=false。
	Get(ctx context.Context, accountID, assetID uuid.UUID) (Resource, bool, error)
	// Attempt 读取上传尝试的私有信息；不存在或非本人返回 found=false。
	Attempt(ctx context.Context, accountID, assetID uuid.UUID) (AttemptInfo, bool, error)
	// GetMany 按输入顺序批量读取；不属于本账号的 ID 不出现在结果中，由服务判定整批 404。
	GetMany(ctx context.Context, accountID uuid.UUID, ids []uuid.UUID) ([]Resource, error)
	// ListByTrip 按状态或 ID 集合分页查询旅行资产，按 created_at、id 均降序。
	ListByTrip(ctx context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error)
	// Trip 读取旅行归属信息；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
}

// ListQuery 是 listTripAssets 解析后的查询条件。
type ListQuery struct {
	Status *Status
	IDs    []uuid.UUID
	After  *ListPosition
	Limit  int
}

// ListPosition 是键集分页位置：创建时间与 ID，均降序。
type ListPosition struct {
	CreatedAt time.Time `json:"c"`
	ID        uuid.UUID `json:"i"`
}

// Repo 是一次写事务内的资产仓储。资产没有 PATCH，因此不需要字段级合并的变更历史。
type Repo interface {
	// IDExists 判断 ID 是否已被使用（含软删除行与墓碑），用于创建时的 ID_ALREADY_USED。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	// Insert 写入新资产、声明信息及首个上传尝试的暂存键。
	Insert(ctx context.Context, accountID uuid.UUID, res Resource, attempt AttemptInfo, stagingKey string) (Resource, error)
	// GetForUpdate 在事务内读取资产并加锁，供状态机转换。
	GetForUpdate(ctx context.Context, accountID, assetID uuid.UUID) (Resource, bool, error)
	// StartAttempt 递增尝试序号并写入新暂存键与截止时间，清空上次错误码。
	StartAttempt(ctx context.Context, accountID, assetID uuid.UUID, attempt int32, stagingKey string, expiresAt, now time.Time) (Resource, error)
	// RenewAttempt 只延长当前尝试的截止时间，暂存键不变。
	RenewAttempt(ctx context.Context, accountID, assetID uuid.UUID, expiresAt, now time.Time) (Resource, error)
	// MarkProcessing 把资产转入 processing 并记录确认时间。
	MarkProcessing(ctx context.Context, accountID, assetID uuid.UUID, now time.Time) (Resource, error)
	// MarkReady 写入校验结果：最终键、类型、大小、摘要、尺寸与 EXIF 建议值，清空暂存键；
	// 图片的缩略图状态置为 processing，其余为 none。
	MarkReady(ctx context.Context, accountID, assetID uuid.UUID, info VerifiedInfo, now time.Time) (Resource, error)
	// MarkFailed 记录可公开的失败代码并清空暂存键，等待用户开启新尝试。
	MarkFailed(ctx context.Context, accountID, assetID uuid.UUID, errorCode string, now time.Time) (Resource, error)
	// SetThumbnail 写入缩略图结果；status 为 ready 时 thumbnailKey 非空。
	SetThumbnail(ctx context.Context, accountID, assetID uuid.UUID, status ThumbnailStatus, thumbnailKey string, now time.Time) (Resource, error)
}

// VerifyJobArgs 是确认后入队的校验任务参数；Kind 即 River 任务类型，worker 在 transport/river 注册。
type VerifyJobArgs struct {
	AccountID     uuid.UUID `json:"account_id"`
	AssetID       uuid.UUID `json:"asset_id"`
	UploadAttempt int32     `json:"upload_attempt"`
}

// Kind 实现 write.JobArgs 与 river.JobArgs。
func (VerifyJobArgs) Kind() string { return "asset_verify" }
