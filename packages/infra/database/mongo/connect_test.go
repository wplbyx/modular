package mongo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
)

func TestNewClientOptions_UsesConfiguredHosts(t *testing.T) {
	cfg := &configitem.Mongo{
		Hosts:       []string{"mongo-1:27017", "mongo-2:27017"},
		Database:    "app",
		Username:    "user",
		Password:    "pass",
		ReplicaSet:  "rs0",
		MaxPoolSize: 50,
	}

	opts, err := newClientOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, cfg.Hosts, opts.Hosts)
	require.NotNil(t, opts.Auth)
	require.Equal(t, "user", opts.Auth.Username)
	require.Equal(t, "pass", opts.Auth.Password)
	require.Equal(t, "app", opts.Auth.AuthSource)
	require.NotNil(t, opts.ReplicaSet)
	require.Equal(t, "rs0", *opts.ReplicaSet)
	require.NotNil(t, opts.MaxPoolSize)
	require.Equal(t, uint64(50), *opts.MaxPoolSize)
}

func TestNewClientOptions_AcceptsMongoURI(t *testing.T) {
	uri := "mongodb://mongo-1:27017,mongo-2:27017/?replicaSet=rs0"
	opts, err := newClientOptions(&configitem.Mongo{URI: uri})
	require.NoError(t, err)
	require.Equal(t, uri, opts.GetURI())
}

func TestNewClientOptions_RejectsInvalidConfig(t *testing.T) {
	_, err := newClientOptions(nil)
	require.EqualError(t, err, "mongo config is nil")

	_, err = newClientOptions(&configitem.Mongo{})
	require.EqualError(t, err, "mongo URI or hosts is required")

	_, err = newClientOptions(&configitem.Mongo{URI: "localhost", MaxPoolSize: 1})
	require.EqualError(t, err, "mongo URI must start with mongodb:// or mongodb+srv://")

	_, err = newClientOptions(&configitem.Mongo{Hosts: []string{"localhost:27017"}, MaxPoolSize: -1})
	require.EqualError(t, err, "mongo max pool size cannot be negative")

	_, err = newClientOptions(&configitem.Mongo{URI: "mongodb://localhost", Hosts: []string{"localhost:27017"}})
	require.EqualError(t, err, "mongo URI and hosts are mutually exclusive")
}
