package pool

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/panjf2000/ants/v2"
	"go.opentelemetry.io/otel/trace"
)

var wp *AntsWorkerPool

type AntsWorkerPool struct {
	pool *ants.Pool
	wg   sync.WaitGroup // 使用 WaitGroup 来确保 Close 操作能等待所有任务结束
}

func NewAntsWorkerPool(capacity int) (*AntsWorkerPool, error) {

	p, err := ants.NewPool(capacity, ants.WithOptions(ants.Options{PreAlloc: true}))
	if err != nil {
		return nil, fmt.Errorf("failed to create ants pool: %w", err)
	}

	wp = &AntsWorkerPool{pool: p}

	return wp, nil
}

func Submit(ctx context.Context, task WorkerTask) error {
	if wp == nil {
		return fmt.Errorf("worker pool is not initialized")
	}
	return wp.Submit(ctx, task)
}

// Submit 实现了 Pool 接口的 Submit 方法
func (ap *AntsWorkerPool) Submit(ctx context.Context, task WorkerTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ap.wg.Add(1)

	// 获取当前的 trace span 上下文，用于在新的 goroutine 中延续链路追踪
	span := trace.SpanFromContext(ctx)
	spanContext := span.SpanContext()

	if err := ap.pool.Submit(func() {
		defer func() {
			ap.wg.Done()
			if r := recover(); r != nil {
				log.Printf("GO-PANIC: task recovered from panic: %v", r)
			}
		}()

		// 在新的 goroutine 中创建新的 span context，延续链路追踪
		taskCtx := trace.ContextWithSpanContext(ctx, spanContext)

		if err := task(taskCtx); err != nil {
			log.Printf("GO-ERROR: task failed: %v", err)
		}
	}); err != nil {
		ap.wg.Done()
		return err
	}

	return nil
}

// Close 实现了 Pool 接口的 Close 方法
func (ap *AntsWorkerPool) Close() {
	ap.wg.Wait()
	ap.pool.Release()
}
