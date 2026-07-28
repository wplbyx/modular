package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/wplbyx/modular/packages/config/configitem"
	modularlog "github.com/wplbyx/modular/packages/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
)

type OpenTelemetry struct {
	name    string
	version string
	cfg     *configitem.Telemetry
	setup   bool
	res     *resource.Resource
	Tp      *trace.TracerProvider
	Mp      *metric.MeterProvider
	Lp      *sdklog.LoggerProvider
	logger  *modularlog.LoggerManager
	detach  func()
}

// Option configures an OpenTelemetry resource before its lifecycle starts.
type Option func(*OpenTelemetry)

// WithLoggerManager attaches the OTLP log sink only after telemetry setup.
func WithLoggerManager(manager *modularlog.LoggerManager) Option {
	return func(telemetry *OpenTelemetry) { telemetry.logger = manager }
}

func NewOpenTelemetry(ctx context.Context, name, version string, telemetry *configitem.Telemetry, options ...Option) (*OpenTelemetry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &OpenTelemetry{
		name:    name,
		version: version,
		cfg:     telemetry,
		res:     resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(name), semconv.ServiceVersion(version)),
	}
	for _, option := range options {
		if option != nil {
			option(result)
		}
	}
	return result, nil
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
	if o.Lp != nil && o.logger != nil {
		detach, err := o.logger.AttachTelemetry(o.name, o.Lp)
		if err != nil {
			_ = o.Close(ctx)
			return fmt.Errorf("attach OpenTelemetry log sink: %w", err)
		}
		o.detach = detach
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
	if o.detach != nil {
		if o.logger != nil {
			joined = errors.Join(joined, o.logger.Sync(ctx))
		}
		o.detach()
		o.detach = nil
	}
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

func (o *OpenTelemetry) newTracerProvider(ctx context.Context, telemetry *configitem.Telemetry, res *resource.Resource) error {
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

func (o *OpenTelemetry) newMetricProvider(ctx context.Context, telemetry *configitem.Telemetry, res *resource.Resource) error {
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

func (o *OpenTelemetry) newLoggerProvider(ctx context.Context, telemetry *configitem.Telemetry, res *resource.Resource) error {
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

	o.Lp = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)

	global.SetLoggerProvider(o.Lp)
	return nil
}
