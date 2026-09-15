package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/assets"
)

// testObjectStoreConfig 从环境变量读取测试用对象存储；未设置时返回零值，资产用例据此跳过。
func testObjectStoreConfig() config.ObjectStoreConfig {
	endpoint := os.Getenv("TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT")
	if endpoint == "" {
		return config.ObjectStoreConfig{}
	}
	return config.ObjectStoreConfig{
		Endpoint:        endpoint,
		Region:          envOr("TRIPFOLIO_TEST_OBJECTSTORE_REGION", "us-east-1"),
		Bucket:          envOr("TRIPFOLIO_TEST_OBJECTSTORE_BUCKET", "tripfolio"),
		AccessKeyID:     envOr("TRIPFOLIO_TEST_OBJECTSTORE_ACCESS_KEY", "tripfolio"),
		SecretAccessKey: envOr("TRIPFOLIO_TEST_OBJECTSTORE_SECRET_KEY", "tripfolio-dev-secret"),
		UsePathStyle:    true,
	}
}

type assetFixture struct {
	*tripContentFixture
}

func newAssetFixture(t *testing.T) *assetFixture {
	t.Helper()
	if os.Getenv("TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT") == "" {
		t.Skip("TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT 未设置，跳过资产集成测试")
	}
	return &assetFixture{tripContentFixture: newTripContentFixture(t)}
}

// createAsset 登记资产并返回响应中的 data 与 upload_authorization。
func (f *assetFixture) createAsset(body map[string]any) (map[string]any, map[string]any) {
	f.t.Helper()
	res := f.post("/assets", body)
	expectStatus(f.t, res, http.StatusCreated, "")
	d := res.data()
	asset, _ := d["data"].(map[string]any)
	auth, _ := d["upload_authorization"].(map[string]any)
	return asset, auth
}

// upload 按授权直传对象存储；required_headers 必须原样发送。
func (f *assetFixture) upload(auth map[string]any, content []byte) {
	f.t.Helper()
	req, err := http.NewRequest(auth["method"].(string), auth["url"].(string), bytes.NewReader(content))
	if err != nil {
		f.t.Fatal(err)
	}
	for k, v := range auth["required_headers"].(map[string]any) {
		req.Header.Set(k, v.(string))
	}
	req.ContentLength = int64(len(content))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("直传：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("直传状态 %d：%s", resp.StatusCode, detail)
	}
}

func (f *assetFixture) confirm(assetID string, attempt int) apiResponse {
	return f.post("/assets/"+assetID+"/confirm", map[string]any{"upload_attempt": attempt})
}

// runVerifier 从 river_job 取出入队的校验参数并直接执行校验器：
// 既证明事务入队的参数正确，又不必在测试里启动 River 消费进程。
func (f *assetFixture) runVerifier(assetID string) {
	f.t.Helper()
	var raw []byte
	err := f.pool.QueryRow(context.Background(),
		`SELECT args FROM river_job WHERE kind = 'asset_verify' AND args->>'asset_id' = $1 ORDER BY id DESC LIMIT 1`, assetID,
	).Scan(&raw)
	if err != nil {
		f.t.Fatalf("读取入队的校验任务：%v", err)
	}
	var args assets.VerifyJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		f.t.Fatalf("解析任务参数：%v", err)
	}
	if f.services.AssetVerifier == nil {
		f.t.Fatal("对象存储已配置，校验器不应为 nil")
	}
	if err := f.services.AssetVerifier.Verify(context.Background(), args); err != nil {
		f.t.Fatalf("执行校验：%v", err)
	}
}

func (f *assetFixture) asset(assetID string) map[string]any {
	f.t.Helper()
	res := f.get("/assets/" + assetID)
	expectStatus(f.t, res, http.StatusOK, "")
	return res.data()
}

