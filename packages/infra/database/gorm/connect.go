package gorm

import (
	"context"
	"errors"
	"fmt"

	gormlib "gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config/configitem"
)

// NewGormConnection 使用调用方选择的方言创建并验证 GORM 连接。
func NewGormConnection(
	ctx context.Context,
	cfg *configitem.Database,
	dialector gormlib.Dialector,
) (*gormlib.DB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}
	if cfg.DSN == "" {
		return nil, errors.New("database DSN is empty")
	}
	if dialector == nil {
		return nil, errors.New("gorm dialector is nil")
	}

	db, err := gormlib.Open(dialector, &gormlib.Config{SkipDefaultTransaction: true})
	if err != nil {
		return nil, fmt.Errorf("failed to open gorm connection: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConn)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConn)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return db, nil
}
