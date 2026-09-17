package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
)

// Ensure Consumer implements pubsub.Subscriber interface
var _ pubsub.Subscriber = (*Consumer)(nil)

// Consumer implements pubsub.Subscriber using Kafka
type Consumer struct {
	reader      ConsumerReader
	opts        *ConsumerOptions
	mu          sync.Mutex
	started     bool
	closed      bool
	closeOnce   sync.Once
	closeErr    error
	cancel      context.CancelFunc
	wg          sync.WaitGroup
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

	if o.Workers < 1 || o.MaxRetries < 0 {
		return nil, errors.New("invalid Kafka workers or retry count")
	}
	var reader ConsumerReader = o.Reader
	if reader == nil {
		reader = kafka.NewReader(kafka.ReaderConfig{
			Brokers:        o.Brokers,
			GroupID:        o.GroupID,
			Topic:          o.Topic,
			MinBytes:       o.MinBytes,
			MaxBytes:       o.MaxBytes,
			StartOffset:    o.StartOffset,
			CommitInterval: o.CommitInterval,
		})
	}

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
	if err := ctx.Err(); err != nil {
		return err
	}
	if handler == nil || topic != c.opts.Topic {
		return errors.New("Kafka subscription requires configured topic and handler")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.started {
		return errors.New("Kafka consumer is closed or already subscribed")
	}
	ctx, c.cancel = context.WithCancel(ctx)
	c.started = true
	handler = pubsub.WithMessageMetadata(handler)
	queues := make([]chan kafka.Message, c.opts.Workers)
	for i := range queues {
		queues[i] = make(chan kafka.Message, 1)
		c.wg.Add(1)
		go func(worker int) {
			defer c.wg.Done()
			for msg := range queues[worker] {
				if ctx.Err() != nil {
					return
				}
				c.handleMessage(ctx, worker, handler, msg)
			}
		}(i)
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			for _, queue := range queues {
				close(queue)
			}
		}()
		attempt := 0
		for {
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error(ctx, "Kafka consumer fetch failed", zap.Error(err))
				if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
					return
				}
				attempt++
				continue
			}
			attempt = 0
			index := msg.Partition % len(queues)
			if index < 0 {
				index = 0
			}
			select {
			case queues[index] <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (c *Consumer) Unsubscribe(ctx context.Context, topic string) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Consumer) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		if c.cancel != nil {
			c.cancel()
		}
		c.mu.Unlock()
		c.closeErr = c.reader.Close()
		c.wg.Wait()
		if c.dlqProducer != nil {
			c.closeErr = errors.Join(c.closeErr, c.dlqProducer.Close())
		}
	})
	return c.closeErr
}

func (c *Consumer) handleMessage(ctx context.Context, workerID int, handler pubsub.MessageHandler, msg kafka.Message) {
	message := messageFromKafka(msg)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		handlerErr := delivery.CallHandler(ctx, handler, message)
		if handlerErr == nil {
			break
		}
		log.Warn(ctx, "Kafka handler failed; retaining message", zap.Int64("offset", msg.Offset), zap.Error(handlerErr))
		if attempt >= c.opts.MaxRetries && c.dlqProducer != nil && c.opts.DLQTopic != "" {
			for dlqAttempt := 0; ; dlqAttempt++ {
				if err := c.sendToDLQ(ctx, msg, handlerErr); err == nil {
					break
				}
				if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, dlqAttempt) {
					return
				}
			}
			break
		}
		if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
			return
		}
		attempt++
	}
	if c.opts.GroupID == "" {
		return
	}
	for attempt := 0; ; attempt++ {
		if err := c.reader.CommitMessages(ctx, msg); err == nil {
			return
		} else {
			log.Error(ctx, "Kafka offset commit failed; retrying", zap.Error(err))
		}
		if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
			return
		}
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

// Ensure Consumer implements io.Closer
var _ io.Closer = (*Consumer)(nil)