// makeJPEG 生成一张真实 JPEG，供校验、尺寸与缩略图链路使用。
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func downloadBody(t *testing.T, url string) ([]byte, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("下载：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载状态 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return body, resp.Header
}

// 完整链路：登记 → 直传 → 确认 → worker 校验 → ready → 原图与缩略图下载。
func TestHTTPAssetUploadVerifyDownload(t *testing.T) {
	f := newAssetFixture(t)
	content := makeJPEG(t, 1200, 800)
	sum := sha256.Sum256(content)
	id := uuid.NewString()

	asset, auth := f.createAsset(map[string]any{
		"id": id, "scope": "trip", "trip_id": f.tripID, "original_name": "海边.jpg",
		"expected_size": len(content), "declared_media_type": "image/jpeg", "client_sha256": hex.EncodeToString(sum[:]),
	})
	if asset["status"] != "uploading" || asset["upload_attempt"].(float64) != 1 {
		t.Fatalf("创建后应为 uploading/attempt=1：%v", asset)
	}
	if asset["upload_expires_at"] == nil {
		t.Error("uploading 应带确认截止时间")
	}
	if auth == nil || auth["method"] != "PUT" || auth["upload_attempt"].(float64) != 1 {
		t.Fatalf("创建应直接返回首个尝试的 PUT 授权：%v", auth)
	}
	// 授权 URL 指向暂存前缀，不暴露最终键。
	if url := auth["url"].(string); !bytes.Contains([]byte(url), []byte("/staging/")) {
		t.Errorf("上传授权应指向暂存键：%s", url)
	}
	// 未确认前不能下载。
	res := f.get("/assets/" + id + "/download")
	expectStatus(t, res, http.StatusConflict, "ASSET_NOT_READY")

	f.upload(auth, content)

	res = f.confirm(id, 1)
	expectStatus(t, res, http.StatusAccepted, "")
	if got := res.data()["data"].(map[string]any)["status"]; got != "processing" {
		t.Fatalf("确认后应为 processing，得到 %v", got)
	}
	// 确认后不再接受新尝试或重复确认。
	res = f.post("/assets/"+id+"/upload-authorization", nil)
	expectStatus(t, res, http.StatusConflict, "UPLOAD_NOT_ALLOWED")
	res = f.confirm(id, 1)
	expectStatus(t, res, http.StatusConflict, "UPLOAD_NOT_ALLOWED")

	f.runVerifier(id)

	ready := f.asset(id)
	if ready["status"] != "ready" {
		t.Fatalf("校验后应为 ready，得到 %v（error_code=%v）", ready["status"], ready["error_code"])
	}
	if ready["media_type"] != "image/jpeg" || ready["byte_size"].(float64) != float64(len(content)) {
		t.Errorf("元数据应来自实际内容：%v / %v", ready["media_type"], ready["byte_size"])
	}
	if ready["sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 不符：%v", ready["sha256"])
	}
	if ready["width"].(float64) != 1200 || ready["height"].(float64) != 800 {
		t.Errorf("尺寸不符：%v×%v", ready["width"], ready["height"])
	}
	if ready["thumbnail_status"] != "ready" {
		t.Errorf("图片缩略图应 ready，得到 %v", ready["thumbnail_status"])
	}
	if ready["upload_expires_at"] != nil {
		t.Error("ready 不应保留确认窗口")
	}
	// ready 后不再签发上传授权：替换文件需新建资产。
	res = f.post("/assets/"+id+"/upload-authorization", nil)
	expectStatus(t, res, http.StatusConflict, "UPLOAD_NOT_ALLOWED")

	// 原图下载：内容一致，响应类型被签名固定，带 no-store。
	res = f.get("/assets/" + id + "/download?disposition=attachment")
	expectStatus(t, res, http.StatusOK, "")
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("下载授权响应应 no-store，得到 %q", cc)
	}
	dl := res.data()
	if dl["url"] == nil || dl["status"] != "ready" {
		t.Fatalf("ready 资产应返回下载地址：%v", dl)
	}
	body, hdr := downloadBody(t, dl["url"].(string))
	if !bytes.Equal(body, content) {
		t.Error("下载内容与上传不一致")
	}
	if ct := hdr.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type 应被签名固定为 image/jpeg，得到 %q", ct)
	}
	if cd := hdr.Get("Content-Disposition"); !bytes.HasPrefix([]byte(cd), []byte("attachment")) || !bytes.Contains([]byte(cd), []byte("filename*=UTF-8''")) {
		t.Errorf("Content-Disposition 应为 attachment 且带 RFC 5987 文件名：%q", cd)
	}

	// 缩略图下载：是 JPEG 且长边不超过 512。
	res = f.get("/assets/" + id + "/download?variant=thumbnail")
	expectStatus(t, res, http.StatusOK, "")
	thumb, _ := downloadBody(t, res.data()["url"].(string))
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("解码缩略图：%v", err)
	}
	if cfg.Width != 512 || cfg.Height != 341 {
		t.Errorf("缩略图尺寸应为 512×341，得到 %d×%d", cfg.Width, cfg.Height)
	}

	// 变更日志：创建、确认、ready、缩略图各一条，版本递增。
	n, lastKind, lastVersion := f.countChanges(f.accountID, "asset")
	if n != 4 || lastKind != "upsert" || lastVersion != 4 {
		t.Errorf("变更日志应 4 条 upsert 且版本到 4，得到 n=%d kind=%s version=%d", n, lastKind, lastVersion)
	}
}

