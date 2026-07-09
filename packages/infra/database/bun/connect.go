package bun

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/infra/database"
)

// globalDB is the package-level connection, set by NewBunConnection.
var globalDB *bun.DB

// NewBunConnection creates a Bun database connection and stores it as the global instance.
func NewBunConnection(cfg *config.Database) (*bun.DB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}

	var sqldb *sql.DB

	switch cfg.Dsn {
	case database.DSNPostgres:
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
		sqldb = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	default:
		return nil, fmt.Errorf("unsupported database dsn: %s", cfg.Dsn)
	}

	sqldb.SetMaxOpenConns(cfg.MaxOpenConn)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConn)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	db := bun.NewDB(sqldb, pgdialect.New())

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	globalDB = db
	return db, nil
}

// GetDB returns the global Bun connection, or nil if NewBunConnection has not been called.
func GetDB() *bun.DB {
	return globalDB
}
