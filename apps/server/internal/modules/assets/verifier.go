package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
)

// 可公开的失败代码，写入 Asset.error_code 供客户端提示用户；它们不是 HTTP 问题代码。
const (
	// FailUploadMissing 表示确认时暂存对象不存在：客户端没有真正上传，或对象已被清理。
	FailUploadMissing = "UPLOAD_MISSING"
	// FailUploadModified 表示校验后、复制前暂存对象被改写，ETag 条件复制失败。
	FailUploadModified = "UPLOAD_MODIFIED"
	// FailChecksumMismatch 表示内容摘要与客户端声明不一致。
	FailChecksumMismatch = "CHECKSUM_MISMATCH"
	// FailUnsupportedType 表示按内容嗅探不是允许的类型（声明类型不作数）。
	FailUnsupportedType = "UNSUPPORTED_MEDIA_TYPE"
	// FailTooLarge 表示实际字节数超过该类型上限。
	FailTooLarge = "FILE_TOO_LARGE"
	// FailImageTooLarge 表示图片像素数超过解码上限。
	FailImageTooLarge = "IMAGE_TOO_LARGE"
	// FailCorruptImage 表示魔数是图片但无法解码。
	FailCorruptImage = "CORRUPT_IMAGE"
	// FailProcessing 表示重试耗尽仍未完成，由 worker 在最后一次尝试写入。
	FailProcessing = "PROCESSING_FAILED"
)

// Verifier 是 worker 侧的校验用例：读取暂存对象，校验大小、类型与摘要，
// 以校验时 ETag 为条件复制到最终键，再写回 ready 或 failed；图片另生成缩略图。
//
// 对象存储与图片处理都在数据库事务之外进行；事务内只复核状态并落库，
// 避免账号写锁被长 I/O 占用（层级结构设计 7.x）。
type Verifier struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	keys    ObjectKeys
	objects ObjectProcessor
	images  ImageProcessor
	limits  UploadLimits
	clock   clock.Clock
	logger  *slog.Logger
}

// VerifierDeps 是校验器依赖。
type VerifierDeps struct {
	UnitOfWork write.UnitOfWork[Repo]
	Reader     Reader
	Keys       ObjectKeys
	Objects    ObjectProcessor
	Images     ImageProcessor
	Limits     UploadLimits
	Clock      clock.Clock
	Logger     *slog.Logger
}

// NewVerifier 创建校验器。
func NewVerifier(d VerifierDeps) *Verifier {
	return &Verifier{
		uow: d.UnitOfWork, reader: d.Reader, keys: d.Keys, objects: d.Objects,
		images: d.Images, limits: d.Limits, clock: d.Clock, logger: d.Logger,
	}
}

// ErrStaleJob 表示任务对应的尝试已不是当前尝试，或资产已不在 processing；
// worker 据此结束任务而不重试。
var ErrStaleJob = errors.New("任务对应的上传尝试已过期")

