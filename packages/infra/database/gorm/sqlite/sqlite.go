package sqlite

import (
	"context"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"
)

// NewConnection 使用纯 Go SQLite 驱动创建 GORM 连接。
func NewConnection(ctx context.Context, cfg *configitem.Database) (*gorm.DB, error) {
	if cfg == nil {
		return modulargorm.NewGormConnection(ctx, nil, nil)
	}
	return modulargorm.NewGormConnection(ctx, cfg, sqlite.Open(cfg.DSN))
}

// NewResource 创建纯 Go SQLite GORM 生命周期资源。
func NewResource(cfg *configitem.Database, options ...modulargorm.ResourceOption) *modulargorm.Resource {
	var dialector gorm.Dialector
	if cfg != nil {
		dialector = sqlite.Open(cfg.DSN)
	}
	return modulargorm.NewResource("gorm-sqlite", cfg, dialector, options...)
}
