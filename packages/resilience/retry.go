package resilience

import (
	"context"
	"errors"
	"math"
	"time"

	"go.uber.org/zap"
	"modular/packages/errs"
	"modular/packages/log"
)

// RetryConfig 重试配置
type RetryConfig struct {
	Name            string
	MaxRetries      int
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
	RetryableErrors []error
}

// 默认重试配置
var DefaultRetryConfig = RetryConfig{
	Name:            "default",
	MaxRetries:      3,
	InitialInterval: 100 * time.Millisecond,
	MaxInterval:     2 * time.Second,
	Multiplier:      2.0,
}

type retryImpl struct {
	config RetryConfig
}

func NewRetry(config RetryConfig) Retry {
	if config.MaxRetries < 0 {
		config.MaxRetries = DefaultRetryConfig.MaxRetries
	}
	if config.InitialInterval <= 0 {
		config.InitialInterval = DefaultRetryConfig.InitialInterval
	}
	if config.MaxInterval <= 0 {
		config.MaxInterval = DefaultRetryConfig.MaxInterval
	}
	if config.Multiplier <= 1.0 {
		config.Multiplier = DefaultRetryConfig.Multiplier
	}
	if config.Name == "" {
		config.Name = DefaultRetryConfig.Name
	}
	return &retryImpl{config: config}
}

func (r *retryImpl) Name() string {
	return r.config.Name
}

func (r *retryImpl) Execute(ctx context.Context, fn func() error) error {
	var lastErr error
	delay := r.config.InitialInterval

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			wrappedErr := errs.New(
				errs.WithCode(500),
				errs.WithMsgf("context canceled during retry operation"),
				errs.WithCause(err),
			)
			log.Error("retry context canceled", zap.Error(wrappedErr), zap.String("retry_name", r.config.Name))
			return wrappedErr
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// 如果配置了可重试错误列表，检查错误是否可重试
		if len(r.config.RetryableErrors) > 0 && !r.isRetryableError(lastErr) {
			break
		}

		if attempt < r.config.MaxRetries {
			log.Infof("Retry '%s' attempt %d/%d for error: %v",
				r.config.Name, attempt+1, r.config.MaxRetries, lastErr)

			select {
			case <-ctx.Done():
				wrappedErr := errs.New(
					errs.WithCode(500),
					errs.WithMsgf("context canceled during retry operation"),
					errs.WithCause(ctx.Err()),
				)
				log.Error("retry context canceled", zap.Error(wrappedErr), zap.String("retry_name", r.config.Name))
				return wrappedErr
			case <-time.After(delay):
			}

			// 计算下一次延迟（指数退避）
			delay = time.Duration(math.Min(
				float64(delay)*r.config.Multiplier,
				float64(r.config.MaxInterval),
			))
		}
	}

	finalErr := errs.New(
		errs.WithCode(500),
		errs.WithMsgf("all retry attempts failed: %v", lastErr),
		errs.WithCause(lastErr),
	)
	log.Error("retry exhausted",
		zap.Error(finalErr),
		zap.String("retry_name", r.config.Name),
		zap.Int("max_retries", r.config.MaxRetries),
	)
	return finalErr
}

func (r *retryImpl) isRetryableError(err error) bool {
	for _, retryErr := range r.config.RetryableErrors {
		if errors.Is(err, retryErr) {
			return true
		}
	}
	return false
}
