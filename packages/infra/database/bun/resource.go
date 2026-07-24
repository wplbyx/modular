package bun

import (
	"context"
	"errors"

	bunlib "github.com/uptrace/bun"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

// Resource 是由 core.ManagedResource 管理的 Bun 连接。
type Resource = core.ManagedResource[*bunlib.DB]

type resourceConfig struct {
	connect func(context.Context, *configitem.Database) (*bunlib.DB, error)
}

// ResourceOption 配置 Bun Resource。
type ResourceOption func(*resourceConfig)

// WithConnector 覆盖 Bun 建连函数，主要用于测试或定制连接。
func WithConnector(fn func(context.Context, *configitem.Database) (*bunlib.DB, error)) ResourceOption {
	return func(cfg *resourceConfig) {
		if fn != nil {
			cfg.connect = fn
		}
	}
}

// NewResource 创建 Bun 生命周期资源。
func NewResource(cfg *configitem.Database, options ...ResourceOption) *Resource {
	resourceCfg := resourceConfig{connect: NewBunConnection}
	for _, option := range options {
		if option != nil {
			option(&resourceCfg)
		}
	}

	return core.NewManagedResource(
		"bun",
		func(ctx context.Context) (*bunlib.DB, error) {
			if cfg == nil {
				return nil, errors.New("database config is nil")
			}
			return resourceCfg.connect(ctx, cfg)
		},
		func(_ context.Context, db *bunlib.DB) error {
			return db.Close()
		},
		core.WithResourceCheck(func(ctx context.Context, db *bunlib.DB) error {
			return db.PingContext(ctx)
		}),
	)
}
