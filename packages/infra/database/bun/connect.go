package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/wplbyx/modular/packages/config/configitem"
)

// NewBunConnection 创建并验证一个 PostgreSQL Bun 连接。
func NewBunConnection(ctx context.Context, cfg *configitem.Database) (*bun.DB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}
	if cfg.DSN == "" {
		return nil, errors.New("database DSN is empty")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN)))
	sqldb.SetMaxOpenConns(cfg.MaxOpenConn)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConn)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	db := bun.NewDB(sqldb, pgdialect.New())
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return db, nil
}
