package telemetry

import (
	"context"
	"testing"

	"modular/packages/config"
)

func TestNewOpenTelemetryDefersProviderInitializationToSetup(t *testing.T) {
	ot, err := NewOpenTelemetry(context.Background(), "orders", "v1", &config.Telemetry{Tracer: "localhost:4317"})
	if err != nil {
		t.Fatalf("NewOpenTelemetry() error = %v", err)
	}
	if ot.Tp != nil || ot.Mp != nil || ot.Lp != nil {
		t.Fatalf("providers initialized before Setup: tp=%v mp=%v lp=%v", ot.Tp, ot.Mp, ot.Lp)
	}

	if err := ot.Setup(context.Background()); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if ot.Tp == nil {
		t.Fatal("trace provider not initialized by Setup")
	}
	if err := ot.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenTelemetrySetupIsIdempotent(t *testing.T) {
	ot, err := NewOpenTelemetry(context.Background(), "orders", "v1", &config.Telemetry{})
	if err != nil {
		t.Fatalf("NewOpenTelemetry() error = %v", err)
	}
	if err := ot.Setup(context.Background()); err != nil {
		t.Fatalf("first Setup() error = %v", err)
	}
	if err := ot.Setup(context.Background()); err != nil {
		t.Fatalf("second Setup() error = %v", err)
	}
}