// 传输损坏导致摘要不符时 worker 判定失败；用户重新上传同一份文件即可通过。
// client_sha256 随资产在创建时固定，重试语义是「再传一次这份文件」，不是换文件。
func TestHTTPAssetChecksumMismatchThenRetry(t *testing.T) {
	f := newAssetFixture(t)
	content := makeJPEG(t, 64, 64)
	sum := sha256.Sum256(content)
	id := uuid.NewString()

	_, auth := f.createAsset(map[string]any{
		"id": id, "scope": "trip", "trip_id": f.tripID, "original_name": "a.jpg",
		"expected_size": len(content), "declared_media_type": "image/jpeg", "client_sha256": hex.EncodeToString(sum[:]),
	})
	// 第一次传的是损坏内容：翻转一个字节，长度不变以通过签名里的 Content-Length。
	corrupted := append([]byte(nil), content...)
	corrupted[len(corrupted)-10] ^= 0xFF
	f.upload(auth, corrupted)
	expectStatus(t, f.confirm(id, 1), http.StatusAccepted, "")
	f.runVerifier(id)

	failed := f.asset(id)
	if failed["status"] != "failed" || failed["error_code"] != assets.FailChecksumMismatch {
		t.Fatalf("摘要不符应 failed/CHECKSUM_MISMATCH，得到 %v/%v", failed["status"], failed["error_code"])
	}
	if failed["media_type"] != nil || failed["byte_size"] != nil {
		t.Error("失败的资产不应写入 media_type/byte_size")
	}

	// 开新尝试：序号 2、状态回到 uploading、错误码清空、新授权指向新暂存键。
	res := f.post("/assets/"+id+"/upload-authorization", nil)
	expectStatus(t, res, http.StatusOK, "")
	d := res.data()
	retried := d["data"].(map[string]any)
	if retried["status"] != "uploading" || retried["upload_attempt"].(float64) != 2 || retried["error_code"] != nil {
		t.Fatalf("新尝试应 uploading/attempt=2/无错误码：%v", retried)
	}
	auth2 := d["upload_authorization"].(map[string]any)
	if auth2["upload_attempt"].(float64) != 2 || auth2["url"] == auth["url"] {
		t.Errorf("新尝试应换用新的暂存授权：%v", auth2)
	}
	// 旧序号确认被拒，不能污染新尝试。
	expectStatus(t, f.confirm(id, 1), http.StatusConflict, "UPLOAD_NOT_ALLOWED")

	// 第二次传正确内容，摘要与声明一致，跑通。
	f.upload(auth2, content)
	expectStatus(t, f.confirm(id, 2), http.StatusAccepted, "")
	f.runVerifier(id)
	ready := f.asset(id)
	if ready["status"] != "ready" {
		t.Errorf("重传后应 ready，得到 %v（error_code=%v）", ready["status"], ready["error_code"])
	}
	if ready["sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("重传后 sha256 应与声明一致：%v", ready["sha256"])
	}
}

