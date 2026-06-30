package mqtt

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	paho "github.com/eclipse/paho.mqtt.golang"

	"modular/packages/log"
	"modular/packages/transport/pubsub"
)

// Ensure MQTTClient implements pubsub.Client interface
var _ pubsub.Client = (*MQTTClient)(nil)

// MQTTClient implements pubsub.Client using MQTT protocol
type MQTTClient struct {
	client paho.Client
	opts   *Options

	mu            sync.RWMutex
	subscriptions sync.Map // topic -> pubsub.MessageHandler
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
	pahoOpts.SetOrderMatters(o.OrderMatters)

	if o.TLSConfig != nil {
		pahoOpts.SetTLSConfig(o.TLSConfig)
	}

	// Create client instance for use in handlers
	mqttClient := &MQTTClient{
		opts: o,
	}

	// Set up connection handler to restore subscriptions
	pahoOpts.OnConnect = func(c paho.Client) {
		log.Infof("[MQTT] Connected to broker: %s", o.BrokerURL)
		mqttClient.subscriptions.Range(func(key, value interface{}) bool {
			topic := key.(string)
			handler := value.(pubsub.MessageHandler)
			go func() {
				if token := c.Subscribe(topic, o.DefaultQos, mqttClient.adaptHandler(handler)); token.Wait() && token.Error() != nil {
					log.Warnf("[MQTT] Failed to resubscribe %s: %v", topic, token.Error())
				} else {
					log.Infof("[MQTT] Resubscribed to %s", topic)
				}
			}()
			return true
		})

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
			log.Warnf("[MQTT] Connection lost: %v", err)
		}
	}

	// Set default message handler
	if o.DefaultMessageHandler != nil {
		pahoOpts.SetDefaultPublishHandler(o.DefaultMessageHandler)
	}

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

// Connect establishes connection to the MQTT broker
func (c *MQTTClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client.IsConnected() {
		return nil
	}

	token := c.client.Connect()
	if token.Error() != nil {
		return fmt.Errorf("failed to connect to broker: %w", token.Error())
	}

	select {
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("connection failed: %w", token.Error())
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Disconnect disconnects from the MQTT broker
func (c *MQTTClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.client.IsConnected() {
		return nil
	}

	// Wait for quiescence (250ms) before disconnecting
	const quiescence = 250
	c.client.Disconnect(quiescence)

	return nil
}

// IsConnected returns whether the client is connected
func (c *MQTTClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client.IsConnected()
}

// Publish publishes a message to a topic
func (c *MQTTClient) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Apply options
	publishOpts := &pubsub.PublishOptions{
		QoS:      c.opts.DefaultQos,
		Retained: c.opts.DefaultRetained,
	}
	for _, opt := range opts {
		opt(publishOpts)
	}

	token := c.client.Publish(topic, publishOpts.QoS, publishOpts.Retained, payload)
	if token.Error() != nil {
		return fmt.Errorf("failed to publish message: %w", token.Error())
	}

	select {
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("publish failed: %w", token.Error())
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe subscribes to a topic with a handler
func (c *MQTTClient) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Apply options
	subscribeOpts := &pubsub.SubscribeOptions{
		QoS: c.opts.DefaultQos,
	}
	for _, opt := range opts {
		opt(subscribeOpts)
	}

	// Store subscription for reconnection
	c.subscriptions.Store(topic, handler)

	// Adapt handler to paho format
	pahoHandler := c.adaptHandler(handler)

	token := c.client.Subscribe(topic, subscribeOpts.QoS, pahoHandler)
	if token.Error() != nil {
		return fmt.Errorf("failed to subscribe: %w", token.Error())
	}

	select {
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("subscription failed: %w", token.Error())
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SubscribeMultiple subscribes to multiple topics with their own handlers.
func (c *MQTTClient) SubscribeMultiple(ctx context.Context, subscriptions map[string]pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	for topic, handler := range subscriptions {
		if err := c.Subscribe(ctx, topic, handler, opts...); err != nil {
			return err
		}
	}
	return nil
}

// Unsubscribe unsubscribes from a topic
func (c *MQTTClient) Unsubscribe(ctx context.Context, topic string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Remove stored subscription
	c.subscriptions.Delete(topic)

	token := c.client.Unsubscribe(topic)
	if token.Error() != nil {
		return fmt.Errorf("failed to unsubscribe: %w", token.Error())
	}

	select {
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("unsubscription failed: %w", token.Error())
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the client (alias for Disconnect)
func (c *MQTTClient) Close() error {
	return c.Disconnect(context.Background())
}

// Endpoint returns the broker URL.
func (c *MQTTClient) Endpoint() (*url.URL, error) {
	return url.Parse(c.opts.BrokerURL)
}

// adaptHandler adapts pubsub.MessageHandler to paho.MessageHandler
func (c *MQTTClient) adaptHandler(handler pubsub.MessageHandler) paho.MessageHandler {
	return func(client paho.Client, msg paho.Message) {
		go func() {
			message := pubsub.Message{
				Topic:   msg.Topic(),
				Payload: msg.Payload(),
			}
			if err := handler(context.Background(), message); err != nil {
				log.Warnf("[MQTT] Handler error for topic [%s]: %v", msg.Topic(), err)
			}
			msg.Ack()
		}()
	}
}
