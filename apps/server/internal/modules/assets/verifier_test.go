package assets_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
)

// fakeBucket 模拟对象存储：按键存内容与 ETag，可注入暂时性故障与 ETag 变化。
type fakeBucket struct {
	objects   map[string][]byte
	etags     map[string]string
	getErr    error
	copyErr   error
	putErr    error
	copyCalls int
	deleted   []string
	// mutateAfterGet 在 Get 之后改写暂存对象，模拟校验后被改写导致条件复制失败。
	mutateAfterGet bool
}

type objNotFound struct{}

func (objNotFound) Error() string          { return "对象不存在" }
func (objNotFound) IsObjectNotFound() bool { return true }

type objPrecondition struct{}

func (objPrecondition) Error() string              { return "ETag 不匹配" }
func (objPrecondition) IsPreconditionFailed() bool { return true }

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: map[string][]byte{}, etags: map[string]string{}}
}

func (b *fakeBucket) put(key string, data []byte) {
	b.objects[key] = data
	sum := sha256.Sum256(data)
	b.etags[key] = string(sum[:8])
}

func (b *fakeBucket) Head(_ context.Context, key string) (assets.ObjectInfo, error) {
	data, ok := b.objects[key]
	if !ok {
		return assets.ObjectInfo{}, objNotFound{}
	}
	return assets.ObjectInfo{ETag: b.etags[key], ByteSize: int64(len(data))}, nil
}

func (b *fakeBucket) Get(_ context.Context, key string) (io.ReadCloser, assets.ObjectInfo, error) {
	if b.getErr != nil {
		return nil, assets.ObjectInfo{}, b.getErr
	}
	data, ok := b.objects[key]
	if !ok {
		return nil, assets.ObjectInfo{}, objNotFound{}
	}
	info := assets.ObjectInfo{ETag: b.etags[key], ByteSize: int64(len(data))}
	if b.mutateAfterGet {
		// 读完后对象被改写：ETag 变了，后续条件复制必须失败。
		b.put(key, append(append([]byte{}, data...), '!'))
	}
	return io.NopCloser(bytes.NewReader(data)), info, nil
}

func (b *fakeBucket) CopyIfMatch(_ context.Context, src, dst, etag, _ string) error {
	b.copyCalls++
	if b.copyErr != nil {
		return b.copyErr
	}
	data, ok := b.objects[src]
	if !ok {
		return objNotFound{}
	}
	if b.etags[src] != etag {
		return objPrecondition{}
	}
	b.put(dst, data)
	return nil
}

func (b *fakeBucket) Put(_ context.Context, key, _ string, body io.Reader, _ int64) error {
	if b.putErr != nil {
		return b.putErr
	}
	data, _ := io.ReadAll(body)
	b.put(key, data)
	return nil
}

func (b *fakeBucket) Delete(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(b.objects, k)
		delete(b.etags, k)
		b.deleted = append(b.deleted, k)
	}
	return nil
}

// fakeImages 按魔数嗅探并返回可配置的图片信息。
type fakeImages struct {
	inspectErr error
	thumbErr   error
	info       assets.ImageInfo
}

func (f *fakeImages) SniffMediaType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf"
	}
	return ""
}

func (f *fakeImages) Inspect(_ []byte) (assets.ImageInfo, error) {
	if f.inspectErr != nil {
		return assets.ImageInfo{}, f.inspectErr
	}
	return f.info, nil
}

func (f *fakeImages) Thumbnail(_ []byte) ([]byte, error) {
	if f.thumbErr != nil {
		return nil, f.thumbErr
	}
	return []byte{0xFF, 0xD8, 0xFF, 't', 'h', 'u', 'm', 'b'}, nil
}

