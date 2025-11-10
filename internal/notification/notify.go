package notification

import (
	"fmt"
	"os"
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
	ClusterName          string   `json:"clusterName"`
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
	if payload.ClusterName == "" {
		payload.ClusterName = os.Getenv("CLUSTER_NAME")
	}

	eventToSend := events.NewNotificationEvent(
		payload,
		nil,
	)

	eventToSend.AddHeaderIfNotExists("source", "namespace-janitor-operator")
	eventToSend.AddHeaderIfNotExists("environment", getEnvironment())
	eventToSend.AddHeaderIfNotExists("timestamp", time.Now().Format(time.RFC3339Nano))
	priority := k.determinePriority(payload.ActionTaken)
	eventToSend.AddHeaderIfNotExists("priority", priority)
	eventToSend.AddHeaderIfNotExists("namespace", payload.NamespaceName)
	eventToSend.AddHeaderIfNotExists("flag", payload.CurrentFlag)
	eventToSend.AddHeaderIfNotExists("cluster", payload.ClusterName)
	eventToSend.AddHeaderIfNotExists("action", payload.ActionTaken)
	eventToSend.AddHeaderIfNotExists("requester", payload.Requester)
	eventToSend.AddHeaderIfNotExists("age", payload.Age)
	eventToSend.AddHeaderIfNotExists("additionalRecipients", fmt.Sprintf("%v", payload.AdditionalRecipients))

	k.logger.Info("Publishing janitor event to Kafka",
		"namespace", payload.NamespaceName,
		"action", payload.ActionTaken,
		"eventUUID", eventToSend.UUID,
	)

	if err := k.EventBus.Publish(*eventToSend); err != nil {
		k.logger.Error(err, "Failed to publish event to Kafka")
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}
func getEnvironment() string {
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		return env
	}
	return "okd4-teh-1"
}

func (k *KafkaNotifier) determinePriority(action string) string {
	switch action {
	case "DeletingNamespace":
		return "critical"
	case "AppliedRedFlag":
		return "high"
	case "AppliedYellowFlag":
		return "medium"
	default:
		return "low"
	}
}

func (k *KafkaNotifier) Close() error {
	k.logger.Info("Closing Kafka notifier connection")
	return k.EventBus.Close()
}
