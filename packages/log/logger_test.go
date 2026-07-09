package log

import (
	"bytes"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/wplbyx/modular/packages/config"
)

func TestNewLoggerManagerFailureDoesNotPoisonGlobalLogger(t *testing.T) {
	lm = nil
	t.Cleanup(func() { lm = nil })

	_, err := NewLoggerManager(&config.Logging{Level: "info"})
	if err == nil {
		t.Fatal("NewLoggerManager() error = nil")
	}

	assertNoPanic(t, func() {
		Debug("debug")
		Info("info")
		Warn("warn")
		Error("error")
		Debugf("%s", "debug")
		Infof("%s", "info")
		Warnf("%s", "warn")
		Errorf("%s", "error")
	})
}

func TestNewLoggerManagerSuccessSetsGlobalLogger(t *testing.T) {
	lm = nil
	t.Cleanup(func() { lm = nil })

	var buf bytes.Buffer
	manager, err := NewLoggerManager(&config.Logging{Level: "info"}, withBufferOutput(&buf))
	if err != nil {
		t.Fatalf("NewLoggerManager() error = %v", err)
	}
	defer manager.Close()

	Info("global logger works")

	if !bytes.Contains(buf.Bytes(), []byte("global logger works")) {
		t.Fatalf("log output = %q, want message", buf.String())
	}
}

func withBufferOutput(buf *bytes.Buffer) LoggerManagerOption {
	return func(manager *LoggerManager) {
		encoderCfg := zap.NewProductionEncoderConfig()
		core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(buf), manager.level)
		manager.cores = append(manager.cores, core)
	}
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
