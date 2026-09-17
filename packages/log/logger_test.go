package log

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/wplbyx/modular/packages/config/configitem"
)

func TestNewLoggerManager_ConfigOutputs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		outputs   []string
		wantCores int
		wantFiles int
		wantError string
	}{
		{name: "default", wantCores: 1},
		{name: "console", outputs: []string{"CONSOLE"}, wantCores: 1},
		{name: "file", outputs: []string{"FILE"}, wantCores: 1, wantFiles: 1},
		{name: "combined", outputs: []string{"console", "file", "telemetry"}, wantCores: 2, wantFiles: 1},
		{name: "telemetry only", outputs: []string{"telemetry"}, wantError: "bootstrap output"},
		{name: "invalid after file", outputs: []string{"file", "invalid"}, wantError: "unsupported logging output"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// 使用可 Sync 的真实文件捕获控制台输出，避免测试进程管道的 EINVAL。
			console, err := os.CreateTemp(t.TempDir(), "console")
			require.NoError(t, err)
			previous := os.Stdout
			os.Stdout = console
			t.Cleanup(func() {
				os.Stdout = previous
				require.NoError(t, console.Close())
			})
			dir := filepath.Join(t.TempDir(), "logs")
			cfg := &configitem.Logging{Level: "info", Output: tt.outputs, File: configitem.FileConfig{Filename: filepath.Join(dir, "app.log")}}
			manager, err := NewLoggerManager(cfg, nil)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.NoDirExists(t, dir)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })
			require.Len(t, manager.cores, tt.wantCores)
			require.Len(t, manager.closers, tt.wantFiles)
			manager.Logger().Info(t.Context(), "configured output")
			require.NoError(t, manager.Close(t.Context()))
			if tt.wantFiles > 0 {
				files, err := filepath.Glob(filepath.Join(dir, "app-*.log"))
				require.NoError(t, err)
				require.Len(t, files, 1)
				data, err := os.ReadFile(files[0])
				require.NoError(t, err)
				require.Contains(t, string(data), "configured output")
				writer := manager.closers[0].(*DailyRotate)
				select {
				case <-writer.stopChan:
				default:
					t.Fatal("file rotation was not stopped")
				}
			} else {
				require.NoDirExists(t, dir)
			}
		})
	}
}

func TestNewLoggerManager_ExplicitOutputsOverrideConfig(t *testing.T) {
	var buffer bytes.Buffer
	dir := filepath.Join(t.TempDir(), "unused")
	cfg := &configitem.Logging{Output: []string{"file", "invalid"}, File: configitem.FileConfig{Filename: filepath.Join(dir, "app.log")}}
	manager, err := NewLoggerManager(cfg, withBufferOutput(&buffer))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })
	require.Len(t, manager.cores, 1)
	require.NoDirExists(t, dir)
	manager.Logger().Info(t.Context(), "explicit output")
	require.Contains(t, buffer.String(), "explicit output")
}

func TestNewLoggerManager_FileOptionAndInitializationErrors(t *testing.T) {
	_, err := NewLoggerManager(nil)
	require.ErrorContains(t, err, "config is nil")
	dir := t.TempDir()
	filename := filepath.Join(dir, "nested", "app.log")
	manager, err := NewLoggerManager(&configitem.Logging{File: configitem.FileConfig{Filename: filename}}, WithOutputFiles(t.Context()))
	require.NoError(t, err)
	require.DirExists(t, filepath.Dir(filename))
	require.NoError(t, manager.Close(t.Context()))

	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	_, err = NewLoggerManager(&configitem.Logging{Output: []string{"file"}, File: configitem.FileConfig{Filename: filepath.Join(blocker, "app.log")}})
	require.ErrorContains(t, err, "create log directory")
}

func TestNewLoggerManagerFailureDoesNotInstallDefault(t *testing.T) {
	restore := SetDefault(nopLogger{})
	t.Cleanup(restore)

	_, err := NewLoggerManager(&configitem.Logging{Level: "info", Output: []string{"invalid"}})
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
