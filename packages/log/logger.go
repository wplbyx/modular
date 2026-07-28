// Package log provides the process logger used by modular modules.
package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cyub/ringbuffer"
	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/metadata"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultQueueCapacity = 8192
	defaultErrorTimeout  = 50 * time.Millisecond
	defaultFlushTimeout  = 5 * time.Second
)

const (
	stateOpen int32 = iota
	stateClosing
	stateClosed
)

// Field is the structured field type accepted by Logger.
type Field = zap.Field

// Logger is the logging interface shared by all modular modules.
// Context is mandatory so request metadata and trace identity are never hidden.
type Logger interface {
	Debug(context.Context, string, ...Field)
	Info(context.Context, string, ...Field)
	Warn(context.Context, string, ...Field)
	Error(context.Context, string, ...Field)
	With(...Field) Logger
	Named(string) Logger
}

// Stats is a point-in-time dispatcher snapshot.
type Stats struct {
	Accepted      uint64
	DroppedDebug  uint64
	DroppedInfo   uint64
	DroppedWarn   uint64
	ErrorFallback uint64
	Capacity      int
	Queued        int
	HighWatermark int64
}

// LoggerManagerOption configures the process logger before it starts.
type LoggerManagerOption func(*LoggerManager)

// LoggerManager owns output sinks and the optional asynchronous dispatcher.
type LoggerManager struct {
	config  *configitem.Logging
	level   zapcore.Level
	cores   []zapcore.Core
	closers []io.Closer
	sinks   *sinkSet
	logger  Logger

	async        bool
	queue        *ringbuffer.MpscRingBuffer
	wake         chan struct{}
	space        chan struct{}
	done         chan struct{}
	errorTimeout time.Duration
	flushTimeout time.Duration
	state        atomic.Int32
	producers    atomic.Int64
	finishOnce   sync.Once
	closeMu      sync.Mutex
	closeErr     error
	sinkID       atomic.Uint64

	accepted      atomic.Uint64
	droppedDebug  atomic.Uint64
	droppedInfo   atomic.Uint64
	droppedWarn   atomic.Uint64
	errorFallback atomic.Uint64
	highWatermark atomic.Int64

	fallback zapcore.Core
}

type logRecord struct {
	entry   zapcore.Entry
	fields  []zap.Field
	barrier chan error
}

type logger struct {
	manager *LoggerManager
	name    string
	fields  []zap.Field
}

type defaultHolder struct{ logger Logger }

var defaultLogger atomic.Pointer[defaultHolder]

