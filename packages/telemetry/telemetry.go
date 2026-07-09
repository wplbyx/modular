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

	"github.com/wplbyx/modular/packages/config"
)

type OpenTelemetry struct {
	name    string
	version string
	cfg     *config.Telemetry
	setup   bool
	res     *resource.Resource
	Tp      *trace.TracerProvider
	Mp      *metric.MeterProvider
	Lp      *log.LoggerProvider
}

func NewOpenTelemetry(ctx context.Context, name, version string, telemetry *config.Telemetry) (*OpenTelemetry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &OpenTelemetry{
		name:    name,
		version: version,
		cfg:     telemetry,
		res:     resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(name), semconv.ServiceVersion(version)),
	}, nil
}

// Name 实现 app.Resource 接口
func (o *OpenTelemetry) Name() string { return "telemetry" }

// Setup 初始化 OTel providers。
func (o *OpenTelemetry) Setup(ctx context.Context) error {
	if o == nil || o.setup {
		return nil
	}
	if o.res == nil {
		o.res = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(o.name), semconv.ServiceVersion(o.version))
	}
	if err := o.newTracerProvider(ctx, o.cfg, o.res); err != nil {
		_ = o.Close(ctx)
		return err
	}
	if err := o.newMetricProvider(ctx, o.cfg, o.res); err != nil {
		_ = o.Close(ctx)
		return err
	}
	if err := o.newLoggerProvider(ctx, o.cfg, o.res); err != nil {
		_ = o.Close(ctx)
		return err
	}
	o.setup = true
	return nil
}

// Close flushes and closes initialized OpenTelemetry providers.
func (o *OpenTelemetry) Close(ctx context.Context) error {
	if o == nil {
		return nil
	}

	var joined error
	if o.Lp != nil {
		joined = errors.Join(joined, o.Lp.Shutdown(ctx))
		o.Lp = nil
	}
	if o.Mp != nil {
		joined = errors.Join(joined, o.Mp.Shutdown(ctx))
		o.Mp = nil
	}
	if o.Tp != nil {
		joined = errors.Join(joined, o.Tp.Shutdown(ctx))
		o.Tp = nil
	}
	o.setup = false
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
