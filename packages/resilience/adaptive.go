package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	"github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"

	"github.com/wplbyx/modular/packages/errs"
)

const defaultMaxOperations = 256

var (
	adaptiveLimitMessage = errs.Define("ADAPTIVE_LIMIT_EXCEEDED", errs.Template("too many requests"))
	adaptiveOpenMessage  = errs.Define("ADAPTIVE_CIRCUIT_OPEN", errs.Template("service is temporarily unavailable"))
)

// DoneFunc reports the result of an admitted operation exactly once.
type DoneFunc func(error)

// Protection is the transport-facing adaptive admission contract.
// Implementations must be concurrency-safe.
type Protection interface {
	Allow(context.Context, string) (DoneFunc, error)
}

// AdaptiveConfig configures the Aegis BBR limiter and SRE circuit breakers.
// Zero values are replaced with conservative defaults.
type AdaptiveConfig struct {
	Window          time.Duration
	Buckets         int
	CPUThreshold    int64
	CPUQuota        float64
	MinimumRequests int64
	SuccessRatio    float64
	MaxOperations   int
}

// DefaultAdaptiveConfig returns the built-in transport protection profile.
func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		Window:          10 * time.Second,
		Buckets:         40,
		CPUThreshold:    900,
		MinimumRequests: 100,
		SuccessRatio:    0.8,
		MaxOperations:   defaultMaxOperations,
	}
}

// AdaptiveProtection combines one process-level BBR limiter with bounded,
// operation-level SRE circuit breakers. The algorithms remain owned by Aegis.
type AdaptiveProtection struct {
	limiter ratelimit.Limiter
	config  AdaptiveConfig

	mu       sync.RWMutex
	breakers map[string]circuitbreaker.CircuitBreaker
	fallback circuitbreaker.CircuitBreaker
}

// NewAdaptiveProtection creates the default transport protection chain.
func NewAdaptiveProtection(config AdaptiveConfig) *AdaptiveProtection {
	config = normalizeAdaptiveConfig(config)
	limiterOptions := []bbr.Option{
		bbr.WithWindow(config.Window),
		bbr.WithBucket(config.Buckets),
		bbr.WithCPUThreshold(config.CPUThreshold),
	}
	if config.CPUQuota > 0 {
		limiterOptions = append(limiterOptions, bbr.WithCPUQuota(config.CPUQuota))
	}
	return &AdaptiveProtection{
		limiter:  bbr.NewLimiter(limiterOptions...),
		config:   config,
		breakers: make(map[string]circuitbreaker.CircuitBreaker),
		fallback: newSREBreaker(config),
	}
}

// Allow checks the operation breaker first and the process limiter second.
// Rejections use stable, language-neutral errs reasons for boundary mapping.
func (p *AdaptiveProtection) Allow(ctx context.Context, operation string) (DoneFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	breaker := p.breaker(operation)
	if err := breaker.Allow(); err != nil {
		return nil, errs.ServiceUnavailable(
			adaptiveOpenMessage,
			errs.WithCause(err),
			errs.WithField("operation", normalizeOperation(operation)),
		)
	}
	rateDone, err := p.limiter.Allow()
	if err != nil {
		return nil, errs.TooManyRequests(
			adaptiveLimitMessage,
			errs.WithCause(err),
			errs.WithField("operation", normalizeOperation(operation)),
		)
	}

	var once sync.Once
	return func(result error) {
		once.Do(func() {
			rateDone(ratelimit.DoneInfo{Err: result})
			if result == nil {
				breaker.MarkSuccess()
				return
			}
			breaker.MarkFailed()
		})
	}, nil
}

// Stats exposes only the BBR snapshot needed for operations diagnostics.
func (p *AdaptiveProtection) Stats() bbr.Stat {
	if limiter, ok := p.limiter.(*bbr.BBR); ok {
		return limiter.Stat()
	}
	return bbr.Stat{}
}

func (p *AdaptiveProtection) breaker(operation string) circuitbreaker.CircuitBreaker {
	operation = normalizeOperation(operation)
	p.mu.RLock()
	breaker, ok := p.breakers[operation]
	p.mu.RUnlock()
	if ok {
		return breaker
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if breaker, ok = p.breakers[operation]; ok {
		return breaker
	}
	if len(p.breakers) >= p.config.MaxOperations {
		return p.fallback
	}
	breaker = newSREBreaker(p.config)
	p.breakers[operation] = breaker
	return breaker
}

func newSREBreaker(config AdaptiveConfig) circuitbreaker.CircuitBreaker {
	return sre.NewBreaker(
		sre.WithWindow(config.Window),
		sre.WithBucket(config.Buckets),
		sre.WithRequest(config.MinimumRequests),
		sre.WithSuccess(config.SuccessRatio),
	)
}

func normalizeAdaptiveConfig(config AdaptiveConfig) AdaptiveConfig {
	defaults := DefaultAdaptiveConfig()
	if config.Window <= 0 {
		config.Window = defaults.Window
	}
	if config.Buckets <= 0 {
		config.Buckets = defaults.Buckets
	}
	if config.CPUThreshold <= 0 {
		config.CPUThreshold = defaults.CPUThreshold
	}
	if config.MinimumRequests <= 0 {
		config.MinimumRequests = defaults.MinimumRequests
	}
	if config.SuccessRatio <= 0 || config.SuccessRatio > 1 {
		config.SuccessRatio = defaults.SuccessRatio
	}
	if config.MaxOperations <= 0 {
		config.MaxOperations = defaults.MaxOperations
	}
	return config
}

func normalizeOperation(operation string) string {
	if operation == "" {
		return "unknown"
	}
	return operation
}

// IsAdaptiveLimit reports whether err is a BBR admission rejection.
func IsAdaptiveLimit(err error) bool {
	return errors.Is(err, ratelimit.ErrLimitExceed)
}

// IsAdaptiveCircuitOpen reports whether err is an SRE breaker rejection.
func IsAdaptiveCircuitOpen(err error) bool {
	return errors.Is(err, circuitbreaker.ErrNotAllowed)
}
