package notification

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"gitlab.snapp.ir/platform/cloudgoutils/pkg/eventbus"
	"gitlab.snapp.ir/platform/cloudgoutils/pkg/events"
	"go.uber.org/zap"
)

type JanitorPayload struct {
	NamespaceName        string   `json:"namespace"`
	CurrentFlag          string   `json:"currentFlag"`
	ActionTaken          string   `json:"actionTaken"`
	Age                  string   `json:"age"`
	Requester            string   `json:"requester"`
	AdditionalRecipients []string `json:"additionalRecipients"`
	Region               string   `json:"region"`
}

type JanitorEvent = events.Event[JanitorPayload]

type Notifier interface {
	Send(payload JanitorPayload) error
	Close() error
}

type KafkaNotifier struct {
	EventBus eventbus.EventBus[JanitorEvent]
	logger   logr.Logger
}

func New(kafkaCfg eventbus.KafkaConfig, zapLogger *zap.Logger, logrLogger logr.Logger) (Notifier, error) {
	if len(kafkaCfg.Broker) == 0 || kafkaCfg.Topic == "" {
		return nil, fmt.Errorf("kafka broker and topic must be configured")
	}

	bus := eventbus.NewKafkaEventBus[JanitorEvent](kafkaCfg, zapLogger)

	logrLogger.Info("Kafka notifier initialized successfully",
		"broker", kafkaCfg.Broker,
		"topic", kafkaCfg.Topic,
	)

	return &KafkaNotifier{
		EventBus: bus,
		logger:   logrLogger,
	}, nil
}

func (k *KafkaNotifier) Send(payload JanitorPayload) error {
	region := payload.Region
	if region == "" {
		region = "unknown"
	}

	eventToSend := events.NewNotificationEvent(
		payload,
		nil,
	)

	eventToSend.AddHeaderIfNotExists("source", "namespace-janitor-operator")
	eventToSend.AddHeaderIfNotExists("region", region)
	eventToSend.AddHeaderIfNotExists("timestamp", time.Now().Format(time.RFC3339Nano))
	eventToSend.AddHeaderIfNotExists("priority", k.determinePriority(payload))
	eventToSend.AddHeaderIfNotExists("namespace", payload.NamespaceName)
	eventToSend.AddHeaderIfNotExists("flag", payload.CurrentFlag)
	eventToSend.AddHeaderIfNotExists("action", payload.ActionTaken)
	eventToSend.AddHeaderIfNotExists("requester", payload.Requester)
	eventToSend.AddHeaderIfNotExists("age", payload.Age)
	eventToSend.AddHeaderIfNotExists("additionalRecipients", fmt.Sprintf("%v", payload.AdditionalRecipients))

	k.logger.Info("Publishing janitor event to Kafka",
		"namespace", payload.NamespaceName,
		"action", payload.ActionTaken,
		"region", region,
		"eventUUID", eventToSend.UUID,
	)

	if err := k.EventBus.Publish(*eventToSend); err != nil {
		k.logger.Error(err, "Failed to publish event to Kafka")
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}

func (k *KafkaNotifier) determinePriority(payload JanitorPayload) string {
	switch payload.CurrentFlag {
	case "red":
		return "high"
	case "yellow":
		return "medium"
	default:
		// Creation, FinalWarning, ScalingDownWorkloads, NamespaceClaimed
		switch payload.ActionTaken {
		case "ScalingDownWorkloads":
			return "critical"
		case "FinalWarning":
			return "high"
		default:
			return "low"
		}
	}
}

func (k *KafkaNotifier) Close() error {
	k.logger.Info("Closing Kafka notifier connection")
	return k.EventBus.Close()
}
