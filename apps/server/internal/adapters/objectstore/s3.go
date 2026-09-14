package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Config 是 S3 兼容存储的连接配置。
// MinIO（开发）：Endpoint=http://localhost:9000、UsePathStyle=true、Region 任意。
// 阿里云 OSS（生产）：Endpoint=https://oss-cn-hangzhou.aliyuncs.com、UsePathStyle=false、Region=oss-cn-hangzhou。
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// UsePathStyle 为 true 时用 endpoint/bucket/key 寻址（MinIO 必需）；
	// false 时用 bucket.endpoint 虚拟主机寻址（OSS 与 AWS 默认）。
	UsePathStyle bool
}

// S3Store 基于 S3 兼容 API 实现 Store。
type S3Store struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	endpoint string
}

// NewS3Store 创建 S3 兼容存储客户端。不在构造时访问网络，连通性由就绪探针检查。
func NewS3Store(cfg Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("objectstore: Bucket 不能为空")
	}
	if cfg.Endpoint == "" {
		return nil, errors.New("objectstore: Endpoint 不能为空")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("objectstore: 访问密钥不能为空")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: cfg.UsePathStyle,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	})
	return &S3Store{
		client:   client,
		presign:  s3.NewPresignClient(client),
		bucket:   cfg.Bucket,
		endpoint: cfg.Endpoint,
	}, nil
}

// Bucket 返回桶名，供就绪探针与日志使用。
func (s *S3Store) Bucket() string { return s.bucket }

// AuthorizeUpload 实现 Store。Content-Type 与 Content-Length 进入签名，
// 客户端必须原样发送，避免声明类型与实际上传不一致。
func (s *S3Store) AuthorizeUpload(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (UploadAuthorization, error) {
	if key == "" {
		return UploadAuthorization{}, errors.New("objectstore: key 不能为空")
	}
	if contentLength <= 0 {
		return UploadAuthorization{}, errors.New("objectstore: contentLength 必须为正")
	}
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(contentLength),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return UploadAuthorization{}, fmt.Errorf("签发上传授权: %w", err)
	}
	// 只回传签名覆盖的头，客户端漏发或改动都会导致存储端拒绝。
	headers := map[string]string{
		"Content-Type":   contentType,
		"Content-Length": fmt.Sprintf("%d", contentLength),
	}
	return UploadAuthorization{
		Method:          req.Method,
		URL:             req.URL,
		RequiredHeaders: headers,
		ExpiresAt:       time.Now().Add(ttl),
	}, nil
}

// AuthorizeDownload 实现 Store。固定 response-content-type 与 response-content-disposition，
// 文件名按 RFC 5987 编码，避免非 ASCII 文件名破坏响应头。
func (s *S3Store) AuthorizeDownload(ctx context.Context, key, contentType, fileName string, disposition Disposition, ttl time.Duration) (DownloadAuthorization, error) {
	if key == "" {
		return DownloadAuthorization{}, errors.New("objectstore: key 不能为空")
	}
	if disposition != DispositionInline && disposition != DispositionAttachment {
		return DownloadAuthorization{}, fmt.Errorf("objectstore: 未知的 disposition %q", disposition)
	}
	input := &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(contentDisposition(disposition, fileName)),
	}
	if contentType != "" {
		input.ResponseContentType = aws.String(contentType)
	}
	req, err := s.presign.PresignGetObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return DownloadAuthorization{}, fmt.Errorf("签发下载授权: %w", err)
	}
	return DownloadAuthorization{URL: req.URL, ExpiresAt: time.Now().Add(ttl)}, nil
}

// contentDisposition 生成同时兼容旧客户端与非 ASCII 文件名的头值：
// ASCII 回退用 filename，UTF-8 原名用 RFC 5987 的 filename*。
func contentDisposition(disposition Disposition, fileName string) string {
	if fileName == "" {
		return string(disposition)
	}
	fallback := asciiFallback(fileName)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		disposition, fallback, url.PathEscape(fileName))
}

// asciiFallback 把非 ASCII 与引号等字符替换为下划线，作为 filename 参数的安全回退值。
func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

// Head 实现 Store。
func (s *S3Store) Head(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, mapError(err, key)
	}
	return ObjectInfo{
		ETag:        normalizeETag(aws.ToString(out.ETag)),
		ByteSize:    aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType),
	}, nil
}

// Get 实现 Store。调用方负责关闭返回的 ReadCloser。
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, ObjectInfo{}, mapError(err, key)
	}
	info := ObjectInfo{
		ETag:        normalizeETag(aws.ToString(out.ETag)),
		ByteSize:    aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType),
	}
	return out.Body, info, nil
}

// CopyIfMatch 实现 Store。服务端复制不经过应用进程，不占用带宽与内存。
func (s *S3Store) CopyIfMatch(ctx context.Context, srcKey, dstKey, srcETag, contentType string) error {
	if srcETag == "" {
		return errors.New("objectstore: 条件复制必须提供源 ETag")
	}
	input := &s3.CopyObjectInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(dstKey),
		CopySource:        aws.String(s.bucket + "/" + srcKey),
		CopySourceIfMatch: aws.String(srcETag),
		MetadataDirective: types.MetadataDirectiveReplace,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if _, err := s.client.CopyObject(ctx, input); err != nil {
		return mapError(err, srcKey)
	}
	return nil
}

// Put 实现 Store。
func (s *S3Store) Put(ctx context.Context, key, contentType string, body io.Reader, contentLength int64) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if contentLength > 0 {
		input.ContentLength = aws.Int64(contentLength)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("写入对象 %s: %w", key, err)
	}
	return nil
}

// Delete 实现 Store。对象不存在视为成功。
func (s *S3Store) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if key == "" {
			continue
		}
		_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil && !errors.Is(mapError(err, key), ErrNotFound) {
			return fmt.Errorf("删除对象 %s: %w", key, err)
		}
	}
	return nil
}

// mapError 把 S3 错误折叠为包级哨兵错误，隔离 SDK 类型。
func mapError(err error, key string) error {
	var noSuchKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
		return fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return fmt.Errorf("%s: %w", key, ErrNotFound)
		case "PreconditionFailed":
			return fmt.Errorf("%s: %w", key, ErrPreconditionFailed)
		}
	}
	var respErr interface{ HTTPStatusCode() int }
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 404:
			return fmt.Errorf("%s: %w", key, ErrNotFound)
		case 412:
			return fmt.Errorf("%s: %w", key, ErrPreconditionFailed)
		}
	}
	return err
}

// normalizeETag 去掉 S3 返回值两端的引号，便于与后续条件请求比较。
func normalizeETag(etag string) string {
	return strings.Trim(etag, `"`)
}
