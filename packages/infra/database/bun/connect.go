package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"modular/packages/config"
	"modular/packages/infra/database"
)

// Ensure BunDB implements Database interface
var _ database.Database = (*BunDB)(nil)

// Global database connection
var globalDB *BunDB

// BunDB wraps bun.DB with the Database interface
type BunDB struct {
	db *bun.DB
}

// NewBunConnection creates a new Bun database connection
func NewBunConnection(cfg *config.Database) (*BunDB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}

	var sqldb *sql.DB
	var err error

	switch cfg.Dsn {
	case database.DSNPostgres:
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
		sqldb = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	default:
		return nil, fmt.Errorf("unsupported database dsn: %s", cfg.Dsn)
	}

	// Configure connection pool
	sqldb.SetMaxOpenConns(cfg.MaxOpenConn)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConn)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Create bun DB
	db := bun.NewDB(sqldb, pgdialect.New())

	// Test connection
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	bunDB := &BunDB{db: db}
	globalDB = bunDB
	return bunDB, nil
}

// GetDB returns the underlying bun.DB
func (b *BunDB) GetDB() *bun.DB {
	return b.db
}

// Ping tests the database connection
func (b *BunDB) Ping(ctx context.Context) error {
	return b.db.Ping()
}

// Close closes the database connection
func (b *BunDB) Close() error {
	return b.db.Close()
}

// NewSelect creates a new select query
func (b *BunDB) NewSelect() *bun.SelectQuery {
	return b.db.NewSelect()
}

// NewInsert creates a new insert query
func (b *BunDB) NewInsert() *bun.InsertQuery {
	return b.db.NewInsert()
}

// NewUpdate creates a new update query
func (b *BunDB) NewUpdate() *bun.UpdateQuery {
	return b.db.NewUpdate()
}

// NewDelete creates a new delete query
func (b *BunDB) NewDelete() *bun.DeleteQuery {
	return b.db.NewDelete()
}

// NewCreateTable creates a new create table query
func (b *BunDB) NewCreateTable() *bun.CreateTableQuery {
	return b.db.NewCreateTable()
}

// NewDropTable creates a new drop table query
func (b *BunDB) NewDropTable() *bun.DropTableQuery {
	return b.db.NewDropTable()
}

// Begin starts a new transaction
func (b *BunDB) Begin(ctx context.Context) (bun.Tx, error) {
	return b.db.BeginTx(ctx, &sql.TxOptions{})
}

// RunInTx runs a function in a transaction
func (b *BunDB) RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return b.db.RunInTx(ctx, &sql.TxOptions{}, fn)
}

// GetBunDB returns the global BunDB instance
func GetBunDB() *bun.DB {
	if globalDB == nil {
		return nil
	}
	return globalDB.db
}

// GetDB returns the underlying bun.DB from global instance
func GetDB() *bun.DB {
	if globalDB == nil {
		return nil
	}
	return globalDB.db
}
