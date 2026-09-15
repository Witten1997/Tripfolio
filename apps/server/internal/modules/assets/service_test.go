package assets_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
)

// fakeObjects 记录授权调用，便于断言签名用的键与有效期。
type fakeObjects struct {
	uploadKeys   []string
	downloadKeys []string
	downloadTTLs []time.Duration
	downloadDisp []assets.Disposition
	downloadName []string
	failUpload   error
}

func (f *fakeObjects) KeysFor(accountID, assetID uuid.UUID, attempt int) assets.Keys {
	return assets.Keys{
		Staging:   "staging/" + accountID.String() + "/" + assetID.String() + "/" + itoa(attempt),
		Final:     "assets/" + accountID.String() + "/" + assetID.String() + "/" + itoa(attempt),
		Thumbnail: "thumbnails/" + accountID.String() + "/" + assetID.String() + "/" + itoa(attempt),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

func (f *fakeObjects) AuthorizeUpload(_ context.Context, key, contentType string, contentLength int64, ttl time.Duration) (assets.UploadAuthorization, error) {
	if f.failUpload != nil {
		return assets.UploadAuthorization{}, f.failUpload
	}
	f.uploadKeys = append(f.uploadKeys, key)
	return assets.UploadAuthorization{
		Method: http.MethodPut, URL: "https://store.invalid/" + key,
		RequiredHeaders: map[string]string{"Content-Type": contentType},
		ExpiresAt:       time.Now().Add(ttl),
	}, nil
}

func (f *fakeObjects) AuthorizeDownload(_ context.Context, key, contentType, fileName string, disposition assets.Disposition, ttl time.Duration) (assets.DownloadAuthorization, error) {
	f.downloadKeys = append(f.downloadKeys, key)
	f.downloadTTLs = append(f.downloadTTLs, ttl)
	f.downloadDisp = append(f.downloadDisp, disposition)
	f.downloadName = append(f.downloadName, fileName)
	url := "https://store.invalid/" + key
	expires := time.Now().Add(ttl)
	return assets.DownloadAuthorization{URL: &url, ExpiresAt: &expires}, nil
}

type fixture struct {
	store   *assets.MemoryStore
	uow     *write.MemoryUnitOfWork[assets.Repo]
	svc     *assets.Service
	objects *fakeObjects
	clock   *clock.Fake
	actor   actor.Actor
	other   actor.Actor
	tripID  uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := assets.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[assets.Repo](store)
	clk := clock.NewFake(time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC))
	objects := &fakeObjects{}
	svc := assets.NewService(assets.Deps{
		UnitOfWork: uow, Reader: store, Keys: objects, Objects: objects,
		Cursors: paging.InsecureCodec{}, Clock: clk,
		Limits: assets.UploadLimits{
			ImageMaxBytes: 20 * 1024 * 1024, PDFMaxBytes: 50 * 1024 * 1024,
			ImageMediaTypes: []string{"image/jpeg", "image/png", "image/webp"},
			PDFMediaTypes:   []string{"application/pdf"},
		},
	})
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	f := &fixture{
		store: store, uow: uow, svc: svc, objects: objects, clock: clk, actor: a,
		other:  actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"},
		tripID: uuid.New(),
	}
	store.PutTrip(a.AccountID, f.tripID, assets.TripInfo{})
	return f
}

func (f *fixture) req(string) uuid.UUID { return uuid.New() }

func (f *fixture) createTripAsset(t *testing.T) (uuid.UUID, assets.Resource) {
	t.Helper()
	id := uuid.New()
	result, auth, err := f.svc.Create(context.Background(), f.actor, f.req("createAsset"), assets.CreateInput{
		ID: id, Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "行程单.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("创建资产：%v", err)
	}
	if auth == nil {
		t.Fatal("创建后应直接返回上传授权")
	}
	res, ok := result.Data.(assets.Resource)
	if !ok {
		t.Fatalf("data 应为资产资源，得到 %T", result.Data)
	}
	return id, res
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("期望业务错误，得到 %v", err)
	}
	return e.Code
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("期望业务错误，得到 %v", err)
	}
	return e.Status
}

