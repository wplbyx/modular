package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceSetupAndClose(t *testing.T) {
	resource := NewResource(&configitem.Database{DSN: ":memory:"})
	require.NoError(t, resource.Setup(context.Background()))
	db, err := resource.Value()
	require.NoError(t, err)
	require.NotNil(t, db)
	require.NoError(t, resource.Check(context.Background()))
	require.NoError(t, resource.Close(context.Background()))
	_, err = resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}