// NewLoggerManager creates a process logger. Callers explicitly install its
// Logger with SetDefault when package-level convenience functions are wanted.
func NewLoggerManager(cfg *configitem.Logging, options ...LoggerManagerOption) (*LoggerManager, error) {
	if cfg == nil {
		return nil, errors.New("logger config is nil")
	}
	if outputSelected(cfg.Output, "file") && cfg.File.Filename != "" {
		if err := ensureLogDir(cfg.File.Filename); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
	}

	manager := &LoggerManager{
		config:       cfg,
		level:        parseLevel(cfg.Level),
		async:        cfg.Async.Enabled,
		errorTimeout: cfg.Async.ErrorTimeout,
		flushTimeout: cfg.Async.FlushTimeout,
		wake:         make(chan struct{}, 1),
		space:        make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
	if manager.errorTimeout <= 0 {
		manager.errorTimeout = defaultErrorTimeout
	}
	if manager.flushTimeout <= 0 {
		manager.flushTimeout = defaultFlushTimeout
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	if len(manager.cores) == 0 {
		return nil, errors.New("logger config output is empty")
	}

	manager.sinks = newSinkSet(manager.cores)
	manager.fallback = zapcore.NewCore(
		zapcore.NewJSONEncoder(newEncoderConfig()),
		zapcore.Lock(zapcore.AddSync(os.Stderr)),
		zapcore.ErrorLevel,
	)
	manager.logger = &logger{manager: manager}

	if manager.async {
		capacity := cfg.Async.Capacity
		if capacity <= 0 {
			capacity = defaultQueueCapacity
		}
		manager.queue = ringbuffer.NewMpscRingBuffer(capacity)
		go manager.consume()
	} else {
		manager.state.Store(stateOpen)
	}
	return manager, nil
}

// Logger returns the process logging interface.
func (m *LoggerManager) Logger() Logger { return m.logger }

// SetDefault installs logger for package-level convenience functions and
// returns a restore function suitable for tests.
func SetDefault(value Logger) func() {
	if value == nil {
		value = nopLogger{}
	}
	previous := defaultLogger.Swap(&defaultHolder{logger: value})
	return func() { defaultLogger.Store(previous) }
}

// Default returns the installed process logger or a no-op logger.
func Default() Logger {
	if holder := defaultLogger.Load(); holder != nil && holder.logger != nil {
		return holder.logger
	}
	return nopLogger{}
}

// Sync waits until all previously accepted records have reached their sinks.
func (m *LoggerManager) Sync(ctx context.Context) error {
	ctx, cancel := m.flushContext(ctx)
	defer cancel()
	if !m.async {
		if !m.beginProducer() {
			return m.waitClosed(ctx)
		}
		defer m.endProducer()
		return m.sinks.Sync()
	}
	if m.state.Load() != stateOpen {
		return m.waitClosed(ctx)
	}
	barrier := make(chan error, 1)
	record := &logRecord{barrier: barrier}
	if err := m.enqueue(ctx, record, true); err != nil {
		return err
	}
	select {
	case err := <-barrier:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close drains accepted records and closes output resources. It is idempotent.
func (m *LoggerManager) Close(ctx context.Context) error {
	ctx, cancel := m.flushContext(ctx)
	defer cancel()
	if m.state.CompareAndSwap(stateOpen, stateClosing) {
		if m.async {
			m.signal(m.wake)
		} else if m.producers.Load() == 0 {
			m.finish()
		}
	}
	return m.waitClosed(ctx)
}

func (m *LoggerManager) waitClosed(ctx context.Context) error {
	select {
	case <-m.done:
		m.closeMu.Lock()
		defer m.closeMu.Unlock()
		return m.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns queue and degradation counters without exposing the queue.
func (m *LoggerManager) Stats() Stats {
	stats := Stats{
		Accepted:      m.accepted.Load(),
		DroppedDebug:  m.droppedDebug.Load(),
		DroppedInfo:   m.droppedInfo.Load(),
		DroppedWarn:   m.droppedWarn.Load(),
		ErrorFallback: m.errorFallback.Load(),
		HighWatermark: m.highWatermark.Load(),
	}
	if m.queue != nil {
		stats.Capacity = m.queue.Capacity()
		stats.Queued = m.queue.Length()
	}
	return stats
}

func (l *logger) Debug(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, zapcore.DebugLevel, message, fields)
}

func (l *logger) Info(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, zapcore.InfoLevel, message, fields)
}

func (l *logger) Warn(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, zapcore.WarnLevel, message, fields)
}

func (l *logger) Error(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, zapcore.ErrorLevel, message, fields)
}

func (l *logger) With(fields ...Field) Logger {
	clone := &logger{manager: l.manager, name: l.name, fields: append([]zap.Field(nil), l.fields...)}
	clone.fields = append(clone.fields, fields...)
	return clone
}

func (l *logger) Named(name string) Logger {
	clone := &logger{manager: l.manager, fields: append([]zap.Field(nil), l.fields...)}
	if l.name == "" {
		clone.name = name
	} else if name == "" {
		clone.name = l.name
	} else {
		clone.name = l.name + "." + name
	}
	return clone
}

func (l *logger) write(ctx context.Context, level zapcore.Level, message string, fields []Field) {
	if l == nil || l.manager == nil || !l.manager.level.Enabled(level) {
		return
	}
	ctx = normalizeContext(ctx)
	allFields := make([]zap.Field, 0, len(l.fields)+len(fields)+6)
	allFields = append(allFields, l.fields...)
	allFields = append(allFields, fields...)
	allFields = append(allFields, contextFields(ctx)...)
	record := &logRecord{
		entry: zapcore.Entry{
			Level:      level,
			Time:       time.Now(),
			LoggerName: l.name,
			Message:    message,
			Caller:     caller(),
		},
		fields: allFields,
	}
	if !l.manager.async {
		if !l.manager.beginProducer() {
			return
		}
		defer l.manager.endProducer()
		_ = l.manager.sinks.Write(record.entry, record.fields)
		return
	}

	wait := level >= zapcore.ErrorLevel
	if err := l.manager.enqueue(ctx, record, wait); err != nil {
		if level >= zapcore.ErrorLevel {
			l.manager.errorFallback.Add(1)
			_ = l.manager.fallback.Write(record.entry, record.fields)
			return
		}
		switch level {
		case zapcore.DebugLevel:
			l.manager.droppedDebug.Add(1)
		case zapcore.InfoLevel:
			l.manager.droppedInfo.Add(1)
		case zapcore.WarnLevel:
			l.manager.droppedWarn.Add(1)
		}
	}
}

func (m *LoggerManager) enqueue(ctx context.Context, record *logRecord, wait bool) error {
	if !m.beginProducer() {
		return errors.New("logger is closing")
	}
	defer m.endProducer()

	var timer *time.Timer
	var timeout <-chan time.Time
	if wait {
		duration := m.errorTimeout
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < duration {
			duration = time.Until(deadline)
		}
		if duration <= 0 {
			return ctx.Err()
		}
		timer = time.NewTimer(duration)
		defer timer.Stop()
		timeout = timer.C
	}

	for {
		if err := m.queue.Enqueue(record); err == nil {
			m.accepted.Add(1)
			m.observeLength()
			m.signal(m.wake)
			return nil
		} else if !errors.Is(err, ringbuffer.ErrIsFull) {
			return err
		}
		if !wait {
			return ringbuffer.ErrIsFull
		}
		select {
		case <-m.space:
		case <-timeout:
			return ringbuffer.ErrIsFull
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *LoggerManager) beginProducer() bool {
	if m.state.Load() != stateOpen {
		return false
	}
	m.producers.Add(1)
	if m.state.Load() == stateOpen {
		return true
	}
	m.endProducer()
	return false
}

func (m *LoggerManager) endProducer() {
	if m.producers.Add(-1) != 0 || m.state.Load() != stateClosing {
		return
	}
	if m.async {
		m.signal(m.wake)
		return
	}
	m.finish()
}

func (m *LoggerManager) consume() {
	for {
		value, err := m.queue.Dequeue()
		if err == nil {
			record, ok := value.(*logRecord)
			if !ok {
				m.writeInternalError("log queue contained an invalid record")
				continue
			}
			if record.barrier != nil {
				record.barrier <- m.sinks.Sync()
				close(record.barrier)
			} else if err := m.sinks.Write(record.entry, record.fields); err != nil {
				m.writeInternalError("write log sink: " + err.Error())
			}
			m.signal(m.space)
			continue
		}
		if !errors.Is(err, ringbuffer.ErrIsEmpty) {
			m.writeInternalError("dequeue log record: " + err.Error())
		}
		if m.state.Load() != stateOpen && m.producers.Load() == 0 && m.queue.Length() == 0 {
			m.finish()
			return
		}
		<-m.wake
	}
}

func (m *LoggerManager) finish() {
	m.finishOnce.Do(func() {
		var result error
		if m.sinks != nil {
			result = errors.Join(result, m.sinks.Sync())
		}
		for index := len(m.closers) - 1; index >= 0; index-- {
			result = errors.Join(result, m.closers[index].Close())
		}
		m.closeMu.Lock()
		m.closeErr = result
		m.closeMu.Unlock()
		m.state.Store(stateClosed)
		close(m.done)
	})
}

func (m *LoggerManager) observeLength() {
	length := int64(m.queue.Length())
	for {
		current := m.highWatermark.Load()
		if length <= current || m.highWatermark.CompareAndSwap(current, length) {
			return
		}
	}
}

func (m *LoggerManager) signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

func (m *LoggerManager) writeInternalError(message string) {
	_ = m.fallback.Write(zapcore.Entry{Level: zapcore.ErrorLevel, Time: time.Now(), Message: message}, nil)
}

// Debug records a structured debug event through the process default.
func Debug(ctx context.Context, message string, fields ...Field) {
	Default().Debug(ctx, message, fields...)
}

// Info records a structured informational event through the process default.
func Info(ctx context.Context, message string, fields ...Field) {
	Default().Info(ctx, message, fields...)
}

// Warn records a structured warning event through the process default.
func Warn(ctx context.Context, message string, fields ...Field) {
	Default().Warn(ctx, message, fields...)
}

// Error records a structured error event through the process default.
func Error(ctx context.Context, message string, fields ...Field) {
	Default().Error(ctx, message, fields...)
}

type nopLogger struct{}

func (nopLogger) Debug(context.Context, string, ...Field) {}
func (nopLogger) Info(context.Context, string, ...Field)  {}
func (nopLogger) Warn(context.Context, string, ...Field)  {}
func (nopLogger) Error(context.Context, string, ...Field) {}
func (nopLogger) With(...Field) Logger                    { return nopLogger{} }
func (nopLogger) Named(string) Logger                     { return nopLogger{} }

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (m *LoggerManager) flushContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = normalizeContext(ctx)
	if _, hasDeadline := ctx.Deadline(); hasDeadline || m.flushTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, m.flushTimeout)
}

func contextFields(ctx context.Context) []zap.Field {
	fields := make([]zap.Field, 0, 6)
	md := metadata.FromContext(ctx)
	if values := md.Get(metadata.RequestIDKey); len(values) > 0 {
		fields = append(fields, zap.String("request_id", values[0]))
	}
	if values := md.Get(metadata.LanguageKey); len(values) > 0 {
		fields = append(fields, zap.String("language", values[0]))
	}
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		fields = append(fields, zap.String("trace_id", span.TraceID().String()), zap.String("span_id", span.SpanID().String()))
	}
	return fields
}

func caller() zapcore.EntryCaller {
	pcs := make([]uintptr, 12)
	count := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "/packages/log.") && !strings.HasSuffix(frame.Function, "/packages/log.Debug") && !strings.HasSuffix(frame.Function, "/packages/log.Info") && !strings.HasSuffix(frame.Function, "/packages/log.Warn") && !strings.HasSuffix(frame.Function, "/packages/log.Error") {
			return zapcore.EntryCaller{Defined: true, PC: frame.PC, File: frame.File, Line: frame.Line}
		}
		if !more {
			return zapcore.EntryCaller{}
		}
	}
}