// Verify 执行一次校验。返回 nil 表示任务已终结（ready、failed 或过期任务）；
// 返回非 nil 表示暂时性故障（对象存储、数据库不可达），worker 应重试。
func (v *Verifier) Verify(ctx context.Context, args VerifyJobArgs) error {
	res, attempt, err := v.loadProcessing(ctx, args)
	if err != nil {
		if errors.Is(err, ErrStaleJob) {
			v.logger.InfoContext(ctx, "资产校验任务已过期，跳过", "asset_id", args.AssetID, "upload_attempt", args.UploadAttempt)
			return nil
		}
		return err
	}

	keys := v.keys.KeysFor(args.AccountID, args.AssetID, int(args.UploadAttempt))
	outcome, err := v.inspectStaging(ctx, res, attempt, keys)
	if err != nil {
		return err
	}
	if outcome.failCode != "" {
		return v.fail(ctx, args, outcome.failCode)
	}

	// 以校验时读取到的 ETag 为条件复制：若暂存对象在读取后被改写，复制失败即判定本次上传无效。
	if err := v.objects.CopyIfMatch(ctx, keys.Staging, keys.Final, outcome.etag, outcome.info.MediaType); err != nil {
		switch {
		case isPreconditionFailed(err):
			return v.fail(ctx, args, FailUploadModified)
		case isNotFound(err):
			return v.fail(ctx, args, FailUploadMissing)
		}
		return fmt.Errorf("复制到最终键: %w", err)
	}
	outcome.info.FinalKey = keys.Final

	if err := v.markReady(ctx, args, outcome.info); err != nil {
		return err
	}
	// 暂存对象已无用途；删除失败不影响结果，生命周期规则会兜底。
	if err := v.objects.Delete(ctx, keys.Staging); err != nil {
		v.logger.WarnContext(ctx, "删除暂存对象失败，等待生命周期清理", "key", keys.Staging, "error", err)
	}

	if outcome.info.Image != nil {
		v.thumbnail(ctx, args, keys, outcome.data)
	}
	return nil
}

// Fail 由 worker 在重试耗尽时调用，把仍处于 processing 的资产标记为失败，
// 避免资产永远停在 processing 让用户无法重新上传。
func (v *Verifier) Fail(ctx context.Context, args VerifyJobArgs, code string) error {
	return v.fail(ctx, args, code)
}

// stagingOutcome 是暂存对象检查的结果：要么判定失败，要么得到可写回的校验信息与内容。
type stagingOutcome struct {
	failCode string
	etag     string
	info     VerifiedInfo
	data     []byte
}

// inspectStaging 读取并校验暂存对象。只有暂时性故障返回 error；内容问题走 failCode。
func (v *Verifier) inspectStaging(ctx context.Context, res Resource, attempt AttemptInfo, keys Keys) (stagingOutcome, error) {
	rc, info, err := v.objects.Get(ctx, keys.Staging)
	if err != nil {
		if isNotFound(err) {
			return stagingOutcome{failCode: FailUploadMissing}, nil
		}
		return stagingOutcome{}, fmt.Errorf("读取暂存对象: %w", err)
	}
	defer rc.Close()

	// 读取上限取两类中较大者：类型要嗅探后才知道，先按最大值读，再按实际类型判定。
	readCap := max(v.limits.ImageMaxBytes, v.limits.PDFMaxBytes)
	data, err := readLimited(rc, readCap)
	if err != nil {
		return stagingOutcome{}, fmt.Errorf("读取暂存内容: %w", err)
	}
	if int64(len(data)) > readCap {
		return stagingOutcome{failCode: FailTooLarge}, nil
	}

	sum := sha256.Sum256(data)
	if attempt.ClientSHA256 != nil && !bytes.Equal(attempt.ClientSHA256, sum[:]) {
		return stagingOutcome{failCode: FailChecksumMismatch}, nil
	}

	// 类型只看内容魔数，声明类型与扩展名都不作数。
	mediaType := v.images.SniffMediaType(data)
	isImage := slices.Contains(v.limits.ImageMediaTypes, mediaType)
	isPDF := slices.Contains(v.limits.PDFMediaTypes, mediaType)
	switch {
	case mediaType == "", !isImage && !isPDF:
		return stagingOutcome{failCode: FailUnsupportedType}, nil
	case res.Scope == ScopeAvatar && !isImage:
		return stagingOutcome{failCode: FailUnsupportedType}, nil
	}
	limit := v.limits.ImageMaxBytes
	if isPDF {
		limit = v.limits.PDFMaxBytes
	}
	if int64(len(data)) > limit {
		return stagingOutcome{failCode: FailTooLarge}, nil
	}

	verified := VerifiedInfo{MediaType: mediaType, ByteSize: int64(len(data)), SHA256: sum[:]}
	if isImage {
		img, err := v.images.Inspect(data)
		switch {
		case errors.Is(err, ErrImageTooLarge):
			return stagingOutcome{failCode: FailImageTooLarge}, nil
		case errors.Is(err, ErrNotImage):
			return stagingOutcome{failCode: FailCorruptImage}, nil
		case err != nil:
			return stagingOutcome{}, fmt.Errorf("检查图片: %w", err)
		}
		verified.Image = &img
	}
	return stagingOutcome{etag: info.ETag, info: verified, data: data}, nil
}