// 创建即返回授权，且资产处于 uploading、attempt=1、带确认窗口。
func TestCreateReturnsUploadAuthorizationImmediately(t *testing.T) {
	f := newFixture(t)
	id, res := f.createTripAsset(t)

	if res.Status != assets.StatusUploading {
		t.Errorf("状态应为 uploading，得到 %s", res.Status)
	}
	if res.UploadAttempt != 1 {
		t.Errorf("首次尝试序号应为 1，得到 %d", res.UploadAttempt)
	}
	if res.UploadExpiresAt == nil {
		t.Fatal("uploading 必须有确认截止时间")
	}
	want := f.clock.Now().Add(assets.UploadWindow)
	if !res.UploadExpiresAt.Equal(want) {
		t.Errorf("截止时间 %v，期望 %v", res.UploadExpiresAt, want)
	}
	if res.ThumbnailStatus != assets.ThumbnailNone {
		t.Errorf("缩略图状态应为 none，得到 %s", res.ThumbnailStatus)
	}
	// 校验前不应有任何已确认的元数据。
	if res.MediaType != nil || res.ByteSize != nil || res.SHA256 != nil {
		t.Errorf("校验完成前不应有 media_type/byte_size/sha256：%+v", res)
	}
	// 授权必须指向暂存键，不能指向最终键。
	if len(f.objects.uploadKeys) != 1 {
		t.Fatalf("应签发 1 次上传授权，得到 %d", len(f.objects.uploadKeys))
	}
	if got := f.objects.uploadKeys[0]; got != f.objects.KeysFor(f.actor.AccountID, id, 1).Staging {
		t.Errorf("授权应指向暂存键，得到 %s", got)
	}

	// 变更日志带上 trip_id，供同步按旅行过滤。
	changes := f.uow.Changes()
	if len(changes) != 1 || changes[0].EntityType != "asset" {
		t.Fatalf("应记录一条资产变更，得到 %+v", changes)
	}
	if changes[0].TripID == nil || *changes[0].TripID != f.tripID {
		t.Errorf("变更日志应带 trip_id，得到 %+v", changes[0].TripID)
	}
}

// avatar 范围不属于任何旅行，也不写变更日志：sync_changes 的 CHECK 只允许分类的 trip_id 为空。
func TestCreateAvatarHasNoTripIDAndNoChangeLog(t *testing.T) {
	f := newFixture(t)
	result, auth, err := f.svc.Create(context.Background(), f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeAvatar,
		OriginalName: "头像.png", ExpectedSize: 2048, DeclaredMediaType: "image/png",
	})
	if err != nil {
		t.Fatalf("创建头像资产：%v", err)
	}
	if auth == nil {
		t.Fatal("应返回上传授权")
	}
	res := result.Data.(assets.Resource)
	if res.TripID != nil {
		t.Errorf("avatar 资产不应有 trip_id，得到 %v", res.TripID)
	}
	// 主资源仍要登记，收据与 primary 正常；只是不产生同步变更。
	if result.Primary == nil || result.Primary.ID != res.ID {
		t.Errorf("primary 应指向头像资产：%+v", result.Primary)
	}
	if changes := f.uow.Changes(); len(changes) != 0 {
		t.Errorf("avatar 不应写变更日志，得到 %+v", changes)
	}
}

// scope 与 trip_id 必须配对。
func TestCreateValidatesScopeAndTripPairing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// trip 范围缺 trip_id。
	_, _, err := f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip,
		OriginalName: "a.jpg", ExpectedSize: 10, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("trip 范围缺 trip_id 应 422，得到 %s", got)
	}

	// avatar 范围带了 trip_id。
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeAvatar, TripID: &f.tripID,
		OriginalName: "a.jpg", ExpectedSize: 10, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("avatar 范围带 trip_id 应 422，得到 %s", got)
	}
}

