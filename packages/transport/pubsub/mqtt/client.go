package mqtt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
)

// Ensure MQTTClient implements pubsub.Client interface
var _ pubsub.Client = (*MQTTClient)(nil)

// MQTTClient implements pubsub.Client using MQTT protocol
type MQTTClient struct {
	client paho.Client
	opts   *Options

	mu             sync.RWMutex
	opGate         chan struct{}
	subscriptions  map[string]*subscription
	queue          *delivery.Queue
	stop           chan struct{}
	done           chan struct{}
	closed         bool
	restoring      bool
	restorePending bool
	closeErr       error
}

type subscription struct {
	handler pubsub.MessageHandler
	qos     byte
	ctx     context.Context
}

// NewClient creates a new MQTT pubsub client
func NewClient(opts ...Option) (*MQTTClient, error) {
	o := DefaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	if o.BrokerURL == "" {
		return nil, fmt.Errorf("broker URL is required")
	}

	// Create paho options
	pahoOpts := paho.NewClientOptions()
	pahoOpts.AddBroker(o.BrokerURL)
	pahoOpts.SetClientID(o.ClientID)
	pahoOpts.SetUsername(o.Username)
	pahoOpts.SetPassword(o.Password)
	pahoOpts.SetConnectTimeout(o.ConnectTimeout)
	pahoOpts.SetWriteTimeout(o.WriteTimeout)
	pahoOpts.SetKeepAlive(o.KeepAlive)
	pahoOpts.SetPingTimeout(o.PingTimeout)
	pahoOpts.SetMaxReconnectInterval(o.MaxReconnectDelay)
	pahoOpts.SetAutoReconnect(o.AutoReconnect)
	pahoOpts.SetCleanSession(o.CleanSession)
	// SDK callbacks only enqueue; business ordering is controlled by workers.
	pahoOpts.SetOrderMatters(true)
	pahoOpts.SetAutoAckDisabled(true)

	if o.TLSConfig != nil {
		pahoOpts.SetTLSConfig(o.TLSConfig)
	}

	// Create client instance for use in handlers
	mqttClient := &MQTTClient{
		opts: o, opGate: make(chan struct{}, 1), subscriptions: make(map[string]*subscription), stop: make(chan struct{}), done: make(chan struct{}),
	}

	// Set up connection handler to restore subscriptions
	pahoOpts.OnConnect = func(c paho.Client) {
		log.Info(context.Background(), "MQTT connected", zap.String("broker", o.BrokerURL))
		mqttClient.restore(c)

		// Call custom handler if set
		if o.OnConnectHandler != nil {
			o.OnConnectHandler(c)
		}
	}

	// Set connection lost handler
	if o.ConnectionLostHandler != nil {
		pahoOpts.OnConnectionLost = o.ConnectionLostHandler
	} else {
		pahoOpts.OnConnectionLost = func(c paho.Client, err error) {
			log.Warn(context.Background(), "MQTT connection lost", zap.Error(err))
		}
	}

	// Persistent sessions may deliver before Subscribe installs a route. Retain
	// these deliveries in the same bounded queue until a matching handler exists.
	pahoOpts.SetDefaultPublishHandler(func(client paho.Client, msg paho.Message) {
		mqttClient.mu.RLock()
		q := mqttClient.queue
		closed := mqttClient.closed
		mqttClient.mu.RUnlock()
		if q == nil || closed {
			return
		}
		copied := &copiedMessage{Message: msg, payload: append([]byte(nil), msg.Payload()...)}
		_ = q.Submit(context.Background(), func(ctx context.Context) error {
			if o.DefaultMessageHandler != nil {
				o.DefaultMessageHandler(client, copied)
				return nil
			}
			mqttClient.mu.RLock()
			var matched *subscription
			for filter, sub := range mqttClient.subscriptions {
				if matchTopic(filter, msg.Topic()) {
					matched = sub
					break
				}
			}
			mqttClient.mu.RUnlock()
			if matched == nil {
				return fmt.Errorf("no handler for MQTT topic %s", msg.Topic())
			}
			handlerCtx, cancel := context.WithCancel(matched.ctx)
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			defer cancel()
			return matched.handler(handlerCtx, pubsub.Message{Topic: msg.Topic(), Payload: copied.payload})
		}, msg.Ack, true)
	})

	// Set will message
	if o.WillTopic != "" {
		pahoOpts.SetWill(o.WillTopic, o.WillPayload, o.WillQos, o.WillRetained)
	}

	// Set store
	if o.Store != nil {
		pahoOpts.SetStore(o.Store)
	}

	mqttClient.client = paho.NewClient(pahoOpts)
	return mqttClient, nil
}