func ensureLogDir(filename string) error {
	dir := filepath.Dir(filename)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func parseLevel(level string) zapcore.Level {
	var parsed zapcore.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return zapcore.InfoLevel
	}
	return parsed
}

func outputSelected(outputs []string, expected string) bool {
	for _, output := range outputs {
		if strings.EqualFold(output, expected) {
			return true
		}
	}
	return false
}

type sinkSet struct {
	mu    sync.RWMutex
	order []string
	cores map[string]zapcore.Core
}

func newSinkSet(cores []zapcore.Core) *sinkSet {
	set := &sinkSet{cores: make(map[string]zapcore.Core, len(cores))}
	for index, core := range cores {
		name := fmt.Sprintf("sink-%d", index)
		set.order = append(set.order, name)
		set.cores[name] = core
	}
	return set
}

func (s *sinkSet) Add(name string, core zapcore.Core) func() {
	s.mu.Lock()
	if _, exists := s.cores[name]; !exists {
		s.order = append(s.order, name)
	}
	s.cores[name] = core
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.cores, name)
		s.mu.Unlock()
	}
}

func (s *sinkSet) Write(entry zapcore.Entry, fields []zap.Field) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result error
	for _, name := range s.order {
		core, ok := s.cores[name]
		if ok && core.Enabled(entry.Level) {
			result = errors.Join(result, core.Write(entry, fields))
		}
	}
	return result
}

func (s *sinkSet) Sync() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result error
	for _, name := range s.order {
		if core, ok := s.cores[name]; ok {
			result = errors.Join(result, core.Sync())
		}
	}
	return result
}
