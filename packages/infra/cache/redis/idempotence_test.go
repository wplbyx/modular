package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type lostLockClient struct{ goredis.UniversalClient }

func (*lostLockClient) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) *goredis.BoolCmd {
	return goredis.NewBoolResult(true, nil)
}
func (*lostLockClient) EvalSha(ctx context.Context, sha string, keys []string, args ...interface{}) *goredis.Cmd {
	return goredis.NewCmdResult(int64(0), nil)
}

func TestIdempotentLock_OwnershipLossCancelsCallback(t *testing.T) {
	lock, err := NewIdempotentLock(&lostLockClient{}, WithIdempotentTTL(300*time.Millisecond))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = lock.Run(ctx, "key", func(ctx context.Context, _ func(context.Context) (bool, error)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, context.DeadlineExceeded), "callback was not canceled until parent deadline")
	require.ErrorContains(t, err, "ownership lost")
}
