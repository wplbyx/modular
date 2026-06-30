package kafka

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"modular/packages/log"
	"modular/packages/transport/pubsub"
)

// Ensure Consumer implements pubsub.Subscriber interface
var _ pubsub.Subscriber = (*Consumer)(nil)

// Consumer implements pubsub.Subscriber using Kafka
type Consumer struct {
	reader      *kafka.Reader
	opts        *ConsumerOptions
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	handlers    sync.Map // topic -> pubsub.MessageHandler
	dlqProducer *Producer
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(opts ...ConsumerOption) (*Consumer, error) {
	o := DefaultConsumerOptions()
	for _, opt := range opts {
		opt(o)
	}

	if len(o.Brokers) == 0 {
		return nil, fmt.Errorf("kafka brokers cannot be empty")
	}
	if o.Topic == "" {
		return nil, fmt.Errorf("kafka topic cannot be empty")
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        o.Brokers,
		GroupID:        o.GroupID,
		Topic:          o.Topic,
		MinBytes:       o.MinBytes,
		MaxBytes:       o.MaxBytes,
		StartOffset:    o.StartOffset,
		CommitInterval: o.CommitInterval,
	})

	dlqProducer, err := newDLQProducer(o)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}

	return &Consumer{
		reader:      reader,
		opts:        o,
		dlqProducer: dlqProducer,
	}, nil
}

// Subscribe subscribes to a topic with a handler
// Note: For Kafka, the topic is typically set at consumer creation time
// This method starts consuming messages from the configured topic
func (c *Consumer) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	// Store handler for the topic
	c.handlers.Store(topic, handler)

	workers := c.opts.Workers
	if workers <= 0 {
		workers = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	for i := 0; i < workers; i++ {
		c.wg.Add(1)
		go c.consumeLoop(ctx, topic, handler, i)
	}

	log.Infof("Kafka consumer subscribed to topic: %s", topic)
	return nil
}

// Unsubscribe unsubscribes from a topic
func (c *Consumer) Unsubscribe(ctx context.Context, topic string) error {
	c.handlers.Delete(topic)
	log.Infof("Kafka consumer unsubscribed from topic: %s", topic)
	return nil
}

// Close closes the consumer
func (c *Consumer) Close() error {
	log.Info("Kafka consumer closing...")

	if c.cancel != nil {
		c.cancel()
	}

	if err := c.reader.Close(); err != nil {
		log.Errorf("Kafka consumer close reader failed: %v", err)
		return err
	}

	c.wg.Wait()
	if c.dlqProducer != nil {
		return c.dlqProducer.Close()
	}
	return nil
}

func (c *Consumer) consumeLoop(ctx context.Context, topic string, handler pubsub.MessageHandler, workerID int) {
	defer c.wg.Done()
	log.Infof("Kafka consumer worker %d started for topic %s", workerID, topic)

	for {
		select {
		case <-ctx.Done():
			log.Infof("Kafka consumer worker %d for topic %s stopping", workerID, topic)
			return
		default:
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Errorf("Kafka worker %d failed to fetch message: %v", workerID, err)
				time.Sleep(10 * time.Millisecond)
				continue
			}

			c.handleMessage(ctx, workerID, handler, msg)
		}
	}
}

func (c *Consumer) handleMessage(ctx context.Context, workerID int, handler pubsub.MessageHandler, msg kafka.Message) {
	message := messageFromKafka(msg)
	var handlerErr error

	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		handlerErr = handler(ctx, message)
		if handlerErr == nil {
			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				log.Errorf("Kafka worker %d failed to commit offset: %v", workerID, err)
			}
			return
		}

		if attempt < c.opts.MaxRetries {
			log.Warnf("Kafka worker %d handler error for topic %s, retry %d/%d: %v",
				workerID, msg.Topic, attempt+1, c.opts.MaxRetries, handlerErr)
			if !sleepWithContext(ctx, c.opts.RetryBackoff) {
				return
			}
		}
	}

	log.Warnf("Kafka worker %d exhausted retries for topic %s: %v", workerID, msg.Topic, handlerErr)
	if c.dlqProducer == nil || c.opts.DLQTopic == "" {
		return
	}

	if err := c.sendToDLQ(ctx, msg, handlerErr); err != nil {
		log.Errorf("Kafka worker %d failed to send message to DLQ: %v", workerID, err)
		return
	}

	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		log.Errorf("Kafka worker %d failed to commit DLQ'd message offset: %v", workerID, err)
	}
}

func newDLQProducer(opts *ConsumerOptions) (*Producer, error) {
	if opts.DLQTopic == "" {
		return nil, nil
	}

	producerOpts := opts.DLQProducer
	if producerOpts == nil {
		producerOpts = &ProducerOptions{
			Brokers: opts.Brokers,
			Topic:   opts.DLQTopic,
		}
	}
	if len(producerOpts.Brokers) == 0 {
		producerOpts.Brokers = opts.Brokers
	}
	if producerOpts.Topic == "" {
		producerOpts.Topic = opts.DLQTopic
	}

	options := []ProducerOption{
		WithBrokers(producerOpts.Brokers...),
		WithTopic(producerOpts.Topic),
	}
	if producerOpts.BatchSize > 0 {
		options = append(options, WithBatchSize(producerOpts.BatchSize))
	}
	if producerOpts.BatchTimeout > 0 {
		options = append(options, WithBatchTimeout(producerOpts.BatchTimeout))
	}
	return NewProducer(options...)
}

func (c *Consumer) sendToDLQ(ctx context.Context, msg kafka.Message, reason error) error {
	headers := map[string]string{
		"x-original-topic":     msg.Topic,
		"x-original-partition": fmt.Sprintf("%d", msg.Partition),
		"x-original-offset":    fmt.Sprintf("%d", msg.Offset),
		"x-error":              reason.Error(),
	}
	for _, header := range msg.Headers {
		headers[header.Key] = string(header.Value)
	}
	return c.dlqProducer.Publish(ctx, c.opts.DLQTopic, msg.Value, pubsub.WithKey(string(msg.Key)), pubsub.WithHeaders(headers))
}

func messageFromKafka(msg kafka.Message) pubsub.Message {
	message := pubsub.Message{
		Topic:   msg.Topic,
		Payload: msg.Value,
		Key:     string(msg.Key),
		Headers: make(map[string]string, len(msg.Headers)),
	}
	for _, h := range msg.Headers {
		message.Headers[h.Key] = string(h.Value)
	}
	return message
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Ensure Consumer implements io.Closer
var _ io.Closer = (*Consumer)(nil)