// 大小与类型分别对应 413 与 415，不混入 422。
func TestCreateRejectsOversizedAndUnsupportedTypes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 图片超 20 MiB。
	_, _, err := f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "big.jpg", ExpectedSize: 21 * 1024 * 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := statusOf(t, err); got != http.StatusRequestEntityTooLarge {
		t.Errorf("超大图片应 413，得到 %d", got)
	}
	if got := codeOf(t, err); got != "REQUEST_TOO_LARGE" {
		t.Errorf("代码应为 REQUEST_TOO_LARGE，得到 %s", got)
	}

	// PDF 上限更高，20 MiB 的 PDF 应通过。
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "ok.pdf", ExpectedSize: 21 * 1024 * 1024, DeclaredMediaType: "application/pdf",
	})
	if err != nil {
		t.Errorf("21 MiB 的 PDF 应被接受：%v", err)
	}

	// PDF 超 50 MiB。
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "big.pdf", ExpectedSize: 51 * 1024 * 1024, DeclaredMediaType: "application/pdf",
	})
	if got := statusOf(t, err); got != http.StatusRequestEntityTooLarge {
		t.Errorf("超大 PDF 应 413，得到 %d", got)
	}

	// 不在白名单的类型。
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "a.heic", ExpectedSize: 1024, DeclaredMediaType: "image/heic",
	})
	if got := statusOf(t, err); got != http.StatusUnsupportedMediaType {
		t.Errorf("HEIC 应 415，得到 %d", got)
	}

	// 头像只接受图片，PDF 头像被拒。
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeAvatar,
		OriginalName: "a.pdf", ExpectedSize: 1024, DeclaredMediaType: "application/pdf",
	})
	if got := statusOf(t, err); got != http.StatusUnsupportedMediaType {
		t.Errorf("PDF 头像应 415，得到 %d", got)
	}
}

// 类型带参数或大写时应正常识别。
func TestCreateNormalizesDeclaredMediaType(t *testing.T) {
	f := newFixture(t)
	_, _, err := f.svc.Create(context.Background(), f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "a.jpg", ExpectedSize: 1024, DeclaredMediaType: "IMAGE/JPEG; charset=binary",
	})
	if err != nil {
		t.Errorf("带参数的大写类型应被识别：%v", err)
	}
}

// ID 被占用（含墓碑）时返回 409。
func TestCreateRejectsUsedID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, _ := f.createTripAsset(t)

	// 同一 ID 再创建（不同幂等键）应 409。
	_, _, err := f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: id, Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "b.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "ID_ALREADY_USED" {
		t.Errorf("重复 ID 应 ID_ALREADY_USED，得到 %s", got)
	}

	// 墓碑同样占用 ID。
	purged := uuid.New()
	f.store.PutTombstone(purged)
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: purged, Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "c.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "ID_ALREADY_USED" {
		t.Errorf("墓碑 ID 应 ID_ALREADY_USED，得到 %s", got)
	}
}

// 跨账号与跨旅行一律 404；回收站旅行 410。
func TestCreateScopesToOwnerAndRejectsTrashedTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 别人的旅行：404，不泄漏存在性。
	_, _, err := f.svc.Create(ctx, f.other, uuid.New(), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "a.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "RESOURCE_NOT_FOUND" {
		t.Errorf("跨账号应 404，得到 %s", got)
	}

	// 回收站中的旅行不接受新文件。
	trashed := uuid.New()
	deletedAt := f.clock.Now().Add(-time.Hour)
	f.store.PutTrip(f.actor.AccountID, trashed, assets.TripInfo{DeletedAt: &deletedAt})
	_, _, err = f.svc.Create(ctx, f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &trashed,
		OriginalName: "a.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "TRIP_DELETED" {
		t.Errorf("回收站旅行应 TRIP_DELETED，得到 %s", got)
	}
}

// 未过期的 uploading 续签：暂存键与尝试序号都不变。
func TestAuthorizeUploadRenewsWithinWindow(t *testing.T) {
	f := newFixture(t)
	id, created := f.createTripAsset(t)
	keyBefore := f.store.StagingKey(id)

	f.clock.Advance(5 * time.Minute)
	result, auth, err := f.svc.AuthorizeUpload(context.Background(), f.actor, f.req("authorizeAssetUpload"), id)
	if err != nil {
		t.Fatalf("续签：%v", err)
	}
	res := result.Data.(assets.Resource)

	if res.UploadAttempt != created.UploadAttempt {
		t.Errorf("续签不应递增尝试序号：%d → %d", created.UploadAttempt, res.UploadAttempt)
	}
	if got := f.store.StagingKey(id); got != keyBefore {
		t.Errorf("续签不应更换暂存键：%s → %s", keyBefore, got)
	}
	if auth.UploadAttempt != created.UploadAttempt {
		t.Errorf("授权的尝试序号应与资产一致，得到 %d", auth.UploadAttempt)
	}
	// 截止时间应从当前时刻重新起算。
	want := f.clock.Now().Add(assets.UploadWindow)
	if res.UploadExpiresAt == nil || !res.UploadExpiresAt.Equal(want) {
		t.Errorf("续签后截止时间应为 %v，得到 %v", want, res.UploadExpiresAt)
	}
}

