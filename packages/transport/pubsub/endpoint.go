package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/metadata"
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
	name       string
	sub        Subscriber
	topic      string
	handler    MessageHandler
	opts       []SubscribeOption
	propagator *metadata.Propagator

	onStart func(ctx context.Context) error
	onStop  func(ctx context.Context) error

	mu             sync.Mutex
	cancel         context.CancelFunc
	ready          chan struct{}
	once           sync.Once
	state          string
	readyErr       error
	startDone      chan struct{}
	shutdownDone   chan struct{}
	shutdownErr    error
	shutdownCancel context.CancelFunc
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

// WithMetadataPropagator overrides message metadata extraction. Passing nil
// disables extraction for transports without a header carrier.
func WithMetadataPropagator(propagator *metadata.Propagator) SubscriberOption {
	return func(endpoint *SubscriberEndpoint) { endpoint.propagator = propagator }
}

// NewSubscriberEndpoint creates a core.Endpoint that manages a subscription.
func NewSubscriberEndpoint(name string, sub Subscriber, topic string, handler MessageHandler, opts ...SubscriberOption) *SubscriberEndpoint {
	e := &SubscriberEndpoint{
		name:       name,
		sub:        sub,
		topic:      topic,
		handler:    handler,
		propagator: defaultMetadataPropagator,
		ready:      make(chan struct{}), startDone: make(chan struct{}), shutdownDone: make(chan struct{}),
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

var _ core.ReadyEndpoint = (*SubscriberEndpoint)(nil)

// Name returns the endpoint label for logging.
func (e *SubscriberEndpoint) Name() string { return e.name }

// Startup runs the optional connect hook, subscribes, then blocks until
// Shutdown cancels the internal context.
func (e *SubscriberEndpoint) Startup(ctx context.Context) (err error) {
	e.mu.Lock()
	if e.state != "" {
		e.mu.Unlock()
		return fmt.Errorf("endpoint %s already started or stopped", e.name)
	}
	e.state = "starting"
	subCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		if err != nil {
			e.readyErr = err
		} else if subCtx.Err() != nil {
			e.readyErr = subCtx.Err()
		}
		e.once.Do(func() { close(e.ready) })
		e.mu.Unlock()
	}()
	// startDone covers only connect/subscribe, not the blocking serving lifetime.
	started := false
	defer func() {
		if !started {
			close(e.startDone)
		}
	}()
	if err = subCtx.Err(); err != nil {
		return err
	}
	if e.sub == nil || e.handler == nil {
		return fmt.Errorf("subscriber and handler are required")
	}
	if e.onStart != nil {
		if err = e.onStart(subCtx); err != nil {
			return fmt.Errorf("connect for endpoint %s: %w", e.name, err)
		}
	}
	if err = subCtx.Err(); err != nil {
		return err
	}
	handler := e.handler
	if e.propagator != nil {
		handler = withMessageMetadata(e.propagator, handler)
	}
	if err = e.sub.Subscribe(subCtx, e.topic, handler, e.opts...); err != nil {
		return fmt.Errorf("subscribe %s: %w", e.topic, err)
	}
	e.mu.Lock()
	if e.state != "starting" || subCtx.Err() != nil {
		e.mu.Unlock()
		return fmt.Errorf("endpoint %s stopped during startup", e.name)
	}
	e.state = "running"
	e.once.Do(func() { close(e.ready) })
	e.mu.Unlock()
	started = true
	close(e.startDone)
	<-subCtx.Done()
	return nil
}

func (e *SubscriberEndpoint) Ready(ctx context.Context) error {
	select {
	case <-e.ready:
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.readyErr != nil {
			return e.readyErr
		}
		if e.state != "running" {
			return fmt.Errorf("endpoint %s is not running", e.name)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *SubscriberEndpoint) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if e.state != "stopping" && e.state != "stopped" {
		if e.state == "" {
			close(e.startDone)
		}
		e.state = "stopping"
		if e.cancel != nil {
			e.cancel()
		}
		e.once.Do(func() { close(e.ready) })
		budget, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		e.shutdownCancel = cancel
		go func() {
			defer cancel()
			var err error
			if closer, ok := e.sub.(ContextCloser); ok {
				err = closer.CloseContext(budget)
			} else {
				// Legacy SDKs cannot cancel Close: keep one owner, including late startup cleanup.
				<-e.startDone
				if e.onStop != nil {
					err = e.onStop(budget)
				}
				if e.sub != nil {
					err = errors.Join(err, e.sub.Close())
				}
			}
			e.mu.Lock()
			e.shutdownErr = err
			e.state = "stopped"
			e.mu.Unlock()
			close(e.shutdownDone)
		}()
	}
	cancel := e.shutdownCancel
	e.mu.Unlock()
	select {
	case <-e.shutdownDone:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.shutdownErr
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		return ctx.Err()
	}
}

func joinErrors(errs, err error) error {
	if errs == nil {
		return err
	}
	return fmt.Errorf("%v; %v", errs, err)
}
