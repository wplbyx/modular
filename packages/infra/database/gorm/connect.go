package gorm

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	gormlib "gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/infra/database"
)

// globalDB is the package-level connection, set by NewGormConnection.
var globalDB *gormlib.DB

// NewGormConnection creates a GORM database connection and stores it as the global instance.
func NewGormConnection(cfg *config.Database) (*gormlib.DB, error) {
	if cfg == nil {
		return nil, errors.New("database config is nil")
	}

	var (
		err  error
		conn gormlib.Dialector
	)

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

	db, err := gormlib.Open(conn, &gormlib.Config{
		SkipDefaultTransaction: true,
	})
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

	if err = sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	globalDB = db
	return db, nil
}

// GetDB returns the global GORM connection, or nil if NewGormConnection has not been called.
func GetDB() *gormlib.DB {
	return globalDB
}
