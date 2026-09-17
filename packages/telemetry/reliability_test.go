package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	"go.opentelemetry.io/otel"
)

func TestTelemetry_CloseRestoresPreviousProvider(t *testing.T) {
	previous := otel.GetTracerProvider()
	o, err := NewOpenTelemetry(context.Background(), "test", "v1", &configitem.Telemetry{Tracer: "localhost:4317"})
	require.NoError(t, err)
	require.NoError(t, o.Setup(context.Background()))
	require.NotSame(t, previous, otel.GetTracerProvider())
	require.NoError(t, o.Close(context.Background()))
	require.Same(t, previous, otel.GetTracerProvider())
}

func TestTelemetry_RejectsInvalidTLSAndSamplingConfiguration(t *testing.T) {
	for _, cfg := range []*configitem.Telemetry{
		{UseTLS: true, CAFile: "missing-ca.pem"},
		{CertFile: "client.pem"},
		{Sampler: "unknown"},
		{Sampler: "ratio", SampleRatio: 2},
		{Headers: []string{"invalid"}},
	} {
		_, err := NewOpenTelemetry(context.Background(), "test", "v1", cfg)
		require.Error(t, err)
	}
}
