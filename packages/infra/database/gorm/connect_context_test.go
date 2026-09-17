package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	gormlib "gorm.io/gorm"
)

type countedConnector struct{ pings atomic.Int32 }

func (c *countedConnector) Connect(context.Context) (driver.Conn, error) {
	return &countedConnection{owner: c}, nil
}
func (c *countedConnector) Driver() driver.Driver { return countedDriver{c} }

type countedDriver struct{ owner *countedConnector }

func (d countedDriver) Open(string) (driver.Conn, error) {
	return d.owner.Connect(context.Background())
}

type countedConnection struct {
	driver.Conn
	owner *countedConnector
}

func (c *countedConnection) Ping(ctx context.Context) error { c.owner.pings.Add(1); return ctx.Err() }
func (*countedConnection) Close() error                     { return nil }

type connectionDialect struct {
	gormlib.Dialector
	connection *sql.DB
}

func (connectionDialect) Name() string                      { return "test" }
func (d connectionDialect) Initialize(db *gormlib.DB) error { db.ConnPool = d.connection; return nil }
func TestGormConnection_CanceledContextDoesNotPingInBackground(t *testing.T) {
	connector := &countedConnector{}
	db := sql.OpenDB(connector)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewGormConnection(ctx, &configitem.Database{DSN: "test"}, connectionDialect{connection: db})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, connector.pings.Load())
}
