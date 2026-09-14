// 对象存储集成测试：对真实 S3 兼容服务验证直传、条件复制与下载授权。
// 需要环境变量 TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT（见 apps/server/.env.example）；未设置时跳过。
// 开发环境：docker compose -f infra/compose.yaml up -d minio && docker compose -f infra/compose.yaml run --rm minio-init
package integration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/objectstore"
)

func newTestStore(t *testing.T) *objectstore.S3Store {
	t.Helper()
	endpoint := os.Getenv("TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT")
	if endpoint == "" {
		t.Skip("TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT 未设置，跳过对象存储集成测试")
	}
	bucket := os.Getenv("TRIPFOLIO_TEST_OBJECTSTORE_BUCKET")
	if bucket == "" {
		bucket = "tripfolio"
	}
	store, err := objectstore.NewS3Store(objectstore.Config{
		Endpoint:        endpoint,
		Region:          envOr("TRIPFOLIO_TEST_OBJECTSTORE_REGION", "us-east-1"),
		Bucket:          bucket,
		AccessKeyID:     envOr("TRIPFOLIO_TEST_OBJECTSTORE_ACCESS_KEY", "tripfolio"),
		SecretAccessKey: envOr("TRIPFOLIO_TEST_OBJECTSTORE_SECRET_KEY", "tripfolio-dev-secret"),
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("创建对象存储客户端：%v", err)
	}
	return store
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// TestObjectStoreUploadConfirmFlow 覆盖设计的完整上传链路：
// 签发暂存直传授权 → 客户端 PUT → 服务端校验读取 ETag → 以该 ETag 为条件复制到最终键。
func TestObjectStoreUploadConfirmFlow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	keys := objectstore.KeysFor(uuid.New(), uuid.New(), 1)
	body := []byte("hello tripfolio 你好")
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), keys.Staging, keys.Final)
	})

	auth, err := store.AuthorizeUpload(ctx, keys.Staging, "text/plain", int64(len(body)), 10*time.Minute)
	if err != nil {
		t.Fatalf("签发上传授权：%v", err)
	}
	if auth.Method != http.MethodPut {
		t.Errorf("上传方法应为 PUT，得到 %s", auth.Method)
	}
	if auth.RequiredHeaders["Content-Type"] != "text/plain" {
		t.Errorf("必需头缺少 Content-Type：%v", auth.RequiredHeaders)
	}

	putObject(t, auth, body)

	info, err := store.Head(ctx, keys.Staging)
	if err != nil {
		t.Fatalf("读取暂存对象：%v", err)
	}
	if info.ByteSize != int64(len(body)) {
		t.Errorf("暂存对象大小 %d，期望 %d", info.ByteSize, len(body))
	}
	if info.ETag == "" {
		t.Fatal("暂存对象缺少 ETag，无法条件复制")
	}
	if strings.HasPrefix(info.ETag, `"`) {
		t.Errorf("ETag 未去引号：%q", info.ETag)
	}

	// 内容可完整读回，供 worker 校验摘要与嗅探类型。
	rc, _, err := store.Get(ctx, keys.Staging)
	if err != nil {
		t.Fatalf("打开暂存对象：%v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, body) {
		t.Errorf("暂存对象内容不符：%q", got)
	}

	// 关键规则：ETag 不匹配说明暂存对象在校验后被改写，必须判定失败而不是照常复制。
	if err := store.CopyIfMatch(ctx, keys.Staging, keys.Final, "deadbeefdeadbeefdeadbeefdeadbeef", "text/plain"); err == nil {
		t.Error("ETag 不匹配时条件复制应失败")
	} else if !errors.Is(err, objectstore.ErrPreconditionFailed) {
		t.Errorf("ETag 不匹配应返回 ErrPreconditionFailed，得到 %v", err)
	}
	if _, err := store.Head(ctx, keys.Final); err == nil {
		t.Error("条件复制失败后不应产生最终对象")
	}

	if err := store.CopyIfMatch(ctx, keys.Staging, keys.Final, info.ETag, "text/plain"); err != nil {
		t.Fatalf("条件复制：%v", err)
	}
	final, err := store.Head(ctx, keys.Final)
	if err != nil {
		t.Fatalf("读取最终对象：%v", err)
	}
	if final.ByteSize != int64(len(body)) {
		t.Errorf("最终对象大小 %d，期望 %d", final.ByteSize, len(body))
	}
}

// TestObjectStoreDownloadAuthorizationFixesResponseHeaders 验证下载授权固定响应头，
// 避免在存储域内联渲染任意内容，并保证非 ASCII 文件名可用。
func TestObjectStoreDownloadAuthorizationFixesResponseHeaders(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	keys := objectstore.KeysFor(uuid.New(), uuid.New(), 1)
	body := []byte("%PDF-1.7 fake")
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), keys.Final)
	})

	if err := store.Put(ctx, keys.Final, "application/pdf", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("写入对象：%v", err)
	}

	dl, err := store.AuthorizeDownload(ctx, keys.Final, "application/pdf", "行程单 2026.pdf", objectstore.DispositionAttachment, time.Minute)
	if err != nil {
		t.Fatalf("签发下载授权：%v", err)
	}
	resp, err := http.Get(dl.URL)
	if err != nil {
		t.Fatalf("下载：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载状态 %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, body) {
		t.Errorf("下载内容不符：%q", got)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type 未被签名固定：%q", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition 应为 attachment：%q", cd)
	}
	if !strings.Contains(cd, "filename*=UTF-8''") {
		t.Errorf("Content-Disposition 缺少 RFC 5987 文件名：%q", cd)
	}
}

// TestObjectStoreMissingObjectAndIdempotentDelete 验证不存在对象折叠为 ErrNotFound，
// 且删除不存在的键视为成功，便于清理任务重试。
func TestObjectStoreMissingObjectAndIdempotentDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	missing := "assets/" + uuid.NewString() + "/missing"

	if _, err := store.Head(ctx, missing); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("Head 不存在对象应返回 ErrNotFound，得到 %v", err)
	}
	if _, _, err := store.Get(ctx, missing); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("Get 不存在对象应返回 ErrNotFound，得到 %v", err)
	}
	if err := store.Delete(ctx, missing); err != nil {
		t.Errorf("删除不存在的键应视为成功，得到 %v", err)
	}
	// 重复删除同样成功。
	if err := store.Delete(ctx, missing, ""); err != nil {
		t.Errorf("重复删除应视为成功，得到 %v", err)
	}
}

func putObject(t *testing.T, auth objectstore.UploadAuthorization, body []byte) {
	t.Helper()
	req, err := http.NewRequest(auth.Method, auth.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造直传请求：%v", err)
	}
	for k, v := range auth.RequiredHeaders {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("直传：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(resp.Body)
		t.Fatalf("直传状态 %d：%s", resp.StatusCode, detail)
	}
}
