package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultIdempotentPrefix = "lock:idempotent"
	defaultIdempotentTTL    = 30 * time.Second

	renewIdempotentLockScript = `
local val = redis.call("GET", KEYS[1])
if val == ARGV[1] then
	redis.call("PEXPIRE", KEYS[1], ARGV[2])
	return 1
end
return 0
`

	releaseIdempotentLockScript = `
local val = redis.call("GET", KEYS[1])
if val == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`
)

// IdempotentLock runs a callback while holding a Redis ownership lock.
type IdempotentLock struct {
	client goredis.UniversalClient
	prefix string
	ttl    time.Duration
}

type IdempotentOption func(*IdempotentLock)

func NewIdempotentLock(client goredis.UniversalClient, opts ...IdempotentOption) (*IdempotentLock, error) {
	if client == nil {
		return nil, errors.New("redis client is nil")
	}

	lock := &IdempotentLock{
		client: client,
		prefix: defaultIdempotentPrefix,
		ttl:    defaultIdempotentTTL,
	}
	for _, opt := range opts {
		opt(lock)
	}
	if lock.ttl <= 0 {
		return nil, errors.New("idempotent lock ttl must be positive")
	}
	if lock.prefix == "" {
		return nil, errors.New("idempotent lock prefix is empty")
	}
	return lock, nil
}

func WithIdempotentPrefix(prefix string) IdempotentOption {
	return func(lock *IdempotentLock) {
		lock.prefix = prefix
	}
}

func WithIdempotentTTL(ttl time.Duration) IdempotentOption {
	return func(lock *IdempotentLock) {
		lock.ttl = ttl
	}
}

// Run executes fn while the lock is owned. fn receives a context that is
// cancelled if lock renewal fails, plus a function for explicit renew checks.
func (l *IdempotentLock) Run(ctx context.Context, key string, fn func(context.Context, func(context.Context) (bool, error)) error) (err error) {
	if key == "" {
		return errors.New("idempotent key is empty")
	}
	if fn == nil {
		return errors.New("idempotent callback is nil")
	}

	lockKey := fmt.Sprintf("%s:%s", l.prefix, key)
	lockValue := uuid.NewString()

	acquired, err := l.client.SetNX(ctx, lockKey, lockValue, l.ttl).Result()
	if err != nil {
		return fmt.Errorf("acquire idempotent lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("idempotent lock %q is already held", key)
	}

	lockedCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		if p := recover(); p != nil {
			err = errors.Join(err, fmt.Errorf("idempotent callback panic: %v", p))
		}
		releaseErr := l.release(context.Background(), lockKey, lockValue)
		if releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	renewErr := make(chan error, 1)
	go l.renewLoop(lockedCtx, lockKey, lockValue, renewErr)

	checkAndRenew := func(checkCtx context.Context) (bool, error) {
		return l.renew(checkCtx, lockKey, lockValue)
	}

	err = fn(lockedCtx, checkAndRenew)
	cancel()

	select {
	case renewalErr := <-renewErr:
		err = errors.Join(err, renewalErr)
	default:
	}
	return err
}

func (l *IdempotentLock) renewLoop(ctx context.Context, key, value string, errCh chan<- error) {
	interval := l.ttl / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := l.renew(ctx, key, value)
			if err != nil {
				sendRenewErr(errCh, fmt.Errorf("renew idempotent lock: %w", err))
				return
			}
			if !ok {
				sendRenewErr(errCh, errors.New("idempotent lock ownership lost"))
				return
			}
		}
	}
}

func sendRenewErr(errCh chan<- error, err error) {
	select {
	case errCh <- err:
	default:
	}
}

func (l *IdempotentLock) renew(ctx context.Context, key, value string) (bool, error) {
	result, err := goredis.NewScript(renewIdempotentLockScript).
		Run(ctx, l.client, []string{key}, value, strconvMillis(l.ttl)).
		Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (l *IdempotentLock) release(ctx context.Context, key, value string) error {
	result, err := goredis.NewScript(releaseIdempotentLockScript).
		Run(ctx, l.client, []string{key}, value).
		Int()
	if err != nil {
		return fmt.Errorf("release idempotent lock: %w", err)
	}
	if result == 0 {
		return errors.New("idempotent lock was already released or ownership changed")
	}
	return nil
}

func strconvMillis(d time.Duration) string {
	ms := d.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	return fmt.Sprintf("%d", ms)
}
