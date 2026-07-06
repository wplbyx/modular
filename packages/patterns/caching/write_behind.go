package caching

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// WriteBehind 实现 Write-Behind（异步写回）模式。
// 写：立即更新缓存 -> 异步写入源
// 读：查缓存 -> 未命中则从源读取 -> 回写缓存
type WriteBehind struct {
	cache         KVCache
	ttl           time.Duration
	queue         chan writeTask
	wg            sync.WaitGroup
	pending       sync.WaitGroup
	stopCh        chan struct{}
	mu            sync.RWMutex
	stopped       bool
	defaultWriter WriteBehindWriter
}

type writeTask struct {
	ctx    context.Context
	key    string
	value  string
	writer func(context.Context) error
}

type WriteBehindWriter func(ctx context.Context, key string, value string) error
type WriteBehindOption func(*WriteBehind)

func WithWriteBehindWriter(writer WriteBehindWriter) WriteBehindOption {
	return func(wb *WriteBehind) {
		wb.defaultWriter = writer
	}
}

// NewWriteBehind 创建 WriteBehind 实例
func NewWriteBehind(c KVCache, ttl time.Duration, queueSize int, opts ...WriteBehindOption) *WriteBehind {
	if queueSize <= 0 {
		queueSize = 1
	}
	wb := &WriteBehind{
		cache:  c,
		ttl:    ttl,
		queue:  make(chan writeTask, queueSize),
		stopCh: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(wb)
	}

	wb.wg.Add(1)
	go wb.processQueue()

	return wb
}

// Get retrieves data using write-behind pattern
func (wb *WriteBehind) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	val, err := wb.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}

	data, err := loader()
	if err != nil {
		return "", err
	}

	// 缓存回写失败不影响读结果，显式忽略
	_ = wb.cache.Set(ctx, key, data, wb.ttl)
	return data, nil
}

// Set updates cache immediately and queues DB write
func (wb *WriteBehind) Set(ctx context.Context, key string, value string) error {
	if wb.defaultWriter == nil {
		return errors.New("write-behind default writer is nil")
	}
	if err := wb.cache.Set(ctx, key, value, wb.ttl); err != nil {
		return err
	}

	return wb.enqueue(writeTask{
		ctx:   ctx,
		key:   key,
		value: value,
		writer: func(taskCtx context.Context) error {
			return wb.defaultWriter(taskCtx, key, value)
		},
	})
}

// SetWithWriter updates cache and queues custom writer
func (wb *WriteBehind) SetWithWriter(ctx context.Context, key string, value string, writer func() error) error {
	if writer == nil {
		return errors.New("write-behind writer is nil")
	}
	if err := wb.cache.Set(ctx, key, value, wb.ttl); err != nil {
		return err
	}

	return wb.enqueue(writeTask{
		ctx:   ctx,
		key:   key,
		value: value,
		writer: func(context.Context) error {
			return writer()
		},
	})
}

func (wb *WriteBehind) enqueue(task writeTask) error {
	if task.ctx == nil {
		task.ctx = context.Background()
	}

	wb.mu.RLock()
	stopped := wb.stopped
	wb.mu.RUnlock()
	if stopped {
		return errors.New("write-behind is stopped")
	}

	wb.pending.Add(1)
	select {
	case wb.queue <- task:
		return nil
	case <-task.ctx.Done():
		wb.pending.Done()
		return task.ctx.Err()
	default:
		wb.pending.Done()
		return fmt.Errorf("write-behind queue is full")
	}
}

func (wb *WriteBehind) processQueue() {
	defer wb.wg.Done()

	for {
		select {
		case <-wb.stopCh:
			wb.drainQueue()
			return
		case task := <-wb.queue:
			wb.runTask(task)
		}
	}
}

func (wb *WriteBehind) drainQueue() {
	for {
		select {
		case task := <-wb.queue:
			wb.runTask(task)
		default:
			return
		}
	}
}

func (wb *WriteBehind) runTask(task writeTask) {
	defer wb.pending.Done()
	if task.writer == nil {
		return
	}
	_ = task.writer(task.ctx)
}

// Stop stops the background processor
func (wb *WriteBehind) Stop() {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if wb.stopped {
		return
	}

	wb.stopped = true
	close(wb.stopCh)
	wb.wg.Wait()
}

// Flush flushes remaining items in queue
func (wb *WriteBehind) Flush(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = wb.FlushContext(ctx)
}

func (wb *WriteBehind) FlushContext(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		wb.pending.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
