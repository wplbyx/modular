package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"

	"modular/packages/config"
)

type OpenTelemetry struct {
	Res *resource.Resource
	Tp  *trace.TracerProvider
	Mp  *metric.MeterProvider
	Lp  *log.LoggerProvider
}

func NewOpenTelemetry(ctx context.Context, cfg *config.Application, telemetry *config.Telemetry) (*OpenTelemetry, error) {
	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(cfg.Name), semconv.ServiceVersion(cfg.Version))
	ot := new(OpenTelemetry)
	ot.Res = res
	if err := ot.newTracerProvider(ctx, telemetry, res); err != nil {
		return nil, err
	}
	if err := ot.newMetricProvider(ctx, telemetry, res); err != nil {
		return nil, err
	}
	if err := ot.newLoggerProvider(ctx, telemetry, res); err != nil {
		return nil, err
	}
	return ot, nil
}

// Shutdown flushes and closes initialized OpenTelemetry providers.
func (o *OpenTelemetry) Shutdown(ctx context.Context) error {
	if o == nil {
		return nil
	}

	var joined error
	if o.Lp != nil {
		joined = errors.Join(joined, o.Lp.Shutdown(ctx))
	}
	if o.Mp != nil {
		joined = errors.Join(joined, o.Mp.Shutdown(ctx))
	}
	if o.Tp != nil {
		joined = errors.Join(joined, o.Tp.Shutdown(ctx))
	}
	return joined
}

func (o *OpenTelemetry) newTracerProvider(ctx context.Context, telemetry *config.Telemetry, res *resource.Resource) error {
	if telemetry == nil || telemetry.Tracer == "" {
		return nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(telemetry.Tracer),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	o.Tp = trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)

	otel.SetTracerProvider(o.Tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return nil
}

func (o *OpenTelemetry) newMetricProvider(ctx context.Context, telemetry *config.Telemetry, res *resource.Resource) error {
	if telemetry == nil || telemetry.Metric == "" {
		return nil
	}

	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithEndpoint(telemetry.Metric),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	o.Mp = metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exporter)),
		metric.WithResource(res),
	)

	otel.SetMeterProvider(o.Mp)
	return nil
}

func (o *OpenTelemetry) newLoggerProvider(ctx context.Context, telemetry *config.Telemetry, res *resource.Resource) error {
	if telemetry == nil || telemetry.Logger == "" {
		return nil
	}

	exporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithEndpoint(telemetry.Logger),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP logger exporter: %w", err)
	}

	o.Lp = log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(res),
	)

	global.SetLoggerProvider(o.Lp)
	return nil
}