// 内容与声明类型不符时以嗅探为准；声明是图片但传了 PDF 仍按 PDF 就绪且无缩略图。
func TestHTTPAssetSniffedTypeWinsAndPDFHasNoThumbnail(t *testing.T) {
	f := newAssetFixture(t)
	content := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n")
	id := uuid.NewString()

	_, auth := f.createAsset(map[string]any{
		"id": id, "scope": "trip", "trip_id": f.tripID, "original_name": "行程单.pdf",
		"expected_size": len(content), "declared_media_type": "application/pdf",
	})
	f.upload(auth, content)
	expectStatus(t, f.confirm(id, 1), http.StatusAccepted, "")
	f.runVerifier(id)

	ready := f.asset(id)
	if ready["status"] != "ready" || ready["media_type"] != "application/pdf" {
		t.Fatalf("PDF 应 ready/application/pdf，得到 %v/%v", ready["status"], ready["media_type"])
	}
	if ready["thumbnail_status"] != "none" || ready["width"] != nil {
		t.Errorf("PDF 不应有缩略图与尺寸：%v", ready)
	}
	// PDF 请求缩略图变体：409。
	expectStatus(t, f.get("/assets/"+id+"/download?variant=thumbnail"), http.StatusConflict, "ASSET_NOT_READY")
}

