// Package eventbus provides an in-process, ordered, at-most-once event bus.
package eventbus

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cyub/ringbuffer"
	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	modularlog "github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/metadata"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	ErrNotRunning = errors.New("event bus is not running")
	ErrClosed     = errors.New("event bus is closed")
	ErrFull       = errors.New("event bus queue is full")
	ErrInvalid    = errors.New("event bus argument is invalid")
)

const (
	stateNew int32 = iota
	stateRunning
	stateClosing
	stateClosed
)

// Config configures one process-local bus.
type Config = configitem.EventBus

// Event is the immutable envelope visible to handlers. Payload ownership
// remains with the publisher and must not be mutated after Publish returns.
type Event struct {
	Name       string
	Key        string
	Payload    any
	OccurredAt time.Time
}

// Handler consumes one local event.
type Handler func(context.Context, Event) error

// Stats is a point-in-time bus snapshot.
type Stats struct {
	Published     uint64
	Handled       uint64
	Dropped       uint64
	HandlerErrors uint64
	HandlerPanics uint64
	Capacity      int
	Queued        int
	HighWatermark int64
}

type queuedEvent struct {
	event       Event
	metadata    metadata.Metadata
	spanContext trace.SpanContext
}

// Bus is a core.Resource. Setup starts one consumer and Close drains it.
type Bus struct {
	config Config
	logger modularlog.Logger
	queue  *ringbuffer.MpscRingBuffer
	wake   chan struct{}
	done   chan struct{}

	mu        sync.RWMutex
	handlers  map[string][]Handler
	state     atomic.Int32
	producers atomic.Int64

	published     atomic.Uint64
	handled       atomic.Uint64
	dropped       atomic.Uint64
	handlerErrors atomic.Uint64
	handlerPanics atomic.Uint64
	highWatermark atomic.Int64
}

// New creates an EventBus without starting its consumer.
func New(config Config, logger modularlog.Logger) (*Bus, error) {
	if config.Name == "" {
		config.Name = "eventbus"
	}
	if config.Capacity <= 0 {
		return nil, fmt.Errorf("%w: capacity must be positive", ErrInvalid)
	}
	if logger == nil {
		return nil, fmt.Errorf("%w: logger is nil", ErrInvalid)
	}
	return &Bus{
		config:   config,
		logger:   logger.Named(config.Name),
		queue:    ringbuffer.NewMpscRingBuffer(config.Capacity),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		handlers: make(map[string][]Handler),
	}, nil
}

var _ core.Resource = (*Bus)(nil)

// Name returns the Resource label.
func (b *Bus) Name() string { return b.config.Name }

// Subscribe registers a handler before Setup. Handlers run in registration order.
func (b *Bus) Subscribe(name string, handler Handler) error {
	if name == "" || handler == nil {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Load() != stateNew {
		return errors.New("event bus subscriptions are sealed after Setup")
	}
	b.handlers[name] = append(b.handlers[name], handler)
	return nil
}

// Setup starts the single consumer.
func (b *Bus) Setup(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if !b.state.CompareAndSwap(stateNew, stateRunning) {
		if b.state.Load() == stateClosed {
			return ErrClosed
		}
		return errors.New("event bus is already running")
	}
	go b.consume()
	return nil
}

// Publish enqueues one event without blocking when the queue is full.
func (b *Bus) Publish(ctx context.Context, event Event) error {
	if event.Name == "" {
		return fmt.Errorf("%w: event name is empty", ErrInvalid)
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	if !b.beginProducer() {
		if b.state.Load() >= stateClosing {
			return ErrClosed
		}
		return ErrNotRunning
	}
	defer b.endProducer()

	ctx = normalizeContext(ctx)
	record := &queuedEvent{
		event:       event,
		metadata:    metadata.FromContext(ctx),
		spanContext: trace.SpanContextFromContext(ctx),
	}
	if err := b.queue.Enqueue(record); err != nil {
		if errors.Is(err, ringbuffer.ErrIsFull) {
			b.dropped.Add(1)
			return ErrFull
		}
		return fmt.Errorf("enqueue event: %w", err)
	}
	b.published.Add(1)
	b.observeLength()
	b.signal()
	return nil
}

// Close rejects new events and drains all events accepted before the close.
func (b *Bus) Close(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	if b.state.CompareAndSwap(stateNew, stateClosed) {
		close(b.done)
		return nil
	}
	if b.state.CompareAndSwap(stateRunning, stateClosing) {
		b.signal()
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("event bus drain timed out with %d queued events: %w", b.queue.Length(), ctx.Err())
	}
}

// Stats returns observable queue and handler counters.
func (b *Bus) Stats() Stats {
	return Stats{
		Published:     b.published.Load(),
		Handled:       b.handled.Load(),
		Dropped:       b.dropped.Load(),
		HandlerErrors: b.handlerErrors.Load(),
		HandlerPanics: b.handlerPanics.Load(),
		Capacity:      b.queue.Capacity(),
		Queued:        b.queue.Length(),
		HighWatermark: b.highWatermark.Load(),
	}
}

func (b *Bus) beginProducer() bool {
	if b.state.Load() != stateRunning {
		return false
	}
	b.producers.Add(1)
	if b.state.Load() == stateRunning {
		return true
	}
	b.endProducer()
	return false
}

func (b *Bus) endProducer() {
	if b.producers.Add(-1) == 0 && b.state.Load() == stateClosing {
		b.signal()
	}
}

func (b *Bus) consume() {
	defer func() {
		b.state.Store(stateClosed)
		close(b.done)
	}()
	for {
		value, err := b.queue.Dequeue()
		if err == nil {
			record, ok := value.(*queuedEvent)
			if !ok {
				b.handlerErrors.Add(1)
				b.logger.Error(context.Background(), "event queue contained an invalid record")
				continue
			}
			b.dispatch(record)
			continue
		}
		if !errors.Is(err, ringbuffer.ErrIsEmpty) {
			b.logger.Error(context.Background(), "dequeue local event", zap.Error(err))
		}
		if b.state.Load() == stateClosing && b.producers.Load() == 0 && b.queue.Length() == 0 {
			return
		}
		<-b.wake
	}
}

func (b *Bus) dispatch(record *queuedEvent) {
	ctx := metadata.NewContext(context.Background(), record.metadata)
	if record.spanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, record.spanContext)
	}
	b.mu.RLock()
	handlers := append([]Handler(nil), b.handlers[record.event.Name]...)
	b.mu.RUnlock()
	for index, handler := range handlers {
		if err := b.callHandler(ctx, handler, record.event); err != nil {
			b.handlerErrors.Add(1)
			b.logger.Error(ctx, "local event handler failed",
				zap.String("event", record.event.Name),
				zap.Int("handler_index", index),
				zap.Error(err),
			)
		}
	}
	b.handled.Add(1)
}

func (b *Bus) callHandler(ctx context.Context, handler Handler, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			b.handlerPanics.Add(1)
			err = fmt.Errorf("handler panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return handler(ctx, event)
}

func (b *Bus) observeLength() {
	length := int64(b.queue.Length())
	for {
		current := b.highWatermark.Load()
		if length <= current || b.highWatermark.CompareAndSwap(current, length) {
			return
		}
	}
}

func (b *Bus) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
