package mongo

import (
	"context"
	"errors"

	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

// Resource 是由 core.ManagedResource 管理的 MongoDB 客户端。
type Resource = core.ManagedResource[*mongodriver.Client]

type resourceConfig struct {
	connect func(context.Context, *configitem.Mongo) (*mongodriver.Client, error)
}

// ResourceOption 配置 MongoDB Resource。
type ResourceOption func(*resourceConfig)

// WithConnector 覆盖 MongoDB 建连函数，主要用于测试。
func WithConnector(fn func(context.Context, *configitem.Mongo) (*mongodriver.Client, error)) ResourceOption {
	return func(cfg *resourceConfig) {
		if fn != nil {
			cfg.connect = fn
		}
	}
}

// NewResource 创建 MongoDB 生命周期资源。
func NewResource(cfg *configitem.Mongo, options ...ResourceOption) *Resource {
	resourceCfg := resourceConfig{connect: NewMongoConnection}
	for _, option := range options {
		if option != nil {
			option(&resourceCfg)
		}
	}

	return core.NewManagedResource(
		"mongo",
		func(ctx context.Context) (*mongodriver.Client, error) {
			if cfg == nil {
				return nil, errors.New("mongo config is nil")
			}
			return resourceCfg.connect(ctx, cfg)
		},
		func(ctx context.Context, client *mongodriver.Client) error {
			return client.Disconnect(ctx)
		},
		core.WithResourceCheck(func(ctx context.Context, client *mongodriver.Client) error {
			return client.Ping(ctx, readpref.Primary())
		}),
	)
}
