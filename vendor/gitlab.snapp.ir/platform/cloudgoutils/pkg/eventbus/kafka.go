package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type KafkaConfig struct {
	Broker string `json:"broker,omitempty" koanf:"broker"`
	Topic  string `json:"topic,omitempty" koanf:"topic"`
	Group  string `json:"group,omitempty" koanf:"group"`
}

// KafkaEventBus is the generic Kafka event bus implementation
type KafkaEventBus[T any] struct {
	reader *kafka.Reader
	writer *kafka.Writer
	config KafkaConfig
	logger *zap.Logger
}

// NewKafkaEventBus creates a new generic Kafka event bus
func NewKafkaEventBus[T any](cfg KafkaConfig, logger *zap.Logger) *KafkaEventBus[T] {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{cfg.Broker},
		Topic:       cfg.Topic,
		GroupID:     cfg.Group,
		MinBytes:    1,
		MaxBytes:    10e6,
		StartOffset: kafka.FirstOffset,
		MaxWait:     500 * time.Millisecond,
	})

	writer := &kafka.Writer{
		Addr:     kafka.TCP(cfg.Broker),
		Topic:    cfg.Topic,
		Balancer: &kafka.LeastBytes{},
	}

	logger.Info("🎧 KafkaEventBus initialized",
		zap.String("broker", cfg.Broker),
		zap.String("topic", cfg.Topic),
		zap.String("group", cfg.Group))

	return &KafkaEventBus[T]{
		reader: reader,
		writer: writer,
		config: cfg,
		logger: logger,
	}
}

// Publish publishes a generic event to Kafka
func (k *KafkaEventBus[T]) Publish(event T) error {
	// Serialize the event
	eventData, err := json.Marshal(event)
	if err != nil {
		k.logger.Error("failed to marshal event", zap.Error(err))
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Create Kafka message
	message := kafka.Message{
		Value: eventData,
		Headers: []kafka.Header{
			{
				Key:   "event-type",
				Value: []byte(fmt.Sprintf("%T", event)),
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = k.writer.WriteMessages(ctx, message)
	if err != nil {
		k.logger.Error("failed to publish message to Kafka", zap.Error(err))
		return fmt.Errorf("failed to publish message to Kafka: %w", err)
	}

	k.logger.Info("✅ Event published to Kafka",
		zap.String("topic", k.config.Topic),
		zap.Int("message_size", len(eventData)))

	return nil
}

// Subscribe subscribes to generic events from Kafka
func (k *KafkaEventBus[T]) Subscribe() (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kafkaMessage, err := k.reader.ReadMessage(ctx)
	if err != nil {
		// Don't log timeout errors as they are normal when no messages are available
		if strings.Contains(err.Error(), "context deadline exceeded") {
			var zero T
			return zero, fmt.Errorf("error reading message from Kafka: %w", err)
		}
		k.logger.Error("error reading message from Kafka", zap.Error(err))
		var zero T
		return zero, fmt.Errorf("error reading message from Kafka: %w", err)
	}

	// Deserialize the event
	var event T
	if err := json.Unmarshal(kafkaMessage.Value, &event); err != nil {
		k.logger.Error("failed to unmarshal event", zap.Error(err))
		var zero T
		return zero, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	k.logger.Info("📨 Event received from Kafka",
		zap.String("topic", k.config.Topic),
		zap.Int("message_size", len(kafkaMessage.Value)))

	return event, nil
}

// SubscribeWithHandler subscribes to events with a generic handler
func (k *KafkaEventBus[T]) SubscribeWithHandler(ctx context.Context, handler func(T) error) error {
	k.logger.Info("starting Kafka consumer...")

	for {
		select {
		case <-ctx.Done():
			k.logger.Info("context cancelled, stopping consumer")
			return ctx.Err()
		default:
			event, err := k.Subscribe()
			if err != nil {
				// Check if it's a timeout error (normal when no messages)
				if strings.Contains(err.Error(), "context deadline exceeded") {
					// This is normal behavior when there are no messages
					// Just continue the loop without logging an error
					continue
				}
				k.logger.Error("error in subscription", zap.Error(err))
				continue
			}

			if err := handler(event); err != nil {
				k.logger.Error("handler error", zap.Error(err))
			}
		}
	}
}

// Close closes the Kafka connections
func (k *KafkaEventBus[T]) Close() error {
	var errs []error

	// Close reader
	if err := k.reader.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close reader: %w", err))
	}

	// Close writer
	if err := k.writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close writer: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing Kafka connections: %v", errs)
	}

	k.logger.Info("🔌 KafkaEventBus connections closed")
	return nil
}
