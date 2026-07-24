package mongo

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceProvidesConnectedClient(t *testing.T) {
	client, err := mongo.Connect()
	require.NoError(t, err)
	resource := NewResource(&configitem.Mongo{}, WithConnector(func(context.Context, *configitem.Mongo) (*mongo.Client, error) {
		return client, nil
	}))

	var _ core.Resource = resource
	var _ core.Provider[*mongo.Client] = resource
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	require.Same(t, client, value)
	require.NoError(t, resource.Close(context.Background()))
}

func TestResourceSetupFailureDoesNotProvideClient(t *testing.T) {
	setupErr := errors.New("setup failed")
	resource := NewResource(&configitem.Mongo{}, WithConnector(func(context.Context, *configitem.Mongo) (*mongo.Client, error) {
		return nil, setupErr
	}))

	require.ErrorIs(t, resource.Setup(context.Background()), setupErr)
	_, err := resource.Value()
	require.ErrorIs(t, err, core.ErrResourceNotReady)
}
