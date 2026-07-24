package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/infra/storage"
)

func TestDiskResource(t *testing.T) {
	resource := New(&configitem.Storage{
		Type: "disk",
		Disk: &configitem.DiskStorageConfig{RootDir: t.TempDir()},
	})

	var _ core.Resource = resource
	var _ core.Provider[storage.Storage] = resource
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NoError(t, resource.Close(context.Background()))
	_, err = resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceRejectsUnsupportedType(t *testing.T) {
	resource := New(&configitem.Storage{Type: "s3"})
	err := resource.Setup(context.Background())
	require.ErrorIs(t, err, storage.ErrUnsupportedStorageType)
}
