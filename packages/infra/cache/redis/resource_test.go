package redis

import (
	"context"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/core"
)

func TestResourceImplementsCoreResource(t *testing.T) {
	var _ core.Resource = (*Resource)(nil)
}

func TestResourceSetupAndClose(t *testing.T) {
	client := goredis.NewUniversalClient(&goredis.UniversalOptions{Addrs: []string{"127.0.0.1:0"}})
	res := NewResource(&config.Redis{}, WithConnector(func(*config.Redis) (goredis.UniversalClient, error) {
		return client, nil
	}))

	if err := res.Setup(context.Background()); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if res.Client() != client {
		t.Fatal("Client() did not return setup client")
	}
	if err := res.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if res.Client() != nil {
		t.Fatal("Client() after Close() is not nil")
	}
}

func TestResourceSetupFailureDoesNotSetClient(t *testing.T) {
	setupErr := errors.New("setup boom")
	res := NewResource(&config.Redis{}, WithConnector(func(*config.Redis) (goredis.UniversalClient, error) {
		return nil, setupErr
	}))

	if err := res.Setup(context.Background()); !errors.Is(err, setupErr) {
		t.Fatalf("Setup() error = %v, want setupErr", err)
	}
	if res.Client() != nil {
		t.Fatal("Client() set after failed Setup")
	}
}

func TestResourceSetupRejectsNilConfig(t *testing.T) {
	res := NewResource(nil)
	if err := res.Setup(context.Background()); err == nil {
		t.Fatal("Setup() error = nil")
	}
}