// 过期的 uploading 开新尝试：序号递增且换用新暂存键。
func TestAuthorizeUploadStartsNewAttemptAfterExpiry(t *testing.T) {
	f := newFixture(t)
	id, _ := f.createTripAsset(t)
	keyBefore := f.store.StagingKey(id)

	f.clock.Advance(assets.UploadWindow + time.Minute)
	result, auth, err := f.svc.AuthorizeUpload(context.Background(), f.actor, f.req("authorizeAssetUpload"), id)
	if err != nil {
		t.Fatalf("新尝试：%v", err)
	}
	res := result.Data.(assets.Resource)

	if res.UploadAttempt != 2 {
		t.Errorf("过期后应开第 2 次尝试，得到 %d", res.UploadAttempt)
	}
	if got := f.store.StagingKey(id); got == keyBefore {
		t.Errorf("新尝试必须换用新暂存键，仍为 %s", got)
	}
	if auth.UploadAttempt != 2 {
		t.Errorf("授权序号应为 2，得到 %d", auth.UploadAttempt)
	}
}

// failed 资产可以直接开新尝试，并清掉上次的错误码。
func TestAuthorizeUploadRetriesFailedAssetAndClearsErrorCode(t *testing.T) {
	f := newFixture(t)
	id := uuid.New()
	code := "CHECKSUM_MISMATCH"
	now := f.clock.Now()
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: id, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "a.jpg",
		Status: assets.StatusFailed, UploadAttempt: 2, ThumbnailStatus: assets.ThumbnailNone,
		ErrorCode: &code, Version: 4, CreatedAt: now, UpdatedAt: now,
	})
	f.store.PutAttempt(id, assets.AttemptInfo{ExpectedSize: 1024, DeclaredMediaType: "image/jpeg"})

	result, auth, err := f.svc.AuthorizeUpload(context.Background(), f.actor, f.req("authorizeAssetUpload"), id)
	if err != nil {
		t.Fatalf("失败后重试：%v", err)
	}
	// 新授权必须按创建时声明的类型与大小签名，客户端 PUT 时原样发送。
	if auth.RequiredHeaders["Content-Type"] != "image/jpeg" {
		t.Errorf("授权应沿用声明类型，得到 %v", auth.RequiredHeaders)
	}
	res := result.Data.(assets.Resource)
	if res.Status != assets.StatusUploading {
		t.Errorf("重试后应回到 uploading，得到 %s", res.Status)
	}
	if res.UploadAttempt != 3 {
		t.Errorf("尝试序号应为 3，得到 %d", res.UploadAttempt)
	}
	if res.ErrorCode != nil {
		t.Errorf("新尝试应清空上次错误码，仍为 %v", *res.ErrorCode)
	}
}

// processing 与 ready 都不接受新尝试。
func TestAuthorizeUploadRejectedForProcessingAndReady(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := f.clock.Now()

	for _, status := range []assets.Status{assets.StatusProcessing, assets.StatusReady} {
		id := uuid.New()
		f.store.PutAsset(f.actor.AccountID, assets.Resource{
			ID: id, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "a.jpg",
			Status: status, UploadAttempt: 1, ThumbnailStatus: assets.ThumbnailNone,
			Version: 2, CreatedAt: now, UpdatedAt: now,
		})
		_, _, err := f.svc.AuthorizeUpload(ctx, f.actor, f.req("authorizeAssetUpload"), id)
		if got := codeOf(t, err); got != "UPLOAD_NOT_ALLOWED" {
			t.Errorf("%s 状态应拒绝新尝试，得到 %s", status, got)
		}
		if got := statusOf(t, err); got != http.StatusConflict {
			t.Errorf("%s 状态应返回 409，得到 %d", status, got)
		}
	}
}

