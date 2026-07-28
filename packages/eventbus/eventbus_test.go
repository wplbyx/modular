package eventbus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	modularlog "github.com/wplbyx/modular/packages/log"
)

func TestBusOrdersHandlersAndDrains(t *testing.T) {
	bus, err := New(Config{Name: "test", Capacity: 8}, modularlog.Default())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var received []int
	if err := bus.Subscribe("created", func(_ context.Context, event Event) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, event.Payload.(int))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for value := 0; value < 5; value++ {
		if err := bus.Publish(context.Background(), Event{Name: "created", Payload: value}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Close(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for index, value := range received {
		if index != value {
			t.Fatalf("received = %v", received)
		}
	}
}

func TestBusRejectsPublishOutsideLifecycle(t *testing.T) {
	bus, err := New(Config{Name: "test", Capacity: 1}, modularlog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), Event{Name: "event"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Publish before Setup error = %v", err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), Event{Name: "event"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after Close error = %v", err)
	}
}

func TestBusContinuesAfterHandlerError(t *testing.T) {
	bus, err := New(Config{Name: "test", Capacity: 2}, modularlog.Default())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_ = bus.Subscribe("event", func(context.Context, Event) error { return errors.New("failed") })
	_ = bus.Subscribe("event", func(context.Context, Event) error { called = true; return nil })
	_ = bus.Setup(context.Background())
	_ = bus.Publish(context.Background(), Event{Name: "event"})
	_ = bus.Close(context.Background())
	if !called {
		t.Fatal("second handler was not called")
	}
	if bus.Stats().HandlerErrors != 1 {
		t.Fatalf("handler errors = %d", bus.Stats().HandlerErrors)
	}
}

func TestLastProducerWakesClosingConsumer(t *testing.T) {
	bus, err := New(Config{Name: "test", Capacity: 1}, modularlog.Default())
	if err != nil {
		t.Fatal(err)
	}
	bus.state.Store(stateClosing)
	bus.producers.Store(1)

	bus.endProducer()

	select {
	case <-bus.wake:
	default:
		t.Fatal("last producer did not wake the closing consumer")
	}
}