// loadProcessing 读取资产并确认它仍在等待本次尝试的校验。
func (v *Verifier) loadProcessing(ctx context.Context, args VerifyJobArgs) (Resource, AttemptInfo, error) {
	res, found, err := v.reader.Get(ctx, args.AccountID, args.AssetID)
	if err != nil {
		return Resource{}, AttemptInfo{}, fmt.Errorf("读取资产: %w", err)
	}
	// 不存在、已删除、不在 processing、或尝试序号对不上，都说明任务已过期。
	if !found || res.DeletedAt != nil || res.Status != StatusProcessing || res.UploadAttempt != args.UploadAttempt {
		return Resource{}, AttemptInfo{}, ErrStaleJob
	}
	attempt, found, err := v.reader.Attempt(ctx, args.AccountID, args.AssetID)
	if err != nil {
		return Resource{}, AttemptInfo{}, fmt.Errorf("读取声明信息: %w", err)
	}
	if !found {
		return Resource{}, AttemptInfo{}, ErrStaleJob
	}
	return res, attempt, nil
}

// markReady 在写事务内复核状态后写入校验结果。
func (v *Verifier) markReady(ctx context.Context, args VerifyJobArgs, info VerifiedInfo) error {
	req := v.request(args, "asset_verify")
	_, err := v.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, args.AccountID, args.AssetID)
		if err != nil {
			return apperr.Internal(err)
		}
		// 复核：对象 I/O 期间用户可能删除了资产或开了新尝试，此时不能把旧内容标成 ready。
		if !found || current.DeletedAt != nil || current.Status != StatusProcessing || current.UploadAttempt != args.UploadAttempt {
			return ErrStaleJob
		}
		updated, err := repo.MarkReady(ctx, args.AccountID, args.AssetID, info, v.clock.Now())
		if err != nil {
			return apperr.Internal(err)
		}
		recordWrite(scope, updated, []string{
			"status", "media_type", "byte_size", "sha256", "width", "height",
			"exif_taken_at_local", "exif_latitude", "exif_longitude", "thumbnail_status", "upload_expires_at",
		})
		return nil
	}, nil)
	return v.terminalOrRetry(ctx, err, args, "写入校验结果")
}

// fail 把资产标记为失败并清空暂存键；用户之后可开新尝试。
func (v *Verifier) fail(ctx context.Context, args VerifyJobArgs, code string) error {
	req := v.request(args, "asset_verify")
	_, err := v.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, args.AccountID, args.AssetID)
		if err != nil {
			return apperr.Internal(err)
		}
		if !found || current.DeletedAt != nil || current.Status != StatusProcessing || current.UploadAttempt != args.UploadAttempt {
			return ErrStaleJob
		}
		updated, err := repo.MarkFailed(ctx, args.AccountID, args.AssetID, code, v.clock.Now())
		if err != nil {
			return apperr.Internal(err)
		}
		recordWrite(scope, updated, []string{"status", "error_code", "upload_expires_at"})
		return nil
	}, nil)
	if err == nil {
		v.logger.InfoContext(ctx, "资产校验失败", "asset_id", args.AssetID, "upload_attempt", args.UploadAttempt, "error_code", code)
		// 失败的暂存对象不再需要；删除失败由生命周期兜底。
		keys := v.keys.KeysFor(args.AccountID, args.AssetID, int(args.UploadAttempt))
		_ = v.objects.Delete(ctx, keys.Staging)
	}
	return v.terminalOrRetry(ctx, err, args, "写入失败状态")
}