// 确认转入 processing 并入队校验任务，且两者同事务。
func TestConfirmMarksProcessingAndEnqueuesVerify(t *testing.T) {
	f := newFixture(t)
	id, created := f.createTripAsset(t)

	result, err := f.svc.Confirm(context.Background(), f.actor, f.req("confirmAssetUpload"), id, created.UploadAttempt)
	if err != nil {
		t.Fatalf("确认：%v", err)
	}
	res := result.Data.(assets.Resource)

	if res.Status != assets.StatusProcessing {
		t.Errorf("确认后应为 processing，得到 %s", res.Status)
	}
	// 确认不得直接宣称 ready：这是「不信任客户端」的核心断言。
	if res.Status == assets.StatusReady {
		t.Error("确认接口不能直接把资产置为 ready")
	}
	if res.UploadExpiresAt != nil {
		t.Errorf("processing 不应保留确认窗口，得到 %v", res.UploadExpiresAt)
	}

	jobs := f.uow.Jobs()
	if len(jobs) != 1 || jobs[0].Kind() != "asset_verify" {
		t.Fatalf("应入队一个 asset_verify 任务，得到 %+v", jobs)
	}
	args, ok := jobs[0].(assets.VerifyJobArgs)
	if !ok {
		t.Fatalf("任务参数类型错误：%T", jobs[0])
	}
	if args.AssetID != id || args.UploadAttempt != created.UploadAttempt {
		t.Errorf("任务参数不符：%+v", args)
	}
	if args.AccountID != f.actor.AccountID {
		t.Errorf("任务应带账号：%+v", args)
	}
}

// 序号不是当前尝试时拒绝：迟到的旧授权确认不能污染新尝试。
func TestConfirmRejectsStaleAttempt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, _ := f.createTripAsset(t)

	// 过期后开新尝试，attempt 变为 2。
	f.clock.Advance(assets.UploadWindow + time.Minute)
	if _, _, err := f.svc.AuthorizeUpload(ctx, f.actor, f.req("authorizeAssetUpload"), id); err != nil {
		t.Fatal(err)
	}

	// 用旧序号 1 确认应被拒。
	_, err := f.svc.Confirm(ctx, f.actor, f.req("confirmAssetUpload"), id, 1)
	if got := codeOf(t, err); got != "UPLOAD_NOT_ALLOWED" {
		t.Errorf("旧序号确认应 UPLOAD_NOT_ALLOWED，得到 %s", got)
	}

	// 超前的序号同样被拒。
	_, err = f.svc.Confirm(ctx, f.actor, f.req("confirmAssetUpload"), id, 99)
	if got := codeOf(t, err); got != "UPLOAD_NOT_ALLOWED" {
		t.Errorf("超前序号确认应 UPLOAD_NOT_ALLOWED，得到 %s", got)
	}
}

// 超过确认窗口后确认返回 410 UPLOAD_EXPIRED：暂存对象可能已被清理。
func TestConfirmAfterWindowIsGone(t *testing.T) {
	f := newFixture(t)
	id, created := f.createTripAsset(t)

	f.clock.Advance(assets.UploadWindow + time.Second)
	_, err := f.svc.Confirm(context.Background(), f.actor, f.req("confirmAssetUpload"), id, created.UploadAttempt)
	if got := codeOf(t, err); got != "UPLOAD_EXPIRED" {
		t.Errorf("超窗确认应 UPLOAD_EXPIRED，得到 %s", got)
	}
	if got := statusOf(t, err); got != http.StatusGone {
		t.Errorf("应返回 410，得到 %d", got)
	}
}

// 重复确认被拒：第二次时资产已是 processing。
func TestConfirmTwiceRejectsSecond(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, created := f.createTripAsset(t)

	if _, err := f.svc.Confirm(ctx, f.actor, f.req("confirmAssetUpload"), id, created.UploadAttempt); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Confirm(ctx, f.actor, f.req("confirmAssetUpload"), id, created.UploadAttempt)
	if got := codeOf(t, err); got != "UPLOAD_NOT_ALLOWED" {
		t.Errorf("重复确认应 UPLOAD_NOT_ALLOWED，得到 %s", got)
	}
}

