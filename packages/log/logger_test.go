package log

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wplbyx/modular/packages/config/configitem"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewLoggerManagerFailureDoesNotInstallDefault(t *testing.T) {
	restore := SetDefault(nopLogger{})
	t.Cleanup(restore)

	_, err := NewLoggerManager(&configitem.Logging{Level: "info"})
	if err == nil {
		t.Fatal("NewLoggerManager() error = nil")
	}
	assertNoPanic(t, func() {
		ctx := context.Background()
		Debug(ctx, "debug")
		Info(ctx, "info")
		Warn(ctx, "warn")
		Error(ctx, "error")
	})
}

func TestLoggerManagerAsyncFlushAndDefault(t *testing.T) {
	var buffer bytes.Buffer
	manager, err := NewLoggerManager(&configitem.Logging{
		Level: "info",
		Async: configitem.AsyncLoggingConfig{Enabled: true, Capacity: 2},
	}, withBufferOutput(&buffer))
	if err != nil {
		t.Fatal(err)
	}
	restore := SetDefault(manager.Logger())
	t.Cleanup(restore)

	Info(context.Background(), "global logger works", zap.String("component", "test"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buffer.Bytes(), []byte("global logger works")) {
		t.Fatalf("log output = %q, stats = %+v, want message", buffer.String(), manager.Stats())
	}
}

func TestLoggerManagerDropsLowSeverityWhenQueueIsFull(t *testing.T) {
	manager, err := NewLoggerManager(&configitem.Logging{
		Level: "debug",
		Async: configitem.AsyncLoggingConfig{Enabled: true, Capacity: 1},
	}, withBlockingOutput())
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		manager.Logger().Debug(context.Background(), "queued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.Stats().DroppedDebug == 0 {
		t.Fatal("expected debug records to be dropped")
	}
}

func TestLoggerManagerCloseWaitsForSynchronousWrite(t *testing.T) {
	writer := &gateWriter{entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewLoggerManager(
		&configitem.Logging{Level: "info"},
		withGateOutput(writer),
	)
	if err != nil {
		t.Fatal(err)
	}

	writeDone := make(chan struct{})
	go func() {
		manager.Logger().Info(context.Background(), "blocking write")
		close(writeDone)
	}()
	<-writer.entered

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close(ctx) }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the active write completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(writer.release)
	<-writeDone
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoggerManagerFallsBackWhenErrorQueueStaysFull(t *testing.T) {
	writer := &gateWriter{entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewLoggerManager(
		&configitem.Logging{
			Level: "info",
			Async: configitem.AsyncLoggingConfig{
				Enabled:      true,
				Capacity:     1,
				ErrorTimeout: 10 * time.Millisecond,
			},
		},
		withGateOutput(writer),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager.fallback = zap.NewNop().Core()

	manager.Logger().Info(context.Background(), "active write")
	<-writer.entered
	manager.Logger().Info(context.Background(), "queued write")
	manager.Logger().Error(context.Background(), "fallback write")
	if manager.Stats().ErrorFallback != 1 {
		t.Fatalf("error fallback count = %d", manager.Stats().ErrorFallback)
	}

	close(writer.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func withBufferOutput(buffer *bytes.Buffer) LoggerManagerOption {
	return func(manager *LoggerManager) {
		encoderConfig := zap.NewProductionEncoderConfig()
		core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(buffer), manager.level)
		manager.cores = append(manager.cores, core)
	}
}

func withBlockingOutput() LoggerManagerOption {
	return func(manager *LoggerManager) {
		core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&slowWriter{}), manager.level)
		manager.cores = append(manager.cores, core)
	}
}

func withGateOutput(writer *gateWriter) LoggerManagerOption {
	return func(manager *LoggerManager) {
		core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(writer), manager.level)
		manager.cores = append(manager.cores, core)
	}
}

type slowWriter struct{}

func (*slowWriter) Write(value []byte) (int, error) {
	time.Sleep(time.Millisecond)
	return len(value), nil
}

type gateWriter struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (w *gateWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(value), nil
}

func assertNoPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	fn()
}
