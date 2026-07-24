package gorm

import (
	"context"
	"errors"

	gormlib "gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

// Resource 是由 core.ManagedResource 管理的 GORM 连接。
type Resource = core.ManagedResource[*gormlib.DB]

type Connector func(context.Context, *configitem.Database, gormlib.Dialector) (*gormlib.DB, error)

type resourceConfig struct {
	connect Connector
}

// ResourceOption 配置 GORM Resource。
type ResourceOption func(*resourceConfig)

// WithConnector 覆盖 GORM 建连函数，主要用于测试。
func WithConnector(connector Connector) ResourceOption {
	return func(cfg *resourceConfig) {
		if connector != nil {
			cfg.connect = connector
		}
	}
}

// NewResource 使用已经选定的方言创建 GORM 生命周期资源。
func NewResource(
	name string,
	cfg *configitem.Database,
	dialector gormlib.Dialector,
	options ...ResourceOption,
) *Resource {
	resourceCfg := resourceConfig{connect: NewGormConnection}
	for _, option := range options {
		if option != nil {
			option(&resourceCfg)
		}
	}
	if name == "" {
		name = "gorm"
	}

	return core.NewManagedResource(
		name,
		func(ctx context.Context) (*gormlib.DB, error) {
			if cfg == nil {
				return nil, errors.New("database config is nil")
			}
			return resourceCfg.connect(ctx, cfg, dialector)
		},
		func(_ context.Context, db *gormlib.DB) error {
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.Close()
		},
		core.WithResourceCheck(func(ctx context.Context, db *gormlib.DB) error {
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.PingContext(ctx)
		}),
	)
}
