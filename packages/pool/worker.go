package pool

import "context"

// WorkerTask 协程池任务函数
type WorkerTask func(ctx context.Context) error

// WorkerPool 定义了协程池的通用接口
type WorkerPool interface {
	Submit(ctx context.Context, task WorkerTask) error
	Close()
}
