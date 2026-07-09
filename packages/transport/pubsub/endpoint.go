package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"modular/packages/core"
)

// Connector is an optional capability that a subscriber-backed endpoint may
// invoke before subscribing. MQTTClient satisfies it; Kafka Consumer does not.
type Connector interface {
	Connect(ctx context.Context) error
}

// Disconnector is the symmetric teardown for Connector.
type Disconnector interface {
	Disconnect(ctx context.Context) error
}

// SubscriberEndpoint wraps a pubsub.Subscriber so it can be registered with
// Application as a core.Endpoint. It connects (optional), subscribes, then
// blocks in Startup until Shutdown cancels the context.
//
// Usage:
//
//	// MQTT
//	mqttClient, _ := mqtt.NewClient(mqtt.WithBrokerURL("tcp://broker:1883"))
//	ep := pubsub.NewSubscriberEndpoint(
//	    "mqtt-events",
//	    mqttClient,
//	    "events/topic",
//	    myHandler,
//	    pubsub.WithConnect(mqttClient.Connect),
//	    pubsub.WithDisconnect(mqttClient.Disconnect),
//	)
//	app.WithEndpoint(ep)
//
//	// Kafka (no connect/disconnect needed)
//	consumer, _ := kafka.NewConsumer(kafka.WithBrokers("localhost:9092"), kafka.WithTopic("events"))
//	ep = pubsub.NewSubscriberEndpoint("kafka-events", consumer, "events", myHandler)
//	app.WithEndpoint(ep)
type SubscriberEndpoint struct {
	name    string
	sub     Subscriber
	topic   string
	handler MessageHandler
	opts    []SubscribeOption

	onStart func(ctx context.Context) error
	onStop  func(ctx context.Context) error

	mu     sync.Mutex
	cancel context.CancelFunc
}

// SubscriberOption configures a SubscriberEndpoint.
type SubscriberOption func(*SubscriberEndpoint)

// WithConnect registers a callback invoked at the start of Startup
// (e.g. an MQTT broker connect).
func WithConnect(fn func(ctx context.Context) error) SubscriberOption {
	return func(e *SubscriberEndpoint) { e.onStart = fn }
}

// WithDisconnect registers a callback invoked during Shutdown
// (e.g. an MQTT broker disconnect).
func WithDisconnect(fn func(ctx context.Context) error) SubscriberOption {
	return func(e *SubscriberEndpoint) { e.onStop = fn }
}

// WithSubscribeOptions forwards subscription options to Subscriber.Subscribe.
func WithSubscribeOptions(opts ...SubscribeOption) SubscriberOption {
	return func(e *SubscriberEndpoint) { e.opts = append(e.opts, opts...) }
}

// NewSubscriberEndpoint creates a core.Endpoint that manages a subscription.
func NewSubscriberEndpoint(name string, sub Subscriber, topic string, handler MessageHandler, opts ...SubscriberOption) *SubscriberEndpoint {
	e := &SubscriberEndpoint{
		name:    name,
		sub:     sub,
		topic:   topic,
		handler: handler,
	}
	if connector, ok := sub.(Connector); ok {
		e.onStart = connector.Connect
	}
	if disconnector, ok := sub.(Disconnector); ok {
		e.onStop = disconnector.Disconnect
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

var _ core.Endpoint = (*SubscriberEndpoint)(nil)

// Name returns the endpoint label for logging.
func (e *SubscriberEndpoint) Name() string { return e.name }

// Startup runs the optional connect hook, subscribes, then blocks until
// Shutdown cancels the internal context.
func (e *SubscriberEndpoint) Startup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	subCtx, cancel := context.WithCancel(ctx)

	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	if e.onStart != nil {
		if err := e.onStart(subCtx); err != nil {
			cancel()
			return fmt.Errorf("connect for endpoint %s: %w", e.name, err)
		}
	}

	if err := e.sub.Subscribe(subCtx, e.topic, e.handler, e.opts...); err != nil {
		cancel()
		return fmt.Errorf("subscribe %s: %w", e.topic, err)
	}

	<-subCtx.Done()
	return nil
}

// Shutdown cancels the subscription loop, calls the optional disconnect
// hook, and closes the underlying subscriber.
func (e *SubscriberEndpoint) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.mu.Unlock()

	var errs error
	if e.onStop != nil {
		if err := e.onStop(ctx); err != nil {
			errs = fmt.Errorf("disconnect for endpoint %s: %w", e.name, err)
		}
	}
	if err := e.sub.Close(); err != nil {
		errs = errors.Join(errs, fmt.Errorf("close subscriber for endpoint %s: %w", e.name, err))
	}
	return errs
}
