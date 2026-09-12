package queue

// PingArgs 是用于验证事务入队与 worker 链路的最小任务。
// 后续业务任务（图片校验、缩略图、导出、清理）按同样方式在本文件声明参数类型。
type PingArgs struct {
	Message string `json:"message"`
}

// Kind 是 River 任务类型名，写入 river_job.kind。
func (PingArgs) Kind() string { return "ping" }