// 同一幂等键重放确认：复用收据，不重复入队任务。
func TestConfirmReplayReusesReceipt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, created := f.createTripAsset(t)
	req := f.req("confirmAssetUpload")

	first, err := f.svc.Confirm(ctx, f.actor, req, id, created.UploadAttempt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.Confirm(ctx, f.actor, req, id, created.UploadAttempt)
	if err != nil {
		t.Fatalf("重放应成功：%v", err)
	}
	if !second.Replayed {
		t.Error("第二次应标记为重放")
	}
	if first.Primary.Version == nil || second.Primary.Version == nil ||
		*first.Primary.Version != *second.Primary.Version {
		t.Error("重放不应产生新版本")
	}
	if jobs := f.uow.Jobs(); len(jobs) != 1 {
		t.Errorf("重放不应重复入队，得到 %d 个任务", len(jobs))
	}
}

// 下载授权只对 ready 签发，且原图与缩略图用不同有效期与对象键。
func TestAuthorizeDownloadOnlyForReadyWithVariantTTLs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := uuid.New()
	now := f.clock.Now()
	mediaType, size := "image/jpeg", int64(2048)
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: id, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "海景.jpg",
		Status: assets.StatusReady, UploadAttempt: 1, MediaType: &mediaType, ByteSize: &size,
		ThumbnailStatus: assets.ThumbnailReady, Version: 3, CreatedAt: now, UpdatedAt: now,
	})

	// 原图：60 秒，指向最终键。
	auth, err := f.svc.AuthorizeDownload(ctx, f.actor, assets.DownloadRequest{
		AssetID: id, Variant: assets.VariantOriginal,
	}, assets.DispositionInline)
	if err != nil {
		t.Fatalf("原图授权：%v", err)
	}
	if auth.URL == nil {
		t.Fatal("ready 资产应返回 URL")
	}
	keys := f.objects.KeysFor(f.actor.AccountID, id, 1)
	if f.objects.downloadKeys[0] != keys.Final {
		t.Errorf("原图应指向最终键，得到 %s", f.objects.downloadKeys[0])
	}
	if f.objects.downloadTTLs[0] != assets.OriginalDownloadTTL {
		t.Errorf("原图有效期应为 %v，得到 %v", assets.OriginalDownloadTTL, f.objects.downloadTTLs[0])
	}

	// 缩略图：15 分钟，指向缩略图键，文件名换成 .jpg。
	_, err = f.svc.AuthorizeDownload(ctx, f.actor, assets.DownloadRequest{
		AssetID: id, Variant: assets.VariantThumbnail,
	}, assets.DispositionAttachment)
	if err != nil {
		t.Fatalf("缩略图授权：%v", err)
	}
	if f.objects.downloadKeys[1] != keys.Thumbnail {
		t.Errorf("缩略图应指向缩略图键，得到 %s", f.objects.downloadKeys[1])
	}
	if f.objects.downloadTTLs[1] != assets.ThumbnailDownloadTTL {
		t.Errorf("缩略图有效期应为 %v，得到 %v", assets.ThumbnailDownloadTTL, f.objects.downloadTTLs[1])
	}
	if f.objects.downloadDisp[1] != assets.DispositionAttachment {
		t.Errorf("disposition 未透传，得到 %s", f.objects.downloadDisp[1])
	}
	if got := f.objects.downloadName[1]; got != "海景_thumb.jpg" {
		t.Errorf("缩略图文件名应为 海景_thumb.jpg，得到 %s", got)
	}
}

// 非 ready 资产的单个下载授权返回 409；缩略图未就绪也是 409。
func TestAuthorizeDownloadRejectsNotReady(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, _ := f.createTripAsset(t)

	_, err := f.svc.AuthorizeDownload(ctx, f.actor, assets.DownloadRequest{
		AssetID: id, Variant: assets.VariantOriginal,
	}, assets.DispositionInline)
	if got := codeOf(t, err); got != "ASSET_NOT_READY" {
		t.Errorf("uploading 资产应 ASSET_NOT_READY，得到 %s", got)
	}

	// ready 但缩略图失败：原图可下，缩略图不可。
	readyID := uuid.New()
	now := f.clock.Now()
	mediaType := "image/jpeg"
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: readyID, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "a.jpg",
		Status: assets.StatusReady, UploadAttempt: 1, MediaType: &mediaType,
		ThumbnailStatus: assets.ThumbnailFailed, Version: 3, CreatedAt: now, UpdatedAt: now,
	})
	if _, err := f.svc.AuthorizeDownload(ctx, f.actor, assets.DownloadRequest{
		AssetID: readyID, Variant: assets.VariantOriginal,
	}, assets.DispositionInline); err != nil {
		t.Errorf("缩略图失败不应影响原图下载：%v", err)
	}
	_, err = f.svc.AuthorizeDownload(ctx, f.actor, assets.DownloadRequest{
		AssetID: readyID, Variant: assets.VariantThumbnail,
	}, assets.DispositionInline)
	if got := codeOf(t, err); got != "ASSET_NOT_READY" {
		t.Errorf("缩略图失败时应 ASSET_NOT_READY，得到 %s", got)
	}
}

