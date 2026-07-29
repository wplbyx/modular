package resource

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/idgen"
	"github.com/wplbyx/modular/packages/idgen/snowflake"
)

func TestUUIDv7ResourceLifecycle(t *testing.T) {
	resource := NewUUIDv7("business-id")
	_, err := resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
	require.NoError(t, resource.Setup(context.Background()))
	generator, err := resource.Value()
	require.NoError(t, err)
	_, err = generator.Generate(context.Background())
	require.NoError(t, err)
	require.NoError(t, resource.Close(context.Background()))
	_, err = resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestSnowflakeResourceLifecycle(t *testing.T) {
	resource := NewSnowflake("business-id", snowflake.DefaultLayout(), snowflake.StaticNode(3))
	require.NoError(t, resource.Setup(context.Background()))
	generator, err := resource.Value()
	require.NoError(t, err)
	_, err = generator.Generate(context.Background())
	require.NoError(t, err)
	require.NoError(t, resource.Close(context.Background()))
	_, err = generator.Generate(context.Background())
	require.True(t, errors.Is(err, idgen.ErrClosed))
}