// thumbnail 生成并写入缩略图；任何失败都只标记 thumbnail_status=failed，原图保持可用。
func (v *Verifier) thumbnail(ctx context.Context, args VerifyJobArgs, keys Keys, data []byte) {
	status, key := ThumbnailReady, keys.Thumbnail
	thumb, err := v.images.Thumbnail(data)
	if err != nil {
		v.logger.WarnContext(ctx, "生成缩略图失败", "asset_id", args.AssetID, "error", err)
		status, key = ThumbnailFailed, ""
	} else if err := v.objects.Put(ctx, keys.Thumbnail, "image/jpeg", bytes.NewReader(thumb), int64(len(thumb))); err != nil {
		v.logger.WarnContext(ctx, "写入缩略图失败", "asset_id", args.AssetID, "error", err)
		status, key = ThumbnailFailed, ""
	}

	req := v.request(args, "asset_thumbnail")
	_, err = v.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, args.AccountID, args.AssetID)
		if err != nil {
			return apperr.Internal(err)
		}
		// 只给仍是本次尝试、仍在等缩略图的资产写结果。
		if !found || current.DeletedAt != nil || current.UploadAttempt != args.UploadAttempt || current.ThumbnailStatus != ThumbnailProcessing {
			return ErrStaleJob
		}
		updated, err := repo.SetThumbnail(ctx, args.AccountID, args.AssetID, status, key, v.clock.Now())
		if err != nil {
			return apperr.Internal(err)
		}
		recordWrite(scope, updated, []string{"thumbnail_status"})
		return nil
	}, nil)
	if err != nil && !errors.Is(err, ErrStaleJob) {
		// 缩略图状态写不进去不值得让整个校验任务重跑：原图已 ready。
		v.logger.WarnContext(ctx, "写入缩略图状态失败", "asset_id", args.AssetID, "error", err)
	}
}

// request 用资产与尝试序号派生确定性的操作编号：任务重试命中收据后直接重放，不重复写。
func (v *Verifier) request(args VerifyJobArgs, opType string) write.Request {
	target := fmt.Sprintf("%s:%s:%d", opType, args.AssetID, args.UploadAttempt)
	return write.Request{
		AccountID:     args.AccountID,
		OperationID:   uuid.NewSHA1(uuid.NameSpaceOID, []byte(target)),
		OperationType: opType,
		Fingerprint:   write.Fingerprint(opType, args.AssetID.String(), nil, args),
	}
}

// terminalOrRetry 把写事务错误分成两类：过期任务与账号级拒绝直接终结；其余交给 worker 重试。
func (v *Verifier) terminalOrRetry(ctx context.Context, err error, args VerifyJobArgs, step string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrStaleJob) {
		v.logger.InfoContext(ctx, "资产状态已变化，放弃写入", "asset_id", args.AssetID, "step", step)
		return nil
	}
	if e, ok := apperr.As(err); ok {
		// 账号注销中或会话状态缺失：重试也不会成功。
		if e.Status == 401 || e.Status == 403 {
			v.logger.WarnContext(ctx, "账号不可写，放弃资产校验", "asset_id", args.AssetID, "code", e.Code)
			return nil
		}
	}
	return fmt.Errorf("%s: %w", step, err)
}

// isNotFound 与 isPreconditionFailed 通过错误链上的方法识别对象存储的分类错误，
// 让模块不必导入 adapters/objectstore 的哨兵值。
func isNotFound(err error) bool {
	var target interface{ IsObjectNotFound() bool }
	if errors.As(err, &target) {
		return target.IsObjectNotFound()
	}
	return false
}

func isPreconditionFailed(err error) bool {
	var target interface{ IsPreconditionFailed() bool }
	if errors.As(err, &target) {
		return target.IsPreconditionFailed()
	}
	return false
}

// readLimited 最多读 limit+1 字节：多出的一字节让调用方能区分「恰好等于上限」与「超限」。
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}
