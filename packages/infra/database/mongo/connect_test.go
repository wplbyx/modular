package mongo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/infra/database"
)

func TestNewClientOptions_UsesConfiguredHosts(t *testing.T) {
	cfg := &config.Database{
		Dsn:         database.DSNMongo,
		Urls:        []string{"mongo-1:27017", "mongo-2:27017"},
		Database:    "app",
		Username:    "user",
		Password:    "pass",
		ReplicaSet:  "rs0",
		MaxPoolSize: 50,
	}

	opts, err := newClientOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"mongo-1:27017", "mongo-2:27017"}, opts.Hosts)
	require.NotNil(t, opts.Auth)
	require.Equal(t, "user", opts.Auth.Username)
	require.Equal(t, "pass", opts.Auth.Password)
	require.Equal(t, "app", opts.Auth.AuthSource)
	require.NotNil(t, opts.ReplicaSet)
	require.Equal(t, "rs0", *opts.ReplicaSet)
	require.NotNil(t, opts.MaxPoolSize)
	require.Equal(t, uint64(50), *opts.MaxPoolSize)
}

func TestNewClientOptions_UsesHostPortFallback(t *testing.T) {
	cfg := &config.Database{
		Dsn:  database.DSNMongo,
		Host: "127.0.0.1",
		Port: 27018,
	}

	opts, err := newClientOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1:27018"}, opts.Hosts)
}

func TestNewClientOptions_DefaultsMongoPort(t *testing.T) {
	cfg := &config.Database{
		Dsn:  database.DSNMongo,
		Host: "localhost",
	}

	opts, err := newClientOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"localhost:27017"}, opts.Hosts)
}

func TestNewClientOptions_AcceptsSingleMongoURI(t *testing.T) {
	cfg := &config.Database{
		Dsn:  database.DSNMongo,
		Urls: []string{"mongodb://mongo-1:27017,mongo-2:27017/?replicaSet=rs0"},
	}

	opts, err := newClientOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, "mongodb://mongo-1:27017,mongo-2:27017/?replicaSet=rs0", opts.GetURI())
}

func TestNewClientOptions_RejectsInvalidConfig(t *testing.T) {
	_, err := newClientOptions(nil)
	require.EqualError(t, err, "database config is nil")

	_, err = newClientOptions(&config.Database{Dsn: database.DSNPostgres})
	require.EqualError(t, err, "unsupported database dsn: postgres")

	_, err = newClientOptions(&config.Database{Dsn: database.DSNMongo})
	require.EqualError(t, err, "database host or urls is required")

	_, err = newClientOptions(&config.Database{Dsn: database.DSNMongo, Host: "localhost", MaxPoolSize: -1})
	require.EqualError(t, err, "database max pool size cannot be negative")
}
