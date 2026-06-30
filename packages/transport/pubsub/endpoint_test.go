package pubsub

import (
	"context"
	"testing"
	"time"
)

func TestSubscriberEndpointWithSubscriberOnly(t *testing.T) {
	sub := &testSubscriber{subscribed: make(chan string, 1)}
	endpoint, err := NewSubscriberEndpoint("events", sub)
	if err != nil {
		t.Fatalf("NewSubscriberEndpoint() error = %v", err)
	}
	if err := endpoint.Subscribe(context.Background(), "topic", func(context.Context, Message) error { return nil }); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Start(ctx)
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
	if err := endpoint.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !sub.closed {
		t.Fatal("subscriber was not closed")
	}
}

func TestSubscriberEndpointWithConnector(t *testing.T) {
	sub := &testConnectorSubscriber{
		testSubscriber: testSubscriber{subscribed: make(chan string, 1)},
	}
	endpoint, err := NewSubscriberEndpoint("events", sub)
	if err != nil {
		t.Fatalf("NewSubscriberEndpoint() error = %v", err)
	}
	if err := endpoint.Subscribe(context.Background(), "topic", func(context.Context, Message) error { return nil }); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- endpoint.Start(ctx)
	}()
	<-sub.subscribed

	cancel()
	<-errCh
	if err := endpoint.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if sub.connects != 1 || sub.disconnects != 1 || sub.closed {
		t.Fatalf("connects=%d disconnects=%d closed=%v", sub.connects, sub.disconnects, sub.closed)
	}
}

type testSubscriber struct {
	subscribed chan string
	closed     bool
}

func (s *testSubscriber) Subscribe(_ context.Context, topic string, _ MessageHandler, _ ...SubscribeOption) error {
	s.subscribed <- topic
	return nil
}

func (s *testSubscriber) Unsubscribe(context.Context, string) error {
	return nil
}

func (s *testSubscriber) Close() error {
	s.closed = true
	return nil
}

type testConnectorSubscriber struct {
	testSubscriber
	connects    int
	disconnects int
}

func (s *testConnectorSubscriber) Connect(context.Context) error {
	s.connects++
	return nil
}

func (s *testConnectorSubscriber) Disconnect(context.Context) error {
	s.disconnects++
	return nil
}
