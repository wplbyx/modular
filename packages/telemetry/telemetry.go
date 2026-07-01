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
	res *resource.Resource
	Tp  *trace.TracerProvider
	Mp  *metric.MeterProvider
	Lp  *log.LoggerProvider
}

func NewOpenTelemetry(ctx context.Context, cfg *config.Application, telemetry *config.Telemetry) (*OpenTelemetry, error) {
	ot := new(OpenTelemetry)
	ot.res = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(cfg.Name), semconv.ServiceVersion(cfg.Version))
	if err := ot.newTracerProvider(ctx, telemetry, ot.res); err != nil {
		return nil, err
	}
	if err := ot.newMetricProvider(ctx, telemetry, ot.res); err != nil {
		return nil, err
	}
	if err := ot.newLoggerProvider(ctx, telemetry, ot.res); err != nil {
		return nil, err
	}
	return ot, nil
}

// Name 实现 app.Resource 接口
func (o *OpenTelemetry) Name() string { return "telemetry" }

// Init 初始化 OTel providers。如果已通过 NewOpenTelemetry 初始化则跳过。
func (o *OpenTelemetry) Setup(ctx context.Context) error {
	if o.Tp != nil || o.Mp != nil || o.Lp != nil {
		return nil
	}
	// 如果未通过 NewOpenTelemetry 预初始化，需要调用方自行设置 res 并调用各 provider 初始化方法
	// 典型用法：先 NewOpenTelemetry(ctx, cfg, teleCfg) 完成初始化，再 WithResource(otel) 注册到 app
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
