package assemblycheck

import (
	"context"
	"errors"
	"testing"

	"github.com/wplbyx/modular/packages/core"
)

func TestAssembly_DefersProviderUntilUse(t *testing.T) {
	ctx := context.Background()
	resource := core.NewManagedResource("names", func(context.Context) (map[string]string, error) {
		return map[string]string{"customer-1": "Ada"}, nil
	}, nil)
	reads := 0
	provider := core.ProviderFunc[map[string]string](func() (map[string]string, error) {
		reads++
		return resource.Value()
	})
	orders, err := assemble(provider)
	if err != nil || reads != 0 {
		t.Fatalf("assembly read uninitialized provider: reads=%d err=%v", reads, err)
	}
	if _, err := orders.Summarizer.Summary(ctx, "customer-1"); !errors.Is(err, core.ErrResourceNotReady) {
		t.Fatalf("before Setup: %v", err)
	}
	if err := resource.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resource.Close(ctx) })
	if got, err := orders.Summarizer.Summary(ctx, "customer-1"); err != nil || got != "Order for Ada" {
		t.Fatalf("assembled behavior: got=%q err=%v", got, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := orders.Summarizer.Summary(cancelled, "customer-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := resource.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := orders.Summarizer.Summary(ctx, "customer-1"); !errors.Is(err, core.ErrResourceNotReady) {
		t.Fatalf("after Close: %v", err)
	}
}
