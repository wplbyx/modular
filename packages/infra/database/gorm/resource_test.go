package gorm

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	gormlib "gorm.io/gorm"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceImplementsCoreResource(t *testing.T) {
	var _ core.Resource = (*Resource)(nil)
}

func TestResourceSetupAndClose(t *testing.T) {
	db, err := gormlib.Open(sqlite.Open(":memory:"), &gormlib.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	res := NewResource(&config.Database{}, WithConnector(func(*config.Database) (*gormlib.DB, error) {
		return db, nil
	}))

	if err := res.Setup(context.Background()); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if res.DB() != db {
		t.Fatal("DB() did not return setup database")
	}
	if err := res.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if res.DB() != nil {
		t.Fatal("DB() after Close() is not nil")
	}
}

func TestResourceSetupFailureDoesNotSetDB(t *testing.T) {
	setupErr := errors.New("setup boom")
	res := NewResource(&config.Database{}, WithConnector(func(*config.Database) (*gormlib.DB, error) {
		return nil, setupErr
	}))

	if err := res.Setup(context.Background()); !errors.Is(err, setupErr) {
		t.Fatalf("Setup() error = %v, want setupErr", err)
	}
	if res.DB() != nil {
		t.Fatal("DB() set after failed Setup")
	}
}

func TestResourceSetupRejectsNilConfig(t *testing.T) {
	res := NewResource(nil)
	if err := res.Setup(context.Background()); err == nil {
		t.Fatal("Setup() error = nil")
	}
}