type verifyFixture struct {
	store    *assets.MemoryStore
	uow      *write.MemoryUnitOfWork[assets.Repo]
	bucket   *fakeBucket
	images   *fakeImages
	keys     *fakeObjects
	verifier *assets.Verifier
	clock    *clock.Fake
	account  uuid.UUID
	tripID   uuid.UUID
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	store := assets.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[assets.Repo](store)
	clk := clock.NewFake(time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC))
	bucket := newFakeBucket()
	images := &fakeImages{info: assets.ImageInfo{Width: 4000, Height: 3000}}
	keys := &fakeObjects{}
	v := assets.NewVerifier(assets.VerifierDeps{
		UnitOfWork: uow, Reader: store, Keys: keys, Objects: bucket, Images: images,
		Limits: assets.UploadLimits{
			ImageMaxBytes: 1024, PDFMaxBytes: 2048,
			ImageMediaTypes: []string{"image/jpeg", "image/png", "image/webp"},
			PDFMediaTypes:   []string{"application/pdf"},
		},
		Clock: clk, Logger: slog.New(slog.DiscardHandler),
	})
	return &verifyFixture{
		store: store, uow: uow, bucket: bucket, images: images, keys: keys, verifier: v,
		clock: clk, account: uuid.New(), tripID: uuid.New(),
	}
}

// processingAsset 造一个已确认、等待校验的资产，并把内容放进暂存键。
func (f *verifyFixture) processingAsset(t *testing.T, scope assets.Scope, content []byte, clientSHA []byte) assets.VerifyJobArgs {
	t.Helper()
	id := uuid.New()
	now := f.clock.Now()
	var tripID *uuid.UUID
	if scope == assets.ScopeTrip {
		tripID = &f.tripID
	}
	f.store.PutAsset(f.account, assets.Resource{
		ID: id, TripID: tripID, Scope: scope, OriginalName: "photo.jpg",
		Status: assets.StatusProcessing, UploadAttempt: 1, ThumbnailStatus: assets.ThumbnailNone,
		Version: 2, CreatedAt: now, UpdatedAt: now,
	})
	f.store.PutAttempt(id, assets.AttemptInfo{
		ExpectedSize: int64(len(content)), DeclaredMediaType: "image/jpeg", ClientSHA256: clientSHA,
	})
	keys := f.keys.KeysFor(f.account, id, 1)
	f.bucket.put(keys.Staging, content)
	return assets.VerifyJobArgs{AccountID: f.account, AssetID: id, UploadAttempt: 1}
}

func (f *verifyFixture) asset(t *testing.T, id uuid.UUID) assets.Resource {
	t.Helper()
	res, found, err := f.store.Get(context.Background(), f.account, id)
	if err != nil || !found {
		t.Fatalf("读取资产失败：found=%v err=%v", found, err)
	}
	return res
}

var jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'f', 'a', 'k', 'e', 'j', 'p', 'e', 'g'}
var pdfBytes = []byte("%PDF-1.7 fake document")

