package mongo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"modular/packages/config"
	"modular/packages/infra/database"
)

const defaultMongoPort = 27017

// globalClient is the package-level connection, set by NewMongoConnection.
var globalClient *mongodriver.Client

// NewMongoConnection creates a MongoDB client and stores it as the global instance.
func NewMongoConnection(cfg *config.Database) (*mongodriver.Client, error) {
	opts, err := newClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	client, err := mongodriver.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open mongo connection: %w", err)
	}

	if err = client.Ping(context.Background(), readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	globalClient = client
	return client, nil
}

// GetClient returns the global MongoDB client, or nil if NewMongoConnection has not been called.
func GetClient() *mongodriver.Client {
	return globalClient
}

func newClientOptions(cfg *config.Database) (*options.ClientOptions, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}
	if cfg.Dsn != database.DSNMongo {
		return nil, fmt.Errorf("unsupported database dsn: %s", cfg.Dsn)
	}
	if cfg.MaxPoolSize < 0 {
		return nil, errors.New("database max pool size cannot be negative")
	}

	opts := options.Client()
	uri, hosts, err := mongoEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if uri != "" {
		opts.ApplyURI(uri)
	} else {
		opts.SetHosts(hosts)
	}

	if cfg.Username != "" || cfg.Password != "" {
		auth := options.Credential{
			Username: cfg.Username,
			Password: cfg.Password,
		}
		if cfg.Database != "" {
			auth.AuthSource = cfg.Database
		}
		opts.SetAuth(auth)
	}
	if cfg.ReplicaSet != "" {
		opts.SetReplicaSet(cfg.ReplicaSet)
	}
	if cfg.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(uint64(cfg.MaxPoolSize))
	}

	return opts, nil
}

func mongoEndpoint(cfg *config.Database) (string, []string, error) {
	if len(cfg.Urls) > 0 {
		if len(cfg.Urls) == 1 && isMongoURI(cfg.Urls[0]) {
			return cfg.Urls[0], nil, nil
		}
		for _, u := range cfg.Urls {
			if isMongoURI(u) {
				return "", nil, errors.New("mongodb uri must be the only database url")
			}
		}
		return "", cfg.Urls, nil
	}

	if cfg.Host == "" {
		return "", nil, errors.New("database host or urls is required")
	}

	port := cfg.Port
	if port == 0 {
		port = defaultMongoPort
	}
	return "", []string{net.JoinHostPort(cfg.Host, strconv.Itoa(port))}, nil
}

func isMongoURI(s string) bool {
	return strings.HasPrefix(s, "mongodb://") || strings.HasPrefix(s, "mongodb+srv://")
}
