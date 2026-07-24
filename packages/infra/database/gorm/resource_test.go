package gorm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceSetupFailureDoesNotProvideDB(t *testing.T) {
	setupErr := errors.New("setup boom")
	resource := NewResource("gorm-test", &configitem.Database{}, nil, WithConnector(
		func(context.Context, *configitem.Database, gorm.Dialector) (*gorm.DB, error) {
			return nil, setupErr
		},
	))

	var _ core.Resource = resource
	var _ core.Provider[*gorm.DB] = resource
	require.ErrorIs(t, resource.Setup(context.Background()), setupErr)
	_, err := resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceSetupRejectsNilConfig(t *testing.T) {
	resource := NewResource("gorm-test", nil, nil)
	require.EqualError(t, resource.Setup(context.Background()), "database config is nil")
}
