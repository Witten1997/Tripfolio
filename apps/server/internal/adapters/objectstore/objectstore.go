// Package objectstore 访问私有对象存储：签发直传与下载授权、读取暂存对象、按 ETag 条件复制到最终键、删除对象。
// 实现基于 S3 兼容协议，开发用 MinIO，生产用阿里云 OSS；厂商差异只体现在 Config，调用方不感知。
//
// 对象键由服务端按 account_id、asset_id、upload_attempt 确定，客户端不参与拼接（数据库设计第 14 节）。
// 暂存前缀独立，便于在桶上配置短期自动清理策略回收未确认的上传。
package objectstore

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// Variant 是下载授权针对的对象变体。
type Variant string

const (
	// VariantOriginal 是上传的原始对象。
	VariantOriginal Variant = "original"
	// VariantThumbnail 是 worker 生成的缩略图。
	VariantThumbnail Variant = "thumbnail"
)

// Disposition 控制下载响应的 Content-Disposition。
type Disposition string

const (
	// DispositionInline 让浏览器内联展示（图片预览、PDF 阅读）。
	DispositionInline Disposition = "inline"
	// DispositionAttachment 触发下载保存。
	DispositionAttachment Disposition = "attachment"
)

// ErrNotFound 表示对象不存在。调用方据此判断暂存对象是否真的上传过。
// 它同时带 IsObjectNotFound 方法，使用方可以不导入本包、只凭错误链上的方法识别。
var ErrNotFound error = notFoundError{}

// ErrPreconditionFailed 表示条件复制的 If-Match ETag 不匹配：
// 暂存对象在校验后被改写，按设计判定本次上传失败。
var ErrPreconditionFailed error = preconditionError{}

type notFoundError struct{}

func (notFoundError) Error() string          { return "对象不存在" }
func (notFoundError) IsObjectNotFound() bool { return true }

type preconditionError struct{}

func (preconditionError) Error() string              { return "对象 ETag 与条件不匹配" }
func (preconditionError) IsPreconditionFailed() bool { return true }

// UploadAuthorization 是暂存对象的直传授权。客户端按 Method 与 RequiredHeaders 直传，不经过 API。
type UploadAuthorization struct {
	Method          string
	URL             string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
}

// DownloadAuthorization 是单个对象的短期下载授权。
type DownloadAuthorization struct {
	URL       string
	ExpiresAt time.Time
}

// ObjectInfo 是对象的校验信息。ETag 用作后续条件复制的前提。
type ObjectInfo struct {
	ETag        string
	ByteSize    int64
	ContentType string
}

// Keys 是一个资产某次上传尝试的三个对象键。
type Keys struct {
	Staging   string
	Final     string
	Thumbnail string
}

// KeysFor 按账号、资产与尝试序号推导对象键。同一尝试重复推导结果相同，
// 便于任务重试复用最终键并清理失败遗留对象。
func KeysFor(accountID, assetID uuid.UUID, uploadAttempt int) Keys {
	return Keys{
		Staging:   fmt.Sprintf("staging/%s/%s/%d", accountID, assetID, uploadAttempt),
		Final:     fmt.Sprintf("assets/%s/%s/%d", accountID, assetID, uploadAttempt),
		Thumbnail: fmt.Sprintf("thumbnails/%s/%s/%d", accountID, assetID, uploadAttempt),
	}
}

// Store 是对象存储能力。使用方（modules/assets、worker）按需声明自己的窄接口，
// 这里给出适配器提供的完整能力集合。
type Store interface {
	// AuthorizeUpload 签发暂存对象的 PUT 授权。contentType 与 contentLength 写入签名，
	// 客户端必须按 RequiredHeaders 发送，否则存储端拒绝。
	AuthorizeUpload(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (UploadAuthorization, error)

	// AuthorizeDownload 签发 GET 授权，并固定响应的 Content-Type 与 Content-Disposition，
	// 避免在存储域内联渲染任意内容。fileName 按 RFC 5987 编码。
	AuthorizeDownload(ctx context.Context, key, contentType, fileName string, disposition Disposition, ttl time.Duration) (DownloadAuthorization, error)

	// Head 读取对象的大小、类型与 ETag；对象不存在返回 ErrNotFound。
	Head(ctx context.Context, key string) (ObjectInfo, error)

	// Get 打开对象内容供校验与图片处理；调用方负责关闭。对象不存在返回 ErrNotFound。
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)

	// CopyIfMatch 以 srcETag 为条件把 src 复制到 dst；ETag 不匹配返回 ErrPreconditionFailed。
	CopyIfMatch(ctx context.Context, srcKey, dstKey, srcETag, contentType string) error

	// Put 写入服务端生成的对象（缩略图、导出包）。
	Put(ctx context.Context, key, contentType string, body io.Reader, contentLength int64) error

	// Delete 删除对象；对象不存在视为成功，便于清理任务重试。
	Delete(ctx context.Context, keys ...string) error
}