// 校验通过：ready、元数据来自实际内容、最终键写入、暂存键删除、缩略图就绪。
func TestVerifyHappyPathImage(t *testing.T) {
	f := newVerifyFixture(t)
	taken := types.LocalDateTime("2026-09-10T14:30:00")
	lat, lng := 35.6586, 139.7454
	f.images.info = assets.ImageInfo{Width: 4000, Height: 3000, TakenAt: &taken, Latitude: &lat, Longitude: &lng}
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatalf("Verify：%v", err)
	}

	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusReady {
		t.Fatalf("应为 ready，得到 %s（error_code=%v）", res.Status, res.ErrorCode)
	}
	if res.MediaType == nil || *res.MediaType != "image/jpeg" {
		t.Errorf("media_type 应来自嗅探，得到 %v", res.MediaType)
	}
	if res.ByteSize == nil || *res.ByteSize != int64(len(jpegBytes)) {
		t.Errorf("byte_size 应为实际长度，得到 %v", res.ByteSize)
	}
	sum := sha256.Sum256(jpegBytes)
	if res.SHA256 == nil || len(*res.SHA256) != 64 {
		t.Errorf("sha256 应为 64 位十六进制，得到 %v", res.SHA256)
	} else if (*res.SHA256)[:8] != hexPrefix(sum[:4]) {
		t.Errorf("sha256 与内容不符：%s", *res.SHA256)
	}
	if res.Width == nil || *res.Width != 4000 || res.Height == nil || *res.Height != 3000 {
		t.Errorf("尺寸未写入：%v×%v", res.Width, res.Height)
	}
	// EXIF 建议值原样写入，不做坐标转换。
	if res.ExifTakenAt == nil || *res.ExifTakenAt != taken {
		t.Errorf("exif_taken_at_local 未写入：%v", res.ExifTakenAt)
	}
	if res.ExifLatitude == nil || *res.ExifLatitude != lat || res.ExifLongitude == nil || *res.ExifLongitude != lng {
		t.Errorf("EXIF 坐标未写入：%v,%v", res.ExifLatitude, res.ExifLongitude)
	}
	if res.UploadExpiresAt != nil {
		t.Error("ready 不应保留确认窗口")
	}

	keys := f.keys.KeysFor(f.account, args.AssetID, 1)
	if f.store.FinalKey(args.AssetID) != keys.Final {
		t.Errorf("最终键未写入：%s", f.store.FinalKey(args.AssetID))
	}
	if _, ok := f.bucket.objects[keys.Final]; !ok {
		t.Error("最终对象不存在：条件复制未执行")
	}
	if _, ok := f.bucket.objects[keys.Staging]; ok {
		t.Error("暂存对象应在 ready 后删除")
	}
	// 缩略图链路：状态 ready、对象已写入。
	if res.ThumbnailStatus != assets.ThumbnailReady {
		t.Errorf("缩略图应为 ready，得到 %s", res.ThumbnailStatus)
	}
	if _, ok := f.bucket.objects[keys.Thumbnail]; !ok {
		t.Error("缩略图对象未写入")
	}
	if f.store.ThumbnailKey(args.AssetID) != keys.Thumbnail {
		t.Errorf("缩略图键未写入：%s", f.store.ThumbnailKey(args.AssetID))
	}

	// 两次写事务（ready、thumbnail）各留一条变更日志。
	changes := f.uow.Changes()
	if len(changes) != 2 {
		t.Fatalf("应有 2 条变更日志，得到 %d", len(changes))
	}
	if changes[1].Version <= changes[0].Version {
		t.Error("版本应递增")
	}
}

func hexPrefix(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// PDF 不生成缩略图，thumbnail_status 保持 none。
func TestVerifyPDFSkipsThumbnail(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, pdfBytes, nil)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatalf("Verify：%v", err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusReady {
		t.Fatalf("PDF 应 ready，得到 %s", res.Status)
	}
	if *res.MediaType != "application/pdf" {
		t.Errorf("类型应为 application/pdf，得到 %s", *res.MediaType)
	}
	if res.ThumbnailStatus != assets.ThumbnailNone {
		t.Errorf("PDF 不应有缩略图流程，得到 %s", res.ThumbnailStatus)
	}
	if res.Width != nil || res.Height != nil {
		t.Error("PDF 不应有尺寸")
	}
	if len(f.uow.Changes()) != 1 {
		t.Errorf("PDF 只应有 1 条变更日志，得到 %d", len(f.uow.Changes()))
	}
}

// 声明类型是图片、内容却是 PDF：以嗅探为准，声明不作数。
func TestVerifyUsesSniffedTypeNotDeclared(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, pdfBytes, nil) // 声明 image/jpeg，内容是 PDF

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusReady || *res.MediaType != "application/pdf" {
		t.Errorf("应按内容判定为 PDF 并 ready，得到 %s / %v", res.Status, res.MediaType)
	}
}

