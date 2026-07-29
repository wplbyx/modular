package pool

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/core"
	modularlog "github.com/wplbyx/modular/packages/log"
)

const (
	stateNew int32 = iota
	stateRunning
	stateClosing
	stateClosed
)

type queuedTask struct {
	ctx  context.Context
	task WorkerTask
}

// AntsWorkerPool adapts ants behind the bounded WorkerPool interface.
type AntsWorkerPool struct {
	config Config
	logger modularlog.Logger

	state atomic.Int32
	pool  *ants.Pool
	queue chan queuedTask
	slots chan struct{}

	lifecycleMu sync.Mutex

	taskCtx    context.Context
	cancelTask context.CancelFunc
	dispatched chan struct{}

	admissionMu   sync.Mutex
	accepting     bool
	producers     int
	producersDone chan struct{}

	closeOnce sync.Once
	closeDone chan struct{}
	closeMu   sync.Mutex
	closeErr  error

	queued    atomic.Int64
	running   atomic.Int64
	accepted  atomic.Uint64
	completed atomic.Uint64
	rejected  atomic.Uint64
	canceled  atomic.Uint64
	failed    atomic.Uint64
	panicked  atomic.Uint64
}

var (
	_ WorkerPool    = (*AntsWorkerPool)(nil)
	_ core.Resource = (*AntsWorkerPool)(nil)
)

// New creates an ants-backed worker pool. Setup must be called before Submit.
func New(config Config, logger modularlog.Logger) (*AntsWorkerPool, error) {
	if config.Name == "" {
		config.Name = "worker-pool"
	}
	if config.Capacity <= 0 {
		return nil, fmt.Errorf("%w: capacity must be positive", ErrInvalid)
	}
	if config.Policy != Reject && config.Policy != Queue {
		return nil, fmt.Errorf("%w: unknown overload policy %d", ErrInvalid, config.Policy)
	}
	if config.Policy == Queue && config.QueueCapacity <= 0 {
		return nil, fmt.Errorf("%w: queue capacity must be positive in queue mode", ErrInvalid)
	}
	if config.Policy == Reject && config.QueueCapacity != 0 {
		return nil, fmt.Errorf("%w: queue capacity is only valid in queue mode", ErrInvalid)
	}
	if config.ExpiryDuration < 0 {
		return nil, fmt.Errorf("%w: expiry duration cannot be negative", ErrInvalid)
	}
	if logger == nil {
		return nil, fmt.Errorf("%w: logger is nil", ErrInvalid)
	}

	return &AntsWorkerPool{
		config:     config,
		logger:     logger.Named(config.Name),
		closeDone:  make(chan struct{}),
		dispatched: make(chan struct{}),
	}, nil
}

// Name returns the Resource log label.
func (ap *AntsWorkerPool) Name() string { return ap.config.Name }

// Setup starts the ants pool and the optional bounded dispatcher.
func (ap *AntsWorkerPool) Setup(ctx context.Context) error {
	ap.lifecycleMu.Lock()
	defer ap.lifecycleMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ap.state.CompareAndSwap(stateNew, stateRunning) {
		if ap.state.Load() >= stateClosing {
			return ErrClosed
		}
		return nil
	}

	ap.taskCtx, ap.cancelTask = context.WithCancel(context.Background())
	options := []ants.Option{
		ants.WithNonblocking(true),
		ants.WithPreAlloc(ap.config.PreAlloc),
		ants.WithLogger(antsLogAdapter{logger: ap.logger}),
		ants.WithPanicHandler(func(value any) {
			ap.panicked.Add(1)
			ap.logger.Error(context.Background(), "worker pool recovered unexpected panic",
				zap.Any("panic", value),
				zap.ByteString("stack", debug.Stack()),
			)
		}),
	}
	if ap.config.ExpiryDuration > 0 {
		options = append(options, ants.WithExpiryDuration(ap.config.ExpiryDuration))
	}
	p, err := ants.NewPool(ap.config.Capacity, options...)
	if err != nil {
		ap.cancelTask()
		ap.state.Store(stateClosed)
		close(ap.closeDone)
		return fmt.Errorf("create ants worker pool: %w", err)
	}
	ap.pool = p

	ap.admissionMu.Lock()
	ap.accepting = true
	ap.admissionMu.Unlock()

	if ap.config.Policy == Queue {
		ap.queue = make(chan queuedTask, ap.config.QueueCapacity)
		ap.slots = make(chan struct{}, ap.config.QueueCapacity)
		go ap.dispatch()
	} else {
		close(ap.dispatched)
	}
	return nil
}

