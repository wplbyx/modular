package database

import (
	"context"
	"time"
)

// Database is the interface for database operations
type Database interface {
	// Basic operations
	Ping(ctx context.Context) error
	Close() error
}

// Transaction is the interface for transaction management
type Transaction interface {
	Begin(ctx context.Context) (Tx, error)
}

// Tx is the interface for transaction operations
type Tx interface {
	Commit() error
	Rollback() error
}

// Migrator is the interface for database migrations
type Migrator interface {
	Migrate(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// IndexDefinition represents a database index
type IndexDefinition struct {
	Name    string
	Columns []string
	Unique  bool
}

// ModelIndexer is the interface for models that define indexes
type ModelIndexer interface {
	DefineIndexes() []IndexDefinition
}

// Config represents common database configuration
type Config struct {
	Dsn             string
	Host            string
	Port            int
	Username        string
	Password        string
	Database        string
	MaxOpenConn     int
	MaxIdleConn     int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	EnableTLS       bool
}

// DSN constants
const (
	DSNSqlite     = "sqlite"
	DSNMySQL      = "mysql"
	DSNPostgres   = "postgres"
	DSNClickhouse = "clickhouse"
)
