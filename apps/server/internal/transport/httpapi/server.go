package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// NewServer 创建带超时的 http.Server。读头 10 秒、读 30 秒、写 60 秒、空闲 120 秒、头部 64 KiB；
// 正文上限由各接口按契约（默认 1 MiB）在处理链中限制。
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// Serve 运行服务器直到 ctx 结束，然后在 shutdownTimeout 内停止接收新请求并等待在途请求退出。
func Serve(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http 服务开始监听", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("收到退出信号，开始优雅关闭", "timeout", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("http 服务已关闭")
	return nil
}