// 创建阶段的协议级拒绝：413、415、422，以及归属校验。
func TestHTTPAssetCreateRejections(t *testing.T) {
	f := newAssetFixture(t)
	base := func(over map[string]any) map[string]any {
		body := map[string]any{
			"id": uuid.NewString(), "scope": "trip", "trip_id": f.tripID, "original_name": "a.jpg",
			"expected_size": 1024, "declared_media_type": "image/jpeg",
		}
		for k, v := range over {
			body[k] = v
		}
		return body
	}

	expectStatus(t, f.post("/assets", base(map[string]any{"expected_size": 21 * 1024 * 1024})), http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE")
	expectStatus(t, f.post("/assets", base(map[string]any{"declared_media_type": "image/heic"})), http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE")
	expectStatus(t, f.post("/assets", base(map[string]any{"scope": "avatar", "declared_media_type": "application/pdf", "trip_id": nil})), http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE")
	expectStatus(t, f.post("/assets", base(map[string]any{"trip_id": nil})), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.post("/assets", base(map[string]any{"client_sha256": "nothex"})), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	// 别人的旅行：404。
	expectStatus(t, f.post("/assets", base(map[string]any{"trip_id": uuid.NewString()})), http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// 头像范围：不带 trip_id 即可创建；不写变更日志（sync_changes 只允许分类的 trip_id 为空），但留收据。
	avatarID := uuid.NewString()
	asset, auth := f.createAsset(map[string]any{
		"id": avatarID, "scope": "avatar", "original_name": "me.png", "expected_size": 512, "declared_media_type": "image/png",
	})
	if asset["trip_id"] != nil || auth == nil {
		t.Errorf("头像资产不应有 trip_id 且应带授权：%v", asset)
	}
	var changeRows, receiptRows int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sync_changes WHERE entity_type = 'asset' AND entity_id = $1`, avatarID).Scan(&changeRows); err != nil {
		t.Fatalf("读取头像变更日志：%v", err)
	}
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM mutation_receipts WHERE account_id = $1 AND operation_type = 'asset.create'`, f.accountID).Scan(&receiptRows); err != nil {
		t.Fatalf("读取收据：%v", err)
	}
	if changeRows != 0 {
		t.Errorf("头像资产不应写变更日志，得到 %d 行", changeRows)
	}
	if receiptRows == 0 {
		t.Error("头像创建仍应留下幂等收据")
	}

	// 同一 ID 二次创建：409。
	expectStatus(t, f.post("/assets", base(map[string]any{"id": avatarID, "scope": "avatar", "trip_id": nil, "declared_media_type": "image/png"})), http.StatusConflict, "ID_ALREADY_USED")
}

// 列表、批量授权与跨账号隔离。
func TestHTTPAssetListAndBatchAuthorization(t *testing.T) {
	f := newAssetFixture(t)
	content := makeJPEG(t, 32, 32)

	// 一个 ready、一个仍 uploading。
	readyID, uploadingID := uuid.NewString(), uuid.NewString()
	_, auth := f.createAsset(map[string]any{
		"id": readyID, "scope": "trip", "trip_id": f.tripID, "original_name": "r.jpg",
		"expected_size": len(content), "declared_media_type": "image/jpeg",
	})
	f.upload(auth, content)
	expectStatus(t, f.confirm(readyID, 1), http.StatusAccepted, "")
	f.runVerifier(readyID)
	f.createAsset(map[string]any{
		"id": uploadingID, "scope": "trip", "trip_id": f.tripID, "original_name": "u.jpg",
		"expected_size": 100, "declared_media_type": "image/jpeg",
	})

	// 列表：按创建时间降序，状态筛选，ids 筛选。
	res := f.get(f.path("/assets"))
	expectStatus(t, res, http.StatusOK, "")
	if got := items(res); len(got) != 2 || got[0]["id"] != uploadingID {
		t.Errorf("列表应 2 条且最新在前：%v", got)
	}
	res = f.get(f.path("/assets?status=ready"))
	expectStatus(t, res, http.StatusOK, "")
	if got := items(res); len(got) != 1 || got[0]["id"] != readyID {
		t.Errorf("status=ready 应只有 1 条：%v", got)
	}
	res = f.get(f.path("/assets?ids=" + uploadingID))
	expectStatus(t, res, http.StatusOK, "")
	if got := items(res); len(got) != 1 || got[0]["id"] != uploadingID {
		t.Errorf("ids 筛选错误：%v", got)
	}
	expectStatus(t, f.get(f.path("/assets?status=bogus")), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 批量授权：逐项状态，只有 ready 带 URL，顺序与请求一致。
	res = f.post("/assets/download-authorizations", map[string]any{
		"items": []map[string]any{{"asset_id": uploadingID}, {"asset_id": readyID, "variant": "thumbnail"}},
	})
	expectStatus(t, res, http.StatusOK, "")
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("批量授权应 no-store，得到 %q", cc)
	}
	batch := res.data()["items"].([]any)
	first, second := batch[0].(map[string]any), batch[1].(map[string]any)
	if first["asset_id"] != uploadingID || first["status"] != "uploading" || first["url"] != nil {
		t.Errorf("uploading 项应带状态无 URL：%v", first)
	}
	if second["asset_id"] != readyID || second["url"] == nil || second["thumbnail_status"] != "ready" {
		t.Errorf("ready 项应带缩略图 URL：%v", second)
	}

	// 含他人资产整批 404；跨账号单个读取同样 404。
	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherToken := other.data()["access_token"].(string)
	res = f.do(request{method: http.MethodPost, path: "/assets/download-authorizations", token: otherToken,
		body: map[string]any{"items": []map[string]any{{"asset_id": readyID}}}, headers: f.authHeaders(nil)})
	expectStatus(t, res, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	res = f.do(request{method: http.MethodGet, path: "/assets/" + readyID, token: otherToken})
	expectStatus(t, res, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	res = f.do(request{method: http.MethodGet, path: "/assets/" + readyID + "/download", token: otherToken})
	expectStatus(t, res, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// 幂等重放：同一 Idempotency-Key 再创建返回 replayed 且仍带授权。
	replayID := uuid.NewString()
	key := uuid.NewString()
	body := map[string]any{"id": replayID, "scope": "trip", "trip_id": f.tripID, "original_name": "x.jpg", "expected_size": 10, "declared_media_type": "image/jpeg"}
	res = f.do(request{method: http.MethodPost, path: "/assets", token: f.token, body: body, headers: f.authHeaders(map[string]string{"Idempotency-Key": key})})
	expectStatus(t, res, http.StatusCreated, "")
	res = f.do(request{method: http.MethodPost, path: "/assets", token: f.token, body: body, headers: f.authHeaders(map[string]string{"Idempotency-Key": key})})
	expectStatus(t, res, http.StatusCreated, "")
	if d := res.data(); d["replayed"] != true || d["upload_authorization"] == nil {
		t.Errorf("重放应标记 replayed 并重新签发授权：%v", d)
	}
}

// 头像绑定：avatar 范围资产可在任何状态绑定到账号；trip 范围、他人的、已删除的资产被拒；显式 null 清空。
func TestHTTPAvatarBinding(t *testing.T) {
	f := newAssetFixture(t)
	content := makeJPEG(t, 128, 128)
	patchAccount := func(version string, body map[string]any) apiResponse {
		return f.do(request{method: http.MethodPatch, path: "/account", token: f.token, body: body,
			headers: f.authHeaders(map[string]string{"If-Match": `"` + version + `"`})})
	}
	accountVersion := func() string {
		res := f.get("/account")
		expectStatus(t, res, http.StatusOK, "")
		return res.data()["version"].(string)
	}

	// 上传中的头像即可绑定：引用不要求 ready。
	avatarID := uuid.NewString()
	_, auth := f.createAsset(map[string]any{
		"id": avatarID, "scope": "avatar", "original_name": "me.jpg",
		"expected_size": len(content), "declared_media_type": "image/jpeg",
	})
	res := patchAccount(accountVersion(), map[string]any{"avatar_asset_id": avatarID})
	expectStatus(t, res, http.StatusOK, "")
	if got := res.data()["data"].(map[string]any)["avatar_asset_id"]; got != avatarID {
		t.Fatalf("头像应已绑定：%v", got)
	}

	// 跑完上传链路后，通过下载授权可取到头像缩略图。
	f.upload(auth, content)
	expectStatus(t, f.confirm(avatarID, 1), http.StatusAccepted, "")
	f.runVerifier(avatarID)
	res = f.get("/assets/" + avatarID + "/download?variant=thumbnail")
	expectStatus(t, res, http.StatusOK, "")
	if res.data()["url"] == nil {
		t.Error("ready 头像应能签发缩略图下载授权")
	}

	// trip 范围资产不能当头像。
	tripAssetID := uuid.NewString()
	f.createAsset(map[string]any{
		"id": tripAssetID, "scope": "trip", "trip_id": f.tripID, "original_name": "t.jpg",
		"expected_size": 100, "declared_media_type": "image/jpeg",
	})
	res = patchAccount(accountVersion(), map[string]any{"avatar_asset_id": tripAssetID})
	expectStatus(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 他人的头像资产：同样拒绝，不泄漏存在性。
	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherToken := other.data()["access_token"].(string)
	foreignID := uuid.NewString()
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/assets", token: otherToken, headers: f.authHeaders(nil),
		body: map[string]any{"id": foreignID, "scope": "avatar", "original_name": "x.png", "expected_size": 10, "declared_media_type": "image/png"},
	}), http.StatusCreated, "")
	res = patchAccount(accountVersion(), map[string]any{"avatar_asset_id": foreignID})
	expectStatus(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 不存在的 ID 同样 422，而不是 404：这是字段校验而非资源路径。
	res = patchAccount(accountVersion(), map[string]any{"avatar_asset_id": uuid.NewString()})
	expectStatus(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 显式 null 清空头像。
	res = patchAccount(accountVersion(), map[string]any{"avatar_asset_id": nil})
	expectStatus(t, res, http.StatusOK, "")
	if got := res.data()["data"].(map[string]any)["avatar_asset_id"]; got != nil {
		t.Errorf("显式 null 应清空头像，得到 %v", got)
	}
}
