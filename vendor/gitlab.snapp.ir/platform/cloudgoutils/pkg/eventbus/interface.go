package eventbus

import (
	"context"
)

// EventBus is the generic interface for event bus operations
type EventBus[T any] interface {
	Publish(event T) error
	Subscribe() (T, error)
	SubscribeWithHandler(ctx context.Context, handler func(T) error) error
	Close() error
}