// 各类内容问题都判定 failed 并写入对应代码，同时删除暂存对象，且不会触碰最终键。
func TestVerifyFailsWithPublicCodes(t *testing.T) {
	cases := []struct {
		name     string
		content  []byte
		scope    assets.Scope
		sha      []byte
		setup    func(f *verifyFixture)
		wantCode string
	}{
		{
			name: "不认识的类型", content: []byte("GIF89a...."), scope: assets.ScopeTrip,
			wantCode: assets.FailUnsupportedType,
		},
		{
			name: "头像不接受 PDF", content: pdfBytes, scope: assets.ScopeAvatar,
			wantCode: assets.FailUnsupportedType,
		},
		{
			name: "摘要不符", content: jpegBytes, scope: assets.ScopeTrip,
			sha:      bytes.Repeat([]byte{0xAB}, 32),
			wantCode: assets.FailChecksumMismatch,
		},
		{
			name: "图片超大小上限", content: append(append([]byte{}, jpegBytes...), bytes.Repeat([]byte{0}, 1024)...),
			scope: assets.ScopeTrip, wantCode: assets.FailTooLarge,
		},
		{
			name: "像素超限", content: jpegBytes, scope: assets.ScopeTrip,
			setup:    func(f *verifyFixture) { f.images.inspectErr = assets.ErrImageTooLarge },
			wantCode: assets.FailImageTooLarge,
		},
		{
			name: "魔数是图片但解码失败", content: jpegBytes, scope: assets.ScopeTrip,
			setup:    func(f *verifyFixture) { f.images.inspectErr = assets.ErrNotImage },
			wantCode: assets.FailCorruptImage,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			args := f.processingAsset(t, c.scope, c.content, c.sha)

			if err := f.verifier.Verify(context.Background(), args); err != nil {
				t.Fatalf("内容问题应终结任务而非返回错误：%v", err)
			}
			res := f.asset(t, args.AssetID)
			if res.Status != assets.StatusFailed {
				t.Fatalf("应为 failed，得到 %s", res.Status)
			}
			if res.ErrorCode == nil || *res.ErrorCode != c.wantCode {
				t.Errorf("error_code 应为 %s，得到 %v", c.wantCode, res.ErrorCode)
			}
			if f.bucket.copyCalls != 0 {
				t.Error("判定失败前不应执行条件复制")
			}
			keys := f.keys.KeysFor(f.account, args.AssetID, 1)
			if _, ok := f.bucket.objects[keys.Staging]; ok {
				t.Error("失败后应删除暂存对象")
			}
			// failed 资产必须能开新尝试，因此不能残留 ready 才有的字段。
			if res.MediaType != nil || res.ByteSize != nil {
				t.Error("失败的资产不应写入 media_type/byte_size")
			}
		})
	}
}

// 暂存对象不存在：客户端只确认没上传，判定 UPLOAD_MISSING。
func TestVerifyMissingStagingObject(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)
	keys := f.keys.KeysFor(f.account, args.AssetID, 1)
	delete(f.bucket.objects, keys.Staging)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusFailed || *res.ErrorCode != assets.FailUploadMissing {
		t.Errorf("应 failed/UPLOAD_MISSING，得到 %s/%v", res.Status, res.ErrorCode)
	}
}

// 校验后对象被改写：ETag 条件复制失败，判定 UPLOAD_MODIFIED，不能把改写后的内容标 ready。
func TestVerifyRejectsObjectModifiedAfterInspection(t *testing.T) {
	f := newVerifyFixture(t)
	f.bucket.mutateAfterGet = true
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusFailed || *res.ErrorCode != assets.FailUploadModified {
		t.Errorf("应 failed/UPLOAD_MODIFIED，得到 %s/%v", res.Status, res.ErrorCode)
	}
	keys := f.keys.KeysFor(f.account, args.AssetID, 1)
	if _, ok := f.bucket.objects[keys.Final]; ok {
		t.Error("条件复制失败后不应产生最终对象")
	}
}

// 对象存储暂时不可达：返回错误让 worker 重试，资产保持 processing 不被误判。
func TestVerifyTransientStorageErrorIsRetried(t *testing.T) {
	f := newVerifyFixture(t)
	f.bucket.getErr = errors.New("connection refused")
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)

	err := f.verifier.Verify(context.Background(), args)
	if err == nil {
		t.Fatal("暂时性故障应返回错误供重试")
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusProcessing {
		t.Errorf("暂时性故障不应改变状态，得到 %s", res.Status)
	}
	if len(f.uow.Changes()) != 0 {
		t.Error("暂时性故障不应写变更日志")
	}

	// 复制阶段的暂时性故障同样重试。
	f2 := newVerifyFixture(t)
	f2.bucket.copyErr = errors.New("503 slow down")
	args2 := f2.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)
	if err := f2.verifier.Verify(context.Background(), args2); err == nil {
		t.Error("复制阶段暂时性故障应返回错误")
	}
	if f2.asset(t, args2.AssetID).Status != assets.StatusProcessing {
		t.Error("复制失败不应改变状态")
	}
}