// 批量授权逐项返回状态，只有 ready 的带 URL。
func TestAuthorizeDownloadsReturnsPerItemStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := f.clock.Now()
	mediaType := "image/jpeg"

	readyID, uploadingID := uuid.New(), uuid.New()
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: readyID, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "ready.jpg",
		Status: assets.StatusReady, UploadAttempt: 1, MediaType: &mediaType,
		ThumbnailStatus: assets.ThumbnailReady, Version: 3, CreatedAt: now, UpdatedAt: now,
	})
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: uploadingID, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "wip.jpg",
		Status: assets.StatusUploading, UploadAttempt: 1,
		ThumbnailStatus: assets.ThumbnailNone, Version: 1, CreatedAt: now, UpdatedAt: now,
	})

	items, err := f.svc.AuthorizeDownloads(ctx, f.actor, []assets.DownloadRequest{
		{AssetID: readyID, Variant: assets.VariantOriginal},
		{AssetID: uploadingID, Variant: assets.VariantOriginal},
	}, assets.DispositionInline)
	if err != nil {
		t.Fatalf("批量授权：%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应返回 2 项，得到 %d", len(items))
	}
	// 顺序必须与请求一致。
	if items[0].AssetID != readyID || items[1].AssetID != uploadingID {
		t.Errorf("返回顺序应与请求一致：%+v", items)
	}
	if items[0].URL == nil {
		t.Error("ready 项应带 URL")
	}
	if items[1].URL != nil {
		t.Error("uploading 项不应带 URL")
	}
	// 非 ready 项仍要给出状态，供客户端展示「上传中」。
	if items[1].Status != assets.StatusUploading {
		t.Errorf("应返回 uploading 状态，得到 %s", items[1].Status)
	}
}

// 批量授权含他人资产时整批 404，不逐项泄漏。
func TestAuthorizeDownloadsRejectsWholeBatchOnForeignAsset(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := f.clock.Now()
	mine := uuid.New()
	f.store.PutAsset(f.actor.AccountID, assets.Resource{
		ID: mine, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "a.jpg",
		Status: assets.StatusReady, UploadAttempt: 1, ThumbnailStatus: assets.ThumbnailNone,
		Version: 3, CreatedAt: now, UpdatedAt: now,
	})
	foreign := uuid.New()
	f.store.PutAsset(f.other.AccountID, assets.Resource{
		ID: foreign, Scope: assets.ScopeAvatar, OriginalName: "b.jpg",
		Status: assets.StatusReady, UploadAttempt: 1, ThumbnailStatus: assets.ThumbnailNone,
		Version: 3, CreatedAt: now, UpdatedAt: now,
	})

	_, err := f.svc.AuthorizeDownloads(ctx, f.actor, []assets.DownloadRequest{
		{AssetID: mine, Variant: assets.VariantOriginal},
		{AssetID: foreign, Variant: assets.VariantOriginal},
	}, assets.DispositionInline)
	if got := codeOf(t, err); got != "RESOURCE_NOT_FOUND" {
		t.Errorf("含他人资产应整批 404，得到 %s", got)
	}
}

// 批量上限 100。
func TestAuthorizeDownloadsEnforcesBatchLimit(t *testing.T) {
	f := newFixture(t)
	items := make([]assets.DownloadRequest, assets.MaxDownloadBatch+1)
	for i := range items {
		items[i] = assets.DownloadRequest{AssetID: uuid.New(), Variant: assets.VariantOriginal}
	}
	_, err := f.svc.AuthorizeDownloads(context.Background(), f.actor, items, assets.DispositionInline)
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("超过 100 个应 422，得到 %s", got)
	}

	_, err = f.svc.AuthorizeDownloads(context.Background(), f.actor, nil, assets.DispositionInline)
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("空列表应 422，得到 %s", got)
	}
}