// Submit accepts a task without waiting for it to complete.
func (ap *AntsWorkerPool) Submit(ctx context.Context, task WorkerTask) error {
	if task == nil {
		return fmt.Errorf("%w: task is nil", ErrInvalid)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ap.beginSubmit(); err != nil {
		return err
	}
	defer ap.endSubmit()

	record := queuedTask{ctx: ctx, task: task}
	if ap.config.Policy == Reject {
		if err := ap.pool.Submit(func() { ap.run(record) }); err != nil {
			return ap.mapSubmitError(err)
		}
		ap.accepted.Add(1)
		return nil
	}

	select {
	case ap.slots <- struct{}{}:
	default:
		ap.rejected.Add(1)
		return ErrOverloaded
	}
	select {
	case ap.queue <- record:
		ap.queued.Add(1)
		ap.accepted.Add(1)
		return nil
	default:
		// slots and queue have the same capacity; this only protects future
		// implementation changes from leaking an admission token.
		<-ap.slots
		ap.rejected.Add(1)
		return ErrOverloaded
	}
}

func (ap *AntsWorkerPool) beginSubmit() error {
	ap.admissionMu.Lock()
	defer ap.admissionMu.Unlock()
	if !ap.accepting {
		switch ap.state.Load() {
		case stateNew:
			return ErrNotRunning
		default:
			return ErrClosed
		}
	}
	ap.producers++
	return nil
}

func (ap *AntsWorkerPool) endSubmit() {
	ap.admissionMu.Lock()
	ap.producers--
	if ap.producers == 0 && ap.producersDone != nil {
		close(ap.producersDone)
		ap.producersDone = nil
	}
	ap.admissionMu.Unlock()
}

func (ap *AntsWorkerPool) dispatch() {
	defer close(ap.dispatched)
	for record := range ap.queue {
		if record.ctx.Err() != nil {
			ap.dropQueuedTask()
			continue
		}
	queued:
		for {
			err := ap.pool.Submit(func() { ap.run(record) })
			if err == nil {
				ap.releaseQueueSlot()
				break
			}
			if !errors.Is(err, ants.ErrPoolOverload) {
				ap.dropQueuedTask()
				break
			}

			timer := time.NewTimer(time.Millisecond)
			select {
			case <-timer.C:
			case <-record.ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				ap.dropQueuedTask()
				break queued
			case <-ap.taskCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				ap.dropQueuedTask()
				ap.cancelQueued()
				return
			}
		}
	}
}

func (ap *AntsWorkerPool) cancelQueued() {
	for range ap.queue {
		ap.dropQueuedTask()
	}
}

func (ap *AntsWorkerPool) dropQueuedTask() {
	ap.releaseQueueSlot()
	ap.canceled.Add(1)
}

func (ap *AntsWorkerPool) releaseQueueSlot() {
	ap.queued.Add(-1)
	<-ap.slots
}

func (ap *AntsWorkerPool) run(record queuedTask) {
	ctx, cancel := context.WithCancel(record.ctx)
	stop := context.AfterFunc(ap.taskCtx, cancel)
	ap.running.Add(1)
	defer func() {
		ap.running.Add(-1)
		stop()
		cancel()
		ap.completed.Add(1)
		if value := recover(); value != nil {
			ap.panicked.Add(1)
			ap.logger.Error(ctx, "worker pool task panicked",
				zap.Any("panic", value),
				zap.ByteString("stack", debug.Stack()),
			)
		}
	}()

	if err := ctx.Err(); err != nil {
		ap.canceled.Add(1)
		return
	}
	if err := record.task(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			ap.canceled.Add(1)
		} else {
			ap.failed.Add(1)
		}
		ap.logger.Error(ctx, "worker pool task failed", zap.Error(err))
	}
}

func (ap *AntsWorkerPool) mapSubmitError(err error) error {
	switch {
	case errors.Is(err, ants.ErrPoolOverload):
		ap.rejected.Add(1)
		return ErrOverloaded
	case errors.Is(err, ants.ErrPoolClosed):
		return ErrClosed
	default:
		return fmt.Errorf("submit worker task: %w", err)
	}
}

// Close stops admission, drains accepted tasks, and honors the shutdown budget.
func (ap *AntsWorkerPool) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ap.lifecycleMu.Lock()
	if ap.state.Load() == stateClosed {
		ap.lifecycleMu.Unlock()
		select {
		case <-ap.closeDone:
			ap.closeMu.Lock()
			defer ap.closeMu.Unlock()
			return ap.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ap.closeOnce.Do(func() {
		if ap.state.CompareAndSwap(stateNew, stateClosed) {
			close(ap.closeDone)
			return
		}
		go ap.shutdown(ctx)
	})
	ap.lifecycleMu.Unlock()

	select {
	case <-ap.closeDone:
		ap.closeMu.Lock()
		defer ap.closeMu.Unlock()
		return ap.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ap *AntsWorkerPool) shutdown(ctx context.Context) {
	ap.state.Store(stateClosing)

	ap.admissionMu.Lock()
	ap.accepting = false
	var producersDone <-chan struct{}
	if ap.producers > 0 {
		ap.producersDone = make(chan struct{})
		producersDone = ap.producersDone
	}
	ap.admissionMu.Unlock()

	if producersDone != nil {
		<-producersDone
	}

	if ap.config.Policy == Queue {
		close(ap.queue)
		select {
		case <-ap.dispatched:
		case <-ctx.Done():
			ap.cancelTask()
		}
	}

	if err := ctx.Err(); err != nil {
		ap.cancelTask()
	}
	err := ap.pool.ReleaseContext(ctx)
	ap.cancelTask()
	if errors.Is(err, ants.ErrPoolClosed) {
		err = nil
	}
	if err != nil {
		err = fmt.Errorf("close worker pool: %w", err)
	}

	ap.closeMu.Lock()
	ap.closeErr = err
	ap.closeMu.Unlock()
	ap.state.Store(stateClosed)
	close(ap.closeDone)
}

// Stats returns observable admission and execution counters.
func (ap *AntsWorkerPool) Stats() Stats {
	return Stats{
		Capacity:      ap.config.Capacity,
		QueueCapacity: ap.config.QueueCapacity,
		Running:       int(ap.running.Load()),
		Queued:        ap.queued.Load(),
		Accepted:      ap.accepted.Load(),
		Completed:     ap.completed.Load(),
		Rejected:      ap.rejected.Load(),
		Canceled:      ap.canceled.Load(),
		Failed:        ap.failed.Load(),
		Panicked:      ap.panicked.Load(),
	}
}

type antsLogAdapter struct{ logger modularlog.Logger }

func (adapter antsLogAdapter) Printf(format string, values ...any) {
	adapter.logger.Error(context.Background(), fmt.Sprintf(format, values...))
}
