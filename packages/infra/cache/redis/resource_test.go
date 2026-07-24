package redis

import (
	"context"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceSetupAndClose(t *testing.T) {
	client := goredis.NewUniversalClient(&goredis.UniversalOptions{Addrs: []string{"127.0.0.1:0"}})
	resource := NewResource(&configitem.Redis{}, WithConnector(func(context.Context, *configitem.Redis) (goredis.UniversalClient, error) {
		return client, nil
	}))

	var _ core.Resource = resource
	var _ core.Provider[goredis.UniversalClient] = resource
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	require.Same(t, client, value)
	require.NoError(t, resource.Close(context.Background()))
	_, err = resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceSetupFailureDoesNotSetClient(t *testing.T) {
	setupErr := errors.New("setup boom")
	resource := NewResource(&configitem.Redis{}, WithConnector(func(context.Context, *configitem.Redis) (goredis.UniversalClient, error) {
		return nil, setupErr
	}))

	require.ErrorIs(t, resource.Setup(context.Background()), setupErr)
	_, err := resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceSetupRejectsNilConfig(t *testing.T) {
	resource := NewResource(nil)
	require.EqualError(t, resource.Setup(context.Background()), "redis config is nil")
}
