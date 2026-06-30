package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"holographic/packages/transport"
)

var (
	_ transport.Endpoint = (*SubscriberEndpoint)(nil)
	_ Subscriber         = (*SubscriberEndpoint)(nil)
)

// SubscriberEndpoint adapts a pub/sub client into an application-managed
// transport endpoint. It is intended for inbound message/event consumers.
type SubscriberEndpoint struct {
	name       string
	subscriber Subscriber

	mu            sync.RWMutex
	subscriptions []subscription
	started       bool
}

type subscription struct {
	topic   string
	handler MessageHandler
	options []SubscribeOption
}

type connector interface {
	Connect(context.Context) error
	Disconnect(context.Context) error
}

// NewSubscriberEndpoint creates a lifecycle-managed subscriber endpoint.
func NewSubscriberEndpoint(name string, subscriber Subscriber) (*SubscriberEndpoint, error) {
	if subscriber == nil {
		return nil, errors.New("pubsub subscriber is nil")
	}
	if name == "" {
		name = "Message Subscriber"
	}
	return &SubscriberEndpoint{name: name, subscriber: subscriber}, nil
}

func (e *SubscriberEndpoint) Name() string {
	return e.name
}

// Subscribe registers a topic handler. If the endpoint has already started,
// the subscription is applied immediately as well.
func (e *SubscriberEndpoint) Subscribe(ctx context.Context, topic string, handler MessageHandler, opts ...SubscribeOption) error {
	if topic == "" {
		return errors.New("subscription topic is empty")
	}
	if handler == nil {
		return errors.New("subscription handler is nil")
	}

	e.mu.Lock()
	e.subscriptions = append(e.subscriptions, subscription{
		topic:   topic,
		handler: handler,
		options: append([]SubscribeOption(nil), opts...),
	})
	started := e.started
	e.mu.Unlock()

	if started {
		return e.subscriber.Subscribe(ctx, topic, handler, opts...)
	}
	return nil
}

func (e *SubscriberEndpoint) Unsubscribe(ctx context.Context, topic string) error {
	e.mu.Lock()
	filtered := e.subscriptions[:0]
	for _, sub := range e.subscriptions {
		if sub.topic != topic {
			filtered = append(filtered, sub)
		}
	}
	e.subscriptions = filtered
	started := e.started
	e.mu.Unlock()

	if started {
		return e.subscriber.Unsubscribe(ctx, topic)
	}
	return nil
}

func (e *SubscriberEndpoint) Start(ctx context.Context) error {
	if conn, ok := e.subscriber.(connector); ok {
		if err := conn.Connect(ctx); err != nil {
			return fmt.Errorf("connect pubsub subscriber: %w", err)
		}
	}

	e.mu.Lock()
	e.started = true
	subs := append([]subscription(nil), e.subscriptions...)
	e.mu.Unlock()

	for _, sub := range subs {
		if err := e.subscriber.Subscribe(ctx, sub.topic, sub.handler, sub.options...); err != nil {
			if conn, ok := e.subscriber.(connector); ok {
				_ = conn.Disconnect(context.Background())
			}
			return fmt.Errorf("subscribe topic %s: %w", sub.topic, err)
		}
	}

	<-ctx.Done()
	return ctx.Err()
}

func (e *SubscriberEndpoint) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return e.closeSubscriber(ctx)
	}
	e.started = false
	subs := append([]subscription(nil), e.subscriptions...)
	e.mu.Unlock()

	var joined error
	for i := len(subs) - 1; i >= 0; i-- {
		if err := e.subscriber.Unsubscribe(ctx, subs[i].topic); err != nil {
			joined = errors.Join(joined, fmt.Errorf("unsubscribe topic %s: %w", subs[i].topic, err))
		}
	}
	if err := e.closeSubscriber(ctx); err != nil {
		joined = errors.Join(joined, err)
	}
	return joined
}

func (e *SubscriberEndpoint) Close() error {
	return e.Stop(context.Background())
}

func (e *SubscriberEndpoint) closeSubscriber(ctx context.Context) error {
	if conn, ok := e.subscriber.(connector); ok {
		if err := conn.Disconnect(ctx); err != nil {
			return fmt.Errorf("disconnect pubsub subscriber: %w", err)
		}
		return nil
	}
	if err := e.subscriber.Close(); err != nil {
		return fmt.Errorf("close pubsub subscriber: %w", err)
	}
	return nil
}
