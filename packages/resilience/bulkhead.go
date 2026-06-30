package resilience

import (
	"context"
	"sync"
	"time"

	"holographic/packages/errs"
	"holographic/packages/log"
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
	queue     chan func()
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
		config: config,
		queue:  make(chan func(), config.QueueSize),
	}

	// 启动工作协程
	for i := 0; i < config.MaxConcurrentCalls; i++ {
		go b.worker()
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
func (b *bulkheadImpl) Execute(ctx context.Context, fn func() error) error {
	// 检查隔板是否已关闭
	b.mutex.RLock()
	if b.closed {
		b.mutex.RUnlock()
		return errs.New(
			errs.WithCode(521),
			errs.WithMsgf("bulkhead '%s' is closed", b.config.Name),
			errs.WithCause(ErrBulkheadClosed),
		)
	}
	b.mutex.RUnlock()

	// 创建一个结果通道
	errCh := make(chan error, 1)

	// 创建一个包装函数
	wrapper := func() {
		// 增加运行计数
		b.mutex.Lock()
		b.running++
		b.mutex.Unlock()

		// 执行函数
		err := fn()

		// 减少运行计数
		b.mutex.Lock()
		b.running--
		b.mutex.Unlock()

		// 发送结果
		errCh <- err
	}

	// 计算等待超时时间
	timeout := b.config.WaitTimeout
	if deadline, ok := ctx.Deadline(); ok {
		// 如果上下文有截止时间，使用较小的值
		if waitTime := time.Until(deadline); waitTime < timeout {
			timeout = waitTime
		}
	}

	// 尝试将任务放入队列
	select {
	case b.queue <- wrapper:
		// 任务已放入队列，等待执行完成
	case <-time.After(timeout):
		// 队列已满且等待超时
		running := b.Running()
		queueLen := len(b.queue)
		log.Infof("Bulkhead '%s' queue timeout, running: %d, queue length: %d, max concurrent: %d, queue size: %d",
			b.config.Name, running, queueLen, b.config.MaxConcurrentCalls, b.config.QueueSize)
		return errs.New(
			errs.WithCode(520),
			errs.WithMsgf("bulkhead '%s' queue timeout", b.config.Name),
			errs.WithCause(ErrBulkheadFull),
		)
	case <-ctx.Done():
		// 上下文已取消
		return errs.New(
			errs.WithCode(499),
			errs.WithMsgf("context canceled while waiting for bulkhead '%s'", b.config.Name),
			errs.WithCause(ctx.Err()),
		)
	}

	// 等待任务执行完成
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		// 上下文已取消
		return errs.New(
			errs.WithCode(499),
			errs.WithMsgf("context canceled while waiting for bulkhead '%s' execution", b.config.Name),
			errs.WithCause(ctx.Err()),
		)
	}
}

// worker 工作协程，不断从队列中获取任务并执行
func (b *bulkheadImpl) worker() {
	for fn := range b.queue {
		fn()
	}
}

// Close 关闭隔板
func (b *bulkheadImpl) Close() {
	b.closeOnce.Do(func() {
		b.mutex.Lock()
		b.closed = true
		b.mutex.Unlock()
		close(b.queue)
	})
}
