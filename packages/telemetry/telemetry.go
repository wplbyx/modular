package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	apilog "go.opentelemetry.io/otel/log"
	apimetric "go.opentelemetry.io/otel/metric"
	apitrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
	"math"
	"os"
	"strings"
	"sync"
	"time"

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

var providerOwner struct {
	sync.Mutex
	owner *OpenTelemetry
}

type OpenTelemetry struct {
	mu                   sync.Mutex
	tlsConfig            *tls.Config
	headers              map[string]string
	previousTrace        apitrace.TracerProvider
	previousMetric       apimetric.MeterProvider
	previousLog          apilog.LoggerProvider
	previousPropagation  propagation.TextMapPropagator
	installedPropagation propagation.TextMapPropagator

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
	if telemetry != nil {
		copyCfg := *telemetry
		copyCfg.Headers = append([]string(nil), telemetry.Headers...)
		result.cfg = &copyCfg
	}
	if err := result.configureExporter(); err != nil {
		return nil, err
	}
	return result, nil
}

// Name 实现 app.Resource 接口
func (o *OpenTelemetry) Name() string { return "telemetry" }

// Setup 初始化 OTel providers。
func (o *OpenTelemetry) Setup(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.setup {
		return nil
	}
	if o.res == nil {
		o.res = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(o.name), semconv.ServiceVersion(o.version))
	}
	if err := o.newTracerProvider(ctx, o.cfg, o.res); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = o.closeLocked(cleanupCtx)
		cancel()
		return err
	}
	if err := o.newMetricProvider(ctx, o.cfg, o.res); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = o.closeLocked(cleanupCtx)
		cancel()
		return err
	}
	if err := o.newLoggerProvider(ctx, o.cfg, o.res); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = o.closeLocked(cleanupCtx)
		cancel()
		return err
	}
	if o.Lp != nil && o.logger != nil {
		detach, err := o.logger.AttachTelemetry(o.name, o.Lp)
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = o.closeLocked(cleanupCtx)
			cancel()
			return fmt.Errorf("attach OpenTelemetry log sink: %w", err)
		}
		o.detach = detach
	}
	providerOwner.Lock()
	if (o.Tp != nil || o.Mp != nil || o.Lp != nil) && providerOwner.owner != nil && providerOwner.owner != o {
		providerOwner.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = o.closeLocked(cleanupCtx)
		return errors.New("another telemetry resource owns the process providers")
	}
	if o.Tp != nil || o.Mp != nil || o.Lp != nil {
		providerOwner.owner = o
	}
	if o.Tp != nil {
		o.previousTrace = otel.GetTracerProvider()
		o.previousPropagation = otel.GetTextMapPropagator()
		o.installedPropagation = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
		otel.SetTracerProvider(o.Tp)
		otel.SetTextMapPropagator(o.installedPropagation)
	}
	if o.Mp != nil {
		o.previousMetric = otel.GetMeterProvider()
		otel.SetMeterProvider(o.Mp)
	}
	if o.Lp != nil {
		o.previousLog = global.GetLoggerProvider()
		global.SetLoggerProvider(o.Lp)
	}
	providerOwner.Unlock()
	o.setup = true
	return nil
}

// Close flushes and closes initialized OpenTelemetry providers.
func (o *OpenTelemetry) Close(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closeLocked(ctx)
}
func (o *OpenTelemetry) closeLocked(ctx context.Context) error {
	if o == nil {
		return nil
	}

	providerOwner.Lock()
	if providerOwner.owner == o {
		if o.previousTrace != nil && otel.GetTracerProvider() == o.Tp {
			otel.SetTracerProvider(o.previousTrace)
			otel.SetTextMapPropagator(o.previousPropagation)
		}
		if o.previousMetric != nil && otel.GetMeterProvider() == o.Mp {
			otel.SetMeterProvider(o.previousMetric)
		}
		if o.previousLog != nil && global.GetLoggerProvider() == o.Lp {
			global.SetLoggerProvider(o.previousLog)
		}
		providerOwner.owner = nil
	}
	providerOwner.Unlock()
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

	options := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(telemetry.Tracer), otlptracegrpc.WithHeaders(o.headers)}
	if o.tlsConfig != nil {
		options = append(options, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(o.tlsConfig)))
	} else {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, options...)

	if err != nil {
		return fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	o.Tp = trace.NewTracerProvider(
		trace.WithSampler(o.sampler()),
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)

	return nil
}

func (o *OpenTelemetry) newMetricProvider(ctx context.Context, telemetry *configitem.Telemetry, res *resource.Resource) error {
	if telemetry == nil || telemetry.Metric == "" {
		return nil
	}

	options := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(telemetry.Metric), otlpmetricgrpc.WithHeaders(o.headers)}
	if o.tlsConfig != nil {
		options = append(options, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(o.tlsConfig)))
	} else {
		options = append(options, otlpmetricgrpc.WithInsecure())
	}
	exporter, err := otlpmetricgrpc.New(ctx, options...)

	if err != nil {
		return fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	o.Mp = metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exporter)),
		metric.WithResource(res),
	)

	return nil
}

func (o *OpenTelemetry) newLoggerProvider(ctx context.Context, telemetry *configitem.Telemetry, res *resource.Resource) error {
	if telemetry == nil || telemetry.Logger == "" {
		return nil
	}

	options := []otlploggrpc.Option{otlploggrpc.WithEndpoint(telemetry.Logger), otlploggrpc.WithHeaders(o.headers)}
	if o.tlsConfig != nil {
		options = append(options, otlploggrpc.WithTLSCredentials(credentials.NewTLS(o.tlsConfig)))
	} else {
		options = append(options, otlploggrpc.WithInsecure())
	}
	exporter, err := otlploggrpc.New(ctx, options...)

	if err != nil {
		return fmt.Errorf("failed to create OTLP logger exporter: %w", err)
	}

	o.Lp = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)

	return nil
}

func (o *OpenTelemetry) configureExporter() error {
	cfg := o.cfg
	if cfg == nil {
		return nil
	}
	if math.IsNaN(cfg.SampleRatio) || cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return errors.New("sample ratio must be between 0 and 1")
	}
	switch cfg.Sampler {
	case "", "always", "never", "ratio", "parentbased":
	default:
		return errors.New("invalid trace sampler")
	}
	o.headers = make(map[string]string)
	for _, header := range cfg.Headers {
		key, value, ok := strings.Cut(header, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("OTLP headers must use key=value")
		}
		o.headers[strings.TrimSpace(key)] = value
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return errors.New("OTLP client certificate and key must be supplied together")
	}
	if !cfg.UseTLS {
		if cfg.CAFile != "" || cfg.CertFile != "" || cfg.ServerName != "" {
			return errors.New("OTLP TLS settings require UseTLS")
		}
		return nil
	}
	o.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("read OTLP CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("invalid OTLP CA certificate")
		}
		o.tlsConfig.RootCAs = pool
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return fmt.Errorf("load OTLP client certificate: %w", err)
		}
		o.tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return nil
}
func (o *OpenTelemetry) sampler() trace.Sampler {
	switch o.cfg.Sampler {
	case "never":
		return trace.NeverSample()
	case "ratio":
		return trace.TraceIDRatioBased(o.cfg.SampleRatio)
	case "parentbased":
		return trace.ParentBased(trace.TraceIDRatioBased(o.cfg.SampleRatio))
	default:
		return trace.AlwaysSample()
	}
}
