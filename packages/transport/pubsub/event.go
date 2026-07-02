package pubsub

import "context"

// Event is a transport-level domain event carried by pub/sub infrastructure.
type Event interface {
	GetID() string
	GetType() string
	GetData() []byte
	GetMetadata() map[string]string
}

// EventHandler handles a typed event from a pub/sub topic.
type EventHandler func(ctx context.Context, event Event) error

// BaseEvent is the default Event implementation.
type BaseEvent struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

func NewBaseEvent(id, eventType string, data []byte, metadata map[string]string) *BaseEvent {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	return &BaseEvent{
		ID:       id,
		Type:     eventType,
		Data:     data,
		Metadata: metadata,
	}
}

func (e *BaseEvent) GetID() string {
	return e.ID
}

func (e *BaseEvent) GetType() string {
	return e.Type
}

func (e *BaseEvent) GetData() []byte {
	return e.Data
}

func (e *BaseEvent) GetMetadata() map[string]string {
	return e.Metadata
}
// EventFromMessage builds a BaseEvent from a transport-level Message.
// The message Topic becomes the event Type; headers are carried as metadata.
func EventFromMessage(msg Message) *BaseEvent {
	return NewBaseEvent(msg.Key, msg.Topic, msg.Payload, msg.Headers)
}

// AsMessageHandler adapts an EventHandler into a MessageHandler, converting
// each incoming Message to an Event before dispatching.
func AsMessageHandler(h EventHandler) MessageHandler {
	return func(ctx context.Context, msg Message) error {
		return h(ctx, EventFromMessage(msg))
	}
}