// 列表按状态与 ID 筛选，游标绑定筛选条件。
func TestListFiltersAndPaginates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	base := f.clock.Now()

	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		id := uuid.New()
		ids = append(ids, id)
		status := assets.StatusReady
		if i == 2 {
			status = assets.StatusFailed
		}
		f.store.PutAsset(f.actor.AccountID, assets.Resource{
			ID: id, TripID: &f.tripID, Scope: assets.ScopeTrip, OriginalName: "a.jpg",
			Status: status, UploadAttempt: 1, ThumbnailStatus: assets.ThumbnailNone,
			Version: 1, CreatedAt: base.Add(time.Duration(i) * time.Minute), UpdatedAt: base,
		})
	}

	// 状态筛选。
	page, err := f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{Status: "ready"})
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("ready 应有 2 条，得到 %d", len(page.Items))
	}

	// 分页：limit=1 时给出游标，第二页不重复。
	page, err = f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("应返回 1 条并给出游标：%+v", page)
	}
	firstID := page.Items[0].ID
	page2, err := f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{Limit: 1, Cursor: *page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ID == firstID {
		t.Errorf("第二页不应重复第一页：%+v", page2.Items)
	}

	// 换了筛选条件后旧游标失效：避免跨条件翻页得到错乱结果。
	_, err = f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{Limit: 1, Status: "ready", Cursor: *page.NextCursor})
	if err == nil {
		t.Error("换筛选条件后旧游标应失效")
	}

	// 非法状态与非法 ID 列表。
	_, err = f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{Status: "bogus"})
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("非法状态应 422，得到 %s", got)
	}
	_, err = f.svc.List(ctx, f.actor, f.tripID, assets.ListFilters{IDs: "not-a-uuid"})
	if got := codeOf(t, err); got != "VALIDATION_FAILED" {
		t.Errorf("非法 ids 应 422，得到 %s", got)
	}
}

// 未配置对象存储时，涉及授权的用例返回 503 而不是崩溃。
func TestUnconfiguredStorageReturnsDependencyUnavailable(t *testing.T) {
	store := assets.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[assets.Repo](store)
	svc := assets.NewService(assets.Deps{
		UnitOfWork: uow, Reader: store, Cursors: paging.InsecureCodec{},
		Clock: clock.NewFake(time.Now()),
	})
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb}
	ctx := context.Background()

	_, _, err := svc.Create(ctx, a, uuid.New(), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeAvatar, OriginalName: "a.jpg",
		ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("未配置存储应 503，得到 %s", got)
	}

	_, _, err = svc.AuthorizeUpload(ctx, a, uuid.New(), uuid.New())
	if got := codeOf(t, err); got != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("未配置存储应 503，得到 %s", got)
	}
}

// 对象存储签发授权失败时映射为 503，不是 500。
func TestUploadAuthorizationFailureMapsToDependency(t *testing.T) {
	f := newFixture(t)
	f.objects.failUpload = errors.New("存储不可达")

	_, _, err := f.svc.Create(context.Background(), f.actor, f.req("createAsset"), assets.CreateInput{
		ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID,
		OriginalName: "a.jpg", ExpectedSize: 1024, DeclaredMediaType: "image/jpeg",
	})
	if got := codeOf(t, err); got != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("存储故障应 503，得到 %s", got)
	}
}

// Get 的归属与删除语义：跨账号 404，已删除 410。
func TestGetScopesToOwnerAndReportsDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, _ := f.createTripAsset(t)

	if _, err := f.svc.Get(ctx, f.other, id); codeOf(t, err) != "RESOURCE_NOT_FOUND" {
		t.Error("跨账号读取应 404")
	}

	deletedAt := f.clock.Now()
	res, _, _ := f.store.Get(ctx, f.actor.AccountID, id)
	res.DeletedAt = &deletedAt
	f.store.PutAsset(f.actor.AccountID, res)

	_, err := f.svc.Get(ctx, f.actor, id)
	if got := codeOf(t, err); got != "RESOURCE_GONE" {
		t.Errorf("已删除应 410 RESOURCE_GONE，得到 %s", got)
	}
}
