// Package riverjobs 是 River 任务的传输层：解析任务参数并调用应用服务。
// 它不复制 HTTP 层的业务逻辑，也不直接修改业务表。目录名沿用结构文档中的 transport/river。
package riverjobs

import (
	"log/slog"

	"github.com/riverqueue/river"
)

// RegisterWorkers 把全部任务处理器注册到 workers。新增任务类型在此追加一行。
func RegisterWorkers(workers *river.Workers, logger *slog.Logger) {
	river.AddWorker(workers, &PingWorker{logger: logger})
	river.AddWorker(workers, &PurgeTripWorker{logger: logger})
}
