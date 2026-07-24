package mysql

import (
	"context"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"
)

// NewConnection 创建 MySQL GORM 连接。
func NewConnection(ctx context.Context, cfg *configitem.Database) (*gorm.DB, error) {
	if cfg == nil {
		return modulargorm.NewGormConnection(ctx, nil, nil)
	}
	return modulargorm.NewGormConnection(ctx, cfg, mysql.Open(cfg.DSN))
}

// NewResource 创建 MySQL GORM 生命周期资源。
func NewResource(cfg *configitem.Database, options ...modulargorm.ResourceOption) *modulargorm.Resource {
	var dialector gorm.Dialector
	if cfg != nil {
		dialector = mysql.Open(cfg.DSN)
	}
	return modulargorm.NewResource("gorm-mysql", cfg, dialector, options...)
}
