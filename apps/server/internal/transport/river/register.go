// Package riverjobs 是 River 任务的传输层：解析任务参数并调用应用服务。
// 它不复制 HTTP 层的业务逻辑，也不直接修改业务表。目录名沿用结构文档中的 transport/river。
package riverjobs

import (
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

// snoozeUnconfigured 是能力未装配时推迟任务的时长。
const snoozeUnconfigured = 5 * time.Minute

// Deps 是各任务处理器需要的应用服务。为 nil 的能力对应的任务会被推迟而不是失败。
type Deps struct {
	Logger        *slog.Logger
	AssetVerifier AssetVerifier
}

// RegisterWorkers 把全部任务处理器注册到 workers。新增任务类型在此追加一行。
func RegisterWorkers(workers *river.Workers, d Deps) {
	river.AddWorker(workers, &PingWorker{logger: d.Logger})
	river.AddWorker(workers, &PurgeTripWorker{logger: d.Logger})
	river.AddWorker(workers, &VerifyAssetWorker{verifier: d.AssetVerifier, logger: d.Logger})
}
