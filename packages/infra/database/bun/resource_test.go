package bun

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	bunlib "github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceSetupAndClose(t *testing.T) {
	db := bunlib.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://user:pass@localhost:5432/db?sslmode=disable"))), pgdialect.New())
	resource := NewResource(&configitem.Database{}, WithConnector(func(context.Context, *configitem.Database) (*bunlib.DB, error) {
		return db, nil
	}))

	var _ core.Resource = resource
	var _ core.Provider[*bunlib.DB] = resource
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	require.Same(t, db, value)
	require.NoError(t, resource.Close(context.Background()))
	_, err = resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceSetupFailureDoesNotSetDB(t *testing.T) {
	setupErr := errors.New("setup boom")
	resource := NewResource(&configitem.Database{}, WithConnector(func(context.Context, *configitem.Database) (*bunlib.DB, error) {
		return nil, setupErr
	}))

	require.ErrorIs(t, resource.Setup(context.Background()), setupErr)
	_, err := resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}

func TestResourceSetupRejectsNilConfig(t *testing.T) {
	resource := NewResource(nil)
	require.EqualError(t, resource.Setup(context.Background()), "database config is nil")
}
