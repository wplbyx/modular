package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/wplbyx/modular/packages/config/configitem"
)

// NewMongoConnection 创建并验证 MongoDB 客户端。
func NewMongoConnection(ctx context.Context, cfg *configitem.Mongo) (*mongodriver.Client, error) {
	opts, err := newClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	client, err := mongodriver.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open mongo connection: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return client, nil
}

func newClientOptions(cfg *configitem.Mongo) (*options.ClientOptions, error) {
	if cfg == nil {
		return nil, errors.New("mongo config is nil")
	}
	if cfg.MaxPoolSize < 0 {
		return nil, errors.New("mongo max pool size cannot be negative")
	}
	if cfg.URI == "" && len(cfg.Hosts) == 0 {
		return nil, errors.New("mongo URI or hosts is required")
	}
	if cfg.URI != "" && len(cfg.Hosts) > 0 {
		return nil, errors.New("mongo URI and hosts are mutually exclusive")
	}
	if cfg.URI != "" && !isMongoURI(cfg.URI) {
		return nil, errors.New("mongo URI must start with mongodb:// or mongodb+srv://")
	}

	opts := options.Client()
	if cfg.URI != "" {
		opts.ApplyURI(cfg.URI)
	} else {
		opts.SetHosts(cfg.Hosts)
	}
	if cfg.Username != "" || cfg.Password != "" {
		auth := options.Credential{Username: cfg.Username, Password: cfg.Password}
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

func isMongoURI(value string) bool {
	return strings.HasPrefix(value, "mongodb://") || strings.HasPrefix(value, "mongodb+srv://")
}
