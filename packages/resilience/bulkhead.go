package resilience

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/log"
)

var (
	// ErrBulkheadFull 隔板已满错误
	ErrBulkheadFull = errs.New(
		errs.WithCode(520),
		errs.WithMsgf("bulkhead is full"),
	)
	// ErrBulkheadClosed 隔板已关闭错误
	ErrBulkheadClosed = errs.New(
		errs.WithCode(521),
		errs.WithMsgf("bulkhead is closed"),
	)
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

	mutex     sync.RWMutex
	running   int
	waiting   int
	released  chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closed    bool
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
		config:   config,
		released: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}

	return b
}

// Name 返回隔板名称
func (b *bulkheadImpl) Name() string {
	return b.config.Name
}

// Running 返回当前运行中的请求数
func (b *bulkheadImpl) Running() int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.running
}

// Execute 在隔板内执行函数
func (b *bulkheadImpl) Execute(ctx context.Context, fn func() error) (err error) {
	if err := b.acquire(ctx); err != nil {
		return err
	}
	defer func() {
		b.release()
		if recovered := recover(); recovered != nil {
			err = errs.New(
				errs.WithCode(522),
				errs.WithMsgf("panic in bulkhead '%s': %v", b.config.Name, recovered),
				errs.WithCause(fmt.Errorf("panic: %v", recovered)),
			)
		}
	}()

	return fn()
}

// Close 关闭隔板
func (b *bulkheadImpl) Close() {
	b.closeOnce.Do(func() {
		b.mutex.Lock()
		b.closed = true
		close(b.done)
		b.mutex.Unlock()
		b.notifyReleased()
	})
}

func (b *bulkheadImpl) acquire(ctx context.Context) error {
	timeout := b.config.WaitTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if waitTime := time.Until(deadline); waitTime < timeout {
			timeout = waitTime
		}
	}
	if timeout <= 0 {
		timeout = b.config.WaitTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	queued := false
	defer func() {
		if queued {
			b.mutex.Lock()
			b.waiting--
			b.mutex.Unlock()
		}
	}()

	for {
		b.mutex.Lock()
		if b.closed {
			b.mutex.Unlock()
			return b.closedError()
		}
		if b.running < b.config.MaxConcurrentCalls {
			b.running++
			b.mutex.Unlock()
			return nil
		}
		if !queued && b.waiting < b.config.QueueSize {
			b.waiting++
			queued = true
		}
		b.mutex.Unlock()

		select {
		case <-b.released:
			continue
		case <-b.done:
			return b.closedError()
		case <-timer.C:
			log.Infof("Bulkhead '%s' queue timeout, running: %d, queue length: %d, max concurrent: %d, queue size: %d",
				b.config.Name, b.Running(), b.Waiting(), b.config.MaxConcurrentCalls, b.config.QueueSize)
			return errs.New(
				errs.WithCode(520),
				errs.WithMsgf("bulkhead '%s' queue timeout", b.config.Name),
				errs.WithCause(ErrBulkheadFull),
			)
		case <-ctx.Done():
			return errs.New(
				errs.WithCode(499),
				errs.WithMsgf("context canceled while waiting for bulkhead '%s'", b.config.Name),
				errs.WithCause(ctx.Err()),
			)
		}
	}
}

func (b *bulkheadImpl) release() {
	b.mutex.Lock()
	if b.running > 0 {
		b.running--
	}
	b.mutex.Unlock()
	b.notifyReleased()
}

func (b *bulkheadImpl) notifyReleased() {
	select {
	case b.released <- struct{}{}:
	default:
	}
}

func (b *bulkheadImpl) closedError() error {
	return errs.New(
		errs.WithCode(521),
		errs.WithMsgf("bulkhead '%s' is closed", b.config.Name),
		errs.WithCause(ErrBulkheadClosed),
	)
}

func (b *bulkheadImpl) Waiting() int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.waiting
}