// 过期任务（新尝试已开启）直接跳过，不能把旧尝试的内容写到新尝试上。
func TestVerifySkipsStaleAttempt(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)
	// 用户已开第 2 次尝试并回到 uploading。
	res := f.asset(t, args.AssetID)
	res.UploadAttempt = 2
	res.Status = assets.StatusUploading
	f.store.PutAsset(f.account, res)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatalf("过期任务应静默终结：%v", err)
	}
	after := f.asset(t, args.AssetID)
	if after.Status != assets.StatusUploading || after.UploadAttempt != 2 {
		t.Errorf("过期任务不应改变资产：%s attempt=%d", after.Status, after.UploadAttempt)
	}
	if f.bucket.copyCalls != 0 {
		t.Error("过期任务不应触碰对象存储")
	}
}

// 已删除的资产不再处理。
func TestVerifySkipsDeletedAsset(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)
	res := f.asset(t, args.AssetID)
	deletedAt := f.clock.Now()
	res.DeletedAt = &deletedAt
	f.store.PutAsset(f.account, res)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if f.bucket.copyCalls != 0 || len(f.uow.Changes()) != 0 {
		t.Error("已删除资产不应有任何写入")
	}
}

// 缩略图失败只标记 thumbnail_status=failed，原图保持 ready 可下载。
func TestVerifyThumbnailFailureKeepsOriginalReady(t *testing.T) {
	f := newVerifyFixture(t)
	f.images.thumbErr = errors.New("decode failed")
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatalf("缩略图失败不应让任务失败：%v", err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusReady {
		t.Errorf("原图应保持 ready，得到 %s", res.Status)
	}
	if res.ThumbnailStatus != assets.ThumbnailFailed {
		t.Errorf("缩略图应为 failed，得到 %s", res.ThumbnailStatus)
	}
	if f.store.ThumbnailKey(args.AssetID) != "" {
		t.Error("失败时不应写入缩略图键")
	}

	// 缩略图写入存储失败同理。
	f2 := newVerifyFixture(t)
	f2.bucket.putErr = errors.New("put failed")
	args2 := f2.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)
	if err := f2.verifier.Verify(context.Background(), args2); err != nil {
		t.Fatal(err)
	}
	if res := f2.asset(t, args2.AssetID); res.Status != assets.StatusReady || res.ThumbnailStatus != assets.ThumbnailFailed {
		t.Errorf("Put 失败应 ready/thumbnail failed，得到 %s/%s", res.Status, res.ThumbnailStatus)
	}
}

// 同一任务重跑（River 重试）命中确定性操作编号，不重复写日志。
func TestVerifyIsIdempotentAcrossRetries(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, pdfBytes, nil)

	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	before := len(f.uow.Changes())
	// 第二次：资产已 ready，loadProcessing 判定过期直接返回。
	if err := f.verifier.Verify(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if len(f.uow.Changes()) != before {
		t.Errorf("重跑不应新增变更日志：%d → %d", before, len(f.uow.Changes()))
	}
}

// Fail 供 worker 在重试耗尽时调用：把卡在 processing 的资产释放为 failed。
func TestFailMarksProcessingAssetFailed(t *testing.T) {
	f := newVerifyFixture(t)
	args := f.processingAsset(t, assets.ScopeTrip, jpegBytes, nil)

	if err := f.verifier.Fail(context.Background(), args, assets.FailProcessing); err != nil {
		t.Fatal(err)
	}
	res := f.asset(t, args.AssetID)
	if res.Status != assets.StatusFailed || *res.ErrorCode != assets.FailProcessing {
		t.Errorf("应 failed/PROCESSING_FAILED，得到 %s/%v", res.Status, res.ErrorCode)
	}

	// 对已 ready 的资产调用 Fail 必须是空操作，不能把好文件标坏。
	f2 := newVerifyFixture(t)
	args2 := f2.processingAsset(t, assets.ScopeTrip, pdfBytes, nil)
	if err := f2.verifier.Verify(context.Background(), args2); err != nil {
		t.Fatal(err)
	}
	if err := f2.verifier.Fail(context.Background(), args2, assets.FailProcessing); err != nil {
		t.Fatal(err)
	}
	if f2.asset(t, args2.AssetID).Status != assets.StatusReady {
		t.Error("对 ready 资产调用 Fail 不应改变状态")
	}
}
