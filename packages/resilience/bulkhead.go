package resilience

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/log"
	"go.uber.org/zap"
)

var (
	bulkheadFullMessage    = errs.Define("BULKHEAD_FULL", errs.Template("too many requests"))
	bulkheadClosedMessage  = errs.Define("BULKHEAD_CLOSED", errs.Template("service is temporarily unavailable"))
	bulkheadPanicMessage   = errs.Define("BULKHEAD_PANIC", errs.Template("internal server error"))
	bulkheadContextMessage = errs.Define("BULKHEAD_CONTEXT_CANCELED", errs.Template("request canceled"))

	// ErrBulkheadFull 隔板已满错误
	ErrBulkheadFull = errs.TooManyRequests(bulkheadFullMessage)
	// ErrBulkheadClosed 隔板已关闭错误
	ErrBulkheadClosed = errs.ServiceUnavailable(bulkheadClosedMessage)
)

// BulkheadConfig 隔板配置
type BulkheadConfig struct {
	// Name 隔板名称
	Name string
	// MaxConcurrentCalls 最大并发调用数
	MaxConcurrentCalls int
	// QueueSize 等待队列大小
	QueueSize int
	// WaitTimeout 队列等待超时时间
	WaitTimeout time.Duration
}

// 默认隔板配置
var DefaultBulkheadConfig = BulkheadConfig{
	Name:               "default",
	MaxConcurrentCalls: 10,
	QueueSize:          5,
	WaitTimeout:        5 * time.Second,
}

// bulkheadImpl 隔板模式实现
type bulkheadImpl struct {
	config BulkheadConfig

	running   atomic.Int64
	waiting   atomic.Int64
	slots     chan struct{}
	queue     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewBulkhead 创建一个新的隔板
func NewBulkhead(config BulkheadConfig) Bulkhead {
	// 使用默认配置填充未设置的字段
	if config.MaxConcurrentCalls <= 0 {
		config.MaxConcurrentCalls = DefaultBulkheadConfig.MaxConcurrentCalls
	}
	if config.QueueSize < 0 {
		config.QueueSize = DefaultBulkheadConfig.QueueSize
	}
	if config.WaitTimeout <= 0 {
		config.WaitTimeout = DefaultBulkheadConfig.WaitTimeout
	}
	if config.Name == "" {
		config.Name = DefaultBulkheadConfig.Name
	}

	b := &bulkheadImpl{
		config: config,
		slots:  make(chan struct{}, config.MaxConcurrentCalls),
		queue:  make(chan struct{}, config.QueueSize),
		done:   make(chan struct{}),
	}

	return b
}

// Name 返回隔板名称
func (b *bulkheadImpl) Name() string {
	return b.config.Name
}

// Running 返回当前运行中的请求数
func (b *bulkheadImpl) Running() int {
	return int(b.running.Load())
}

// Execute 在隔板内执行函数
func (b *bulkheadImpl) Execute(ctx context.Context, fn func() error) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return fmt.Errorf("bulkhead function is nil")
	}
	if err := b.acquire(ctx); err != nil {
		return err
	}
	defer func() {
		b.release()
		if recovered := recover(); recovered != nil {
			err = errs.InternalServer(
				bulkheadPanicMessage.With("name", b.config.Name),
				errs.WithCause(fmt.Errorf("panic: %v", recovered)),
				errs.WithField("bulkhead", b.config.Name),
			)
		}
	}()

	return fn()
}

// Close 关闭隔板
func (b *bulkheadImpl) Close() {
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.done)
	})
}

func (b *bulkheadImpl) acquire(ctx context.Context) error {
	if b.closed.Load() {
		return b.closedError()
	}

	select {
	case b.slots <- struct{}{}:
		b.running.Add(1)
		if b.closed.Load() {
			b.running.Add(-1)
			<-b.slots
			return b.closedError()
		}
		return nil
	default:
	}

	select {
	case b.queue <- struct{}{}:
		b.waiting.Add(1)
	default:
		return b.fullError(ctx, "queue full")
	}
	defer func() {
		b.waiting.Add(-1)
		<-b.queue
	}()

	timer := time.NewTimer(b.config.WaitTimeout)
	defer timer.Stop()
	select {
	case b.slots <- struct{}{}:
		b.running.Add(1)
		if b.closed.Load() {
			b.running.Add(-1)
			<-b.slots
			return b.closedError()
		}
		return nil
	case <-b.done:
		return b.closedError()
	case <-timer.C:
		return b.fullError(ctx, "queue timeout")
	case <-ctx.Done():
		return errs.ClientClosed(
			bulkheadContextMessage.With("name", b.config.Name),
			errs.WithCause(ctx.Err()),
			errs.WithField("bulkhead", b.config.Name),
		)
	}
}

func (b *bulkheadImpl) release() {
	b.running.Add(-1)
	<-b.slots
}

func (b *bulkheadImpl) closedError() error {
	return errs.ServiceUnavailable(
		bulkheadClosedMessage.With("name", b.config.Name),
		errs.WithCause(ErrBulkheadClosed),
		errs.WithField("bulkhead", b.config.Name),
	)
}

func (b *bulkheadImpl) Waiting() int {
	return int(b.waiting.Load())
}

func (b *bulkheadImpl) fullError(ctx context.Context, cause string) error {
	log.Info(ctx, "bulkhead rejected request",
		zap.String("bulkhead", b.config.Name),
		zap.String("cause", cause),
		zap.Int("running", b.Running()),
		zap.Int("waiting", b.Waiting()),
		zap.Int("max_concurrent", b.config.MaxConcurrentCalls),
		zap.Int("queue_size", b.config.QueueSize),
	)
	return errs.TooManyRequests(
		bulkheadFullMessage.With("name", b.config.Name),
		errs.WithCause(ErrBulkheadFull),
		errs.WithField("bulkhead", b.config.Name),
	)
}
