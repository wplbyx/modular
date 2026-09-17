package log

import (
	"context"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type countingExporter struct{ count atomic.Int64 }

func (e *countingExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.count.Add(int64(len(records)))
	return nil
}
func (*countingExporter) Shutdown(context.Context) error   { return nil }
func (*countingExporter) ForceFlush(context.Context) error { return nil }

func TestLoggerManager_DetachStopsOnlyAttachedSink(t *testing.T) {
	manager, err := NewLoggerManager(&configitem.Logging{Level: "info"}, discardOutput())
	require.NoError(t, err)
	defer manager.Close(context.Background())
	exporter := &countingExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	defer provider.Shutdown(context.Background())
	detach, err := manager.AttachTelemetry("test", provider)
	require.NoError(t, err)
	manager.Logger().Info(context.Background(), "attached")
	require.NoError(t, manager.Sync(context.Background()))
	detach()
	detach()
	manager.Logger().Info(context.Background(), "detached")
	require.NoError(t, manager.Sync(context.Background()))
	require.Equal(t, int64(1), exporter.count.Load())
}

func discardOutput() LoggerManagerOption {
	return func(m *LoggerManager) {
		m.cores = append(m.cores, zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(io.Discard), m.level))
	}
}

func BenchmarkLoggerManager_Sinks(b *testing.B) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		b.Run(name, func(b *testing.B) {
			manager, err := NewLoggerManager(&configitem.Logging{Level: "info", Async: configitem.AsyncLoggingConfig{Enabled: async, Capacity: 8192}}, discardOutput())
			if err != nil {
				b.Fatal(err)
			}
			logger := manager.Logger()
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				logger.Info(ctx, "benchmark", zap.String("operation", "test"))
			}
			b.StopTimer()
			if err := manager.Close(ctx); err != nil {
				b.Fatal(err)
			}
			stats := manager.Stats()
			b.ReportMetric(float64(stats.DroppedInfo), "dropped")
		})
	}
}
