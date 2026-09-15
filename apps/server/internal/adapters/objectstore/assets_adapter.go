package objectstore

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/modules/assets"
)

// AssetsStore 把 S3Store 适配为 assets 模块声明的 ObjectKeys、ObjectAuthorizer 与 ObjectProcessor。
// 两边的结构字段一一对应，只是类型分属两个包；模块不导入本包。
type AssetsStore struct {
	store *S3Store
}

// NewAssetsStore 创建适配。
func NewAssetsStore(store *S3Store) *AssetsStore { return &AssetsStore{store: store} }

var (
	_ assets.ObjectKeys       = (*AssetsStore)(nil)
	_ assets.ObjectAuthorizer = (*AssetsStore)(nil)
	_ assets.ObjectProcessor  = (*AssetsStore)(nil)
)

// KeysFor 实现 assets.ObjectKeys。
func (a *AssetsStore) KeysFor(accountID, assetID uuid.UUID, uploadAttempt int) assets.Keys {
	k := KeysFor(accountID, assetID, uploadAttempt)
	return assets.Keys{Staging: k.Staging, Final: k.Final, Thumbnail: k.Thumbnail}
}

// AuthorizeUpload 实现 assets.ObjectAuthorizer。
func (a *AssetsStore) AuthorizeUpload(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (assets.UploadAuthorization, error) {
	auth, err := a.store.AuthorizeUpload(ctx, key, contentType, contentLength, ttl)
	if err != nil {
		return assets.UploadAuthorization{}, err
	}
	return assets.UploadAuthorization{
		Method: auth.Method, URL: auth.URL, RequiredHeaders: auth.RequiredHeaders, ExpiresAt: auth.ExpiresAt,
	}, nil
}

// AuthorizeDownload 实现 assets.ObjectAuthorizer。
func (a *AssetsStore) AuthorizeDownload(ctx context.Context, key, contentType, fileName string, disposition assets.Disposition, ttl time.Duration) (assets.DownloadAuthorization, error) {
	auth, err := a.store.AuthorizeDownload(ctx, key, contentType, fileName, Disposition(disposition), ttl)
	if err != nil {
		return assets.DownloadAuthorization{}, err
	}
	url, expires := auth.URL, auth.ExpiresAt
	return assets.DownloadAuthorization{URL: &url, ExpiresAt: &expires}, nil
}

// Head 实现 assets.ObjectProcessor。
func (a *AssetsStore) Head(ctx context.Context, key string) (assets.ObjectInfo, error) {
	info, err := a.store.Head(ctx, key)
	return toAssetsInfo(info), err
}

// Get 实现 assets.ObjectProcessor。
func (a *AssetsStore) Get(ctx context.Context, key string) (io.ReadCloser, assets.ObjectInfo, error) {
	rc, info, err := a.store.Get(ctx, key)
	return rc, toAssetsInfo(info), err
}

// CopyIfMatch 实现 assets.ObjectProcessor。
func (a *AssetsStore) CopyIfMatch(ctx context.Context, srcKey, dstKey, srcETag, contentType string) error {
	return a.store.CopyIfMatch(ctx, srcKey, dstKey, srcETag, contentType)
}

// Put 实现 assets.ObjectProcessor。
func (a *AssetsStore) Put(ctx context.Context, key, contentType string, body io.Reader, contentLength int64) error {
	return a.store.Put(ctx, key, contentType, body, contentLength)
}

// Delete 实现 assets.ObjectProcessor。
func (a *AssetsStore) Delete(ctx context.Context, keys ...string) error {
	return a.store.Delete(ctx, keys...)
}

func toAssetsInfo(info ObjectInfo) assets.ObjectInfo {
	return assets.ObjectInfo{ETag: info.ETag, ByteSize: info.ByteSize, ContentType: info.ContentType}
}