// Connect establishes connection to the MQTT broker.
func (c *MQTTClient) Connect(ctx context.Context) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-c.opGate }()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return delivery.ErrClosed
	}
	if c.queue == nil {
		workers := c.opts.Workers
		if c.opts.OrderMatters {
			workers = 1
		}
		c.queue = delivery.NewQueue(workers, c.opts.QueueSize)
	}
	c.mu.Unlock()
	if c.client.IsConnected() {
		return nil
	}
	err := c.wait(ctx, c.client.Connect())
	if err != nil {
		closeCtx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = c.CloseContext(closeCtx)
	}
	return err
}

// Disconnect permanently stops admission and drains accepted deliveries.
func (c *MQTTClient) Disconnect(ctx context.Context) error { return c.CloseContext(ctx) }
func (c *MQTTClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.client.IsConnected()
}

func (c *MQTTClient) wait(ctx context.Context, token paho.Token) error {
	select {
	case <-token.Done():
		select {
		case <-c.stop:
			return delivery.ErrClosed
		default:
			return token.Error()
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stop:
		return delivery.ErrClosed
	}
}

func (c *MQTTClient) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return delivery.ErrClosed
	}
	o := &pubsub.PublishOptions{QoS: c.opts.DefaultQos, Retained: c.opts.DefaultRetained}
	for _, opt := range opts {
		opt(o)
	}
	return c.wait(ctx, c.client.Publish(topic, o.QoS, o.Retained, payload))
}

func (c *MQTTClient) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	if topic == "" || handler == nil {
		return fmt.Errorf("topic and handler are required")
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-c.opGate }()
	o := &pubsub.SubscribeOptions{QoS: c.opts.DefaultQos}
	for _, opt := range opts {
		opt(o)
	}
	if o.QoS > 2 {
		return fmt.Errorf("invalid MQTT QoS: %d", o.QoS)
	}
	sub := &subscription{handler: pubsub.WithMessageMetadata(handler), qos: o.QoS, ctx: context.WithoutCancel(ctx)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return delivery.ErrClosed
	}
	if _, exists := c.subscriptions[topic]; exists {
		c.mu.Unlock()
		return fmt.Errorf("topic already subscribed: %s", topic)
	}
	if c.queue == nil {
		workers := c.opts.Workers
		if c.opts.OrderMatters {
			workers = 1
		}
		c.queue = delivery.NewQueue(workers, c.opts.QueueSize)
	}
	c.subscriptions[topic] = sub
	c.mu.Unlock()
	if err := c.wait(ctx, c.client.Subscribe(topic, sub.qos, c.adaptSubscription(sub))); err != nil {
		c.mu.Lock()
		delete(c.subscriptions, topic)
		c.mu.Unlock()
		// Remove the route even when a broker acknowledgement arrives late.
		c.client.Unsubscribe(topic)
		return err
	}
	return nil
}

func (c *MQTTClient) SubscribeMultiple(ctx context.Context, subscriptions map[string]pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	for topic, handler := range subscriptions {
		if err := c.Subscribe(ctx, topic, handler, opts...); err != nil {
			return err
		}
	}
	return nil
}

func (c *MQTTClient) Unsubscribe(ctx context.Context, topic string) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-c.opGate }()
	c.mu.Lock()
	delete(c.subscriptions, topic)
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return delivery.ErrClosed
	}
	return c.wait(ctx, c.client.Unsubscribe(topic))
}

