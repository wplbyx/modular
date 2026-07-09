package pubsub

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubscriberEndpointWithSubscriberOnly(t *testing.T) {
	sub := &testSubscriber{subscribed: make(chan string, 1)}
	endpoint := NewSubscriberEndpoint(
		"events", sub, "topic",
		func(context.Context, Message) error { return nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Startup(ctx)
	}()

	select {
	case topic := <-sub.subscribed:
		if topic != "topic" {
			t.Fatalf("subscribed topic = %q", topic)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription was not applied")
	}

	cancel()
	<-errCh
	if err := endpoint.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !sub.closed {
		t.Fatal("subscriber was not closed")
	}
}

func TestSubscriberEndpointWithConnector(t *testing.T) {
	sub := &testConnectorSubscriber{
		testSubscriber: testSubscriber{subscribed: make(chan string, 1)},
	}
	endpoint := NewSubscriberEndpoint(
		"events", sub, "topic",
		func(context.Context, Message) error { return nil },
		WithConnect(sub.Connect),
		WithDisconnect(sub.Disconnect),
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Startup(ctx)
	}()
	<-sub.subscribed

	cancel()
	<-errCh
	if err := endpoint.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if sub.connects != 1 || sub.disconnects != 1 || !sub.closed {
		t.Fatalf("expected connects=1 disconnects=1 closed=true, got connects=%d disconnects=%d closed=%v", sub.connects, sub.disconnects, sub.closed)
	}
}

func TestSubscriberEndpointAutoDetectsConnector(t *testing.T) {
	sub := &testConnectorSubscriber{
		testSubscriber: testSubscriber{subscribed: make(chan string, 1)},
	}
	endpoint := NewSubscriberEndpoint(
		"events", sub, "topic",
		func(context.Context, Message) error { return nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Startup(ctx)
	}()
	<-sub.subscribed

	cancel()
	<-errCh
	if err := endpoint.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if sub.connects != 1 || sub.disconnects != 1 || !sub.closed {
		t.Fatalf("expected auto connects=1 disconnects=1 closed=true, got connects=%d disconnects=%d closed=%v", sub.connects, sub.disconnects, sub.closed)
	}
}

func TestSubscriberEndpointPassesSubscribeOptions(t *testing.T) {
	sub := &testSubscriber{
		subscribed: make(chan string, 1),
		options:    make(chan SubscribeOptions, 1),
	}
	endpoint := NewSubscriberEndpoint(
		"events", sub, "topic",
		func(context.Context, Message) error { return nil },
		WithSubscribeOptions(WithSubscribeQoS(2), WithQueueName("workers")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Startup(ctx)
	}()

	select {
	case opts := <-sub.options:
		if opts.QoS != 2 || opts.QueueName != "workers" {
			t.Fatalf("SubscribeOptions = %+v", opts)
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe options were not applied")
	}

	cancel()
	<-errCh
	if err := endpoint.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSubscriberEndpointShutdownJoinsDisconnectAndCloseErrors(t *testing.T) {
	disconnectErr := errors.New("disconnect boom")
	closeErr := errors.New("close boom")
	sub := &testConnectorSubscriber{
		testSubscriber: testSubscriber{
			subscribed: make(chan string, 1),
			closeErr:   closeErr,
		},
		disconnectErr: disconnectErr,
	}
	endpoint := NewSubscriberEndpoint(
		"events", sub, "topic",
		func(context.Context, Message) error { return nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Startup(ctx)
	}()
	<-sub.subscribed

	cancel()
	<-errCh
	err := endpoint.Shutdown(context.Background())
	if !errors.Is(err, disconnectErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Shutdown() error = %v, want both disconnect and close errors", err)
	}
}

type testSubscriber struct {
	subscribed chan string
	options    chan SubscribeOptions
	closed     bool
	closeErr   error
}

func (s *testSubscriber) Subscribe(_ context.Context, topic string, _ MessageHandler, opts ...SubscribeOption) error {
	s.subscribed <- topic
	if s.options != nil {
		options := SubscribeOptions{}
		for _, opt := range opts {
			opt(&options)
		}
		s.options <- options
	}
	return nil
}

func (s *testSubscriber) Unsubscribe(context.Context, string) error {
	return nil
}

func (s *testSubscriber) Close() error {
	s.closed = true
	return s.closeErr
}

type testConnectorSubscriber struct {
	testSubscriber
	connects      int
	disconnects   int
	disconnectErr error
}

func (s *testConnectorSubscriber) Connect(context.Context) error {
	s.connects++
	return nil
}

func (s *testConnectorSubscriber) Disconnect(context.Context) error {
	s.disconnects++
	return s.disconnectErr
}
