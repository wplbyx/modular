package postgres

import (
	"context"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"
)

// NewConnection 创建 PostgreSQL GORM 连接。
func NewConnection(ctx context.Context, cfg *configitem.Database) (*gorm.DB, error) {
	if cfg == nil {
		return modulargorm.NewGormConnection(ctx, nil, nil)
	}
	return modulargorm.NewGormConnection(ctx, cfg, postgres.Open(cfg.DSN))
}

// NewResource 创建 PostgreSQL GORM 生命周期资源。
func NewResource(cfg *configitem.Database, options ...modulargorm.ResourceOption) *modulargorm.Resource {
	var dialector gorm.Dialector
	if cfg != nil {
		dialector = postgres.Open(cfg.DSN)
	}
	return modulargorm.NewResource("gorm-postgres", cfg, dialector, options...)
}