func (c *MQTTClient) closeTimeout() time.Duration {
	if c.opts.CloseTimeout <= 0 {
		return 30 * time.Second
	}
	return c.opts.CloseTimeout
}
func (c *MQTTClient) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.closeTimeout())
	defer cancel()
	return c.CloseContext(ctx)
}
func (c *MQTTClient) CloseContext(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.stop)
		q := c.queue
		if q != nil {
			q.Stop()
		}
		go func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), c.closeTimeout())
			defer cancel()
			var err error
			if q != nil {
				err = q.Close(closeCtx)
			}
			c.client.Disconnect(250)
			c.mu.Lock()
			c.closeErr = err
			c.mu.Unlock()
			close(c.done)
		}()
	}
	q := c.queue
	c.mu.Unlock()
	select {
	case <-c.done:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.closeErr
	case <-ctx.Done():
		if q != nil {
			_ = q.Close(ctx)
		}
		return ctx.Err()
	}
}
func (c *MQTTClient) Stats() pubsub.DeliveryStats {
	c.mu.RLock()
	q := c.queue
	c.mu.RUnlock()
	if q == nil {
		return pubsub.DeliveryStats{}
	}
	return q.Stats()
}

// restore serializes broker operations and rechecks subscriptions after reconnect.
func (c *MQTTClient) restore(client paho.Client) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		client.Disconnect(0)
		return
	}
	c.restorePending = true
	if c.restoring {
		c.mu.Unlock()
		return
	}
	c.restoring = true
	c.mu.Unlock()
	go func() {
		for {
			c.mu.Lock()
			c.restorePending = false
			c.mu.Unlock()
			opCtx, cancel := context.WithTimeout(context.Background(), c.opts.ConnectTimeout)
			err := c.acquire(opCtx)
			if err == nil {
				c.mu.RLock()
				snapshot := make(map[string]*subscription, len(c.subscriptions))
				for k, v := range c.subscriptions {
					snapshot[k] = v
				}
				c.mu.RUnlock()
				for topic, sub := range snapshot {
					err = c.wait(opCtx, client.Subscribe(topic, sub.qos, c.adaptSubscription(sub)))
					if err != nil {
						if !errors.Is(err, delivery.ErrClosed) {
							log.Warn(opCtx, "MQTT resubscribe failed", zap.String("topic", topic), zap.Error(err))
						}
						break
					}
				}
				<-c.opGate
			}
			cancel()
			c.mu.Lock()
			if c.closed || !c.restorePending {
				c.restoring = false
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
		}
	}()
}

// Endpoint returns the broker URL.
func (c *MQTTClient) Endpoint() (*url.URL, error) { return url.Parse(c.opts.BrokerURL) }

// copiedMessage prevents SDK-owned payload reuse during asynchronous processing.
type copiedMessage struct {
	paho.Message
	payload []byte
}

func (m *copiedMessage) Payload() []byte { return m.payload }

// Ack is owned by the delivery boundary, including for legacy raw handlers.
func (m *copiedMessage) Ack() {}

func (c *MQTTClient) adaptSubscription(sub *subscription) paho.MessageHandler {
	return func(_ paho.Client, msg paho.Message) {
		c.mu.RLock()
		q := c.queue
		closed := c.closed
		c.mu.RUnlock()
		if q == nil || closed {
			return
		}
		message := pubsub.Message{Topic: msg.Topic(), Payload: append([]byte(nil), msg.Payload()...)}
		_ = q.Submit(sub.ctx, func(ctx context.Context) error { return sub.handler(ctx, message) }, msg.Ack, true)
	}
}

func matchTopic(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") && !strings.HasPrefix(filter, "$") {
		return false
	}
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, part := range f {
		if part == "#" {
			return i == len(f)-1
		}
		if i >= len(t) || (part != "+" && part != t[i]) {
			return false
		}
	}
	return len(f) == len(t)
}

func (c *MQTTClient) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.opGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stop:
		return delivery.ErrClosed
	}
	select {
	case <-c.stop:
		<-c.opGate
		return delivery.ErrClosed
	default:
		return nil
	}
}
