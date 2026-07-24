package clickhouse

import (
	"context"

	"gorm.io/driver/clickhouse"
	"gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
	modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"
)

// NewConnection 创建 ClickHouse GORM 连接。
func NewConnection(ctx context.Context, cfg *configitem.Database) (*gorm.DB, error) {
	if cfg == nil {
		return modulargorm.NewGormConnection(ctx, nil, nil)
	}
	return modulargorm.NewGormConnection(ctx, cfg, clickhouse.Open(cfg.DSN))
}

// NewResource 创建 ClickHouse GORM 生命周期资源。
func NewResource(cfg *configitem.Database, options ...modulargorm.ResourceOption) *modulargorm.Resource {
	var dialector gorm.Dialector
	if cfg != nil {
		dialector = clickhouse.Open(cfg.DSN)
	}
	return modulargorm.NewResource("gorm-clickhouse", cfg, dialector, options...)
}
