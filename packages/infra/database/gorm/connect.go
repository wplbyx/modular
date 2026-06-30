package gorm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"holographic/packages/config"
	"holographic/packages/infra/database"
)

// Ensure GormDB implements Database interface
var _ database.Database = (*GormDB)(nil)

// Global database connection
var globalDB *GormDB

// GormDB wraps gorm.DB with the Database interface
type GormDB struct {
	db *gorm.DB
}

// NewGormConnection creates a new GORM database connection
func NewGormConnection(cfg *config.Database) (*GormDB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}

	var (
		err  error
		conn gorm.Dialector
	)

	// Build connection string based on DSN type
	switch cfg.Dsn {
	case database.DSNSqlite:
		conn = sqlite.Open(cfg.Path)
	case database.DSNMySQL:
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
		conn = mysql.Open(dsn)
	case database.DSNPostgres:
		builder := new(strings.Builder)
		if cfg.Host != "" {
			builder.WriteString(fmt.Sprintf("host=%s ", cfg.Host))
		}
		if cfg.Port != 0 {
			builder.WriteString(fmt.Sprintf("port=%d ", cfg.Port))
		}
		if cfg.Username != "" {
			builder.WriteString(fmt.Sprintf("user=%s ", cfg.Username))
		}
		if cfg.Password != "" {
			builder.WriteString(fmt.Sprintf("password=%s ", cfg.Password))
		}
		if cfg.Database != "" {
			builder.WriteString(fmt.Sprintf("dbname=%s ", cfg.Database))
		}
		builder.WriteString("sslmode=disable")
		conn = postgres.Open(builder.String())
	case database.DSNClickhouse:
		dsn := fmt.Sprintf("tcp://%s:%d?database=%s&username=%s&password=%s&read_timeout=10&write_timeout=20",
			cfg.Host, cfg.Port, cfg.Database, cfg.Username, cfg.Password)
		conn = clickhouse.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database dsn: %s", cfg.Dsn)
	}

	// Open connection
	db, err := gorm.Open(conn, &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open gorm connection: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConn)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConn)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Test connection
	if err = sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	gormDB := &GormDB{db: db}
	globalDB = gormDB
	return gormDB, nil
}

// GetDB returns the underlying gorm.DB
func (g *GormDB) GetDB() *gorm.DB {
	return g.db
}

// Ping tests the database connection
func (g *GormDB) Ping(ctx context.Context) error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close closes the database connection
func (g *GormDB) Close() error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// GetGormDB returns the global GormDB instance
func GetGormDB() *GormDB {
	return globalDB
}

// GetDB returns the underlying gorm.DB from global instance
func GetDB() *gorm.DB {
	if globalDB == nil {
		return nil
	}
	return globalDB.db
}
