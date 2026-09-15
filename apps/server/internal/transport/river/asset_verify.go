package riverjobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"tripfolio/server/internal/modules/assets"
)

// AssetVerifier 是 worker 需要的校验能力，由 assets.Verifier 实现。
type AssetVerifier interface {
	Verify(ctx context.Context, args assets.VerifyJobArgs) error
	Fail(ctx context.Context, args assets.VerifyJobArgs, code string) error
}

// VerifyAssetWorker 处理 assets.VerifyJobArgs：校验暂存对象并把资产标为 ready 或 failed。
// 暂时性故障交给 River 按退避重试；最后一次尝试仍失败时把资产标为 failed，
// 否则资产会永远停在 processing，用户既不能下载也不能重新上传。
type VerifyAssetWorker struct {
	river.WorkerDefaults[assets.VerifyJobArgs]
	verifier AssetVerifier
	logger   *slog.Logger
}

// Work 实现 river.Worker。
func (w *VerifyAssetWorker) Work(ctx context.Context, job *river.Job[assets.VerifyJobArgs]) error {
	if w.verifier == nil {
		// 未配置对象存储的进程不应消费此任务；推迟而不是失败，等有能力的 worker 接手。
		w.logger.WarnContext(ctx, "资产校验器未装配，推迟任务", "job_id", job.ID, "asset_id", job.Args.AssetID)
		return river.JobSnooze(snoozeUnconfigured)
	}

	err := w.verifier.Verify(ctx, job.Args)
	if err == nil {
		return nil
	}
	w.logger.WarnContext(ctx, "资产校验暂时失败",
		"job_id", job.ID, "asset_id", job.Args.AssetID, "upload_attempt", job.Args.UploadAttempt,
		"attempt", job.Attempt, "max_attempts", job.MaxAttempts, "error", err)

	// 最后一次机会也没成功：释放资产让用户能重新上传，任务本身按成功结束。
	if job.Attempt >= job.MaxAttempts {
		if failErr := w.verifier.Fail(ctx, job.Args, assets.FailProcessing); failErr != nil {
			w.logger.ErrorContext(ctx, "重试耗尽且无法标记失败", "asset_id", job.Args.AssetID, "error", failErr)
			return failErr
		}
		return nil
	}
	return err
}
