package events

import (
	"fmt"

	"github.com/google/uuid"
)

const (
	HeaderEventName string = "event-name"
	HeaderEventUUID string = "event-id"
)

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type EventType string

const (
	EventTypeNotification EventType = "notification"
)

type Event[T any] struct {
	H    []Header  `json:"headers"`
	Body T         `json:"body"`
	Type EventType `json:"type"`
	UUID string    `json:"uuid"`
}

// NewEvent creates a new event with explicit type specification
func NewEvent[T any](msg T, eventType EventType, headers []Header) *Event[T] {
	event := &Event[T]{
		H:    headers,
		Body: msg,
		Type: eventType,
		UUID: calculateUUID(),
	}

	// Add default headers
	event.AddHeaderIfNotExists(HeaderEventName, string(eventType))
	event.AddHeaderIfNotExists(HeaderEventUUID, event.UUID)

	return event
}

// NewNotificationEvent creates a notification event with the given message
func NewNotificationEvent[T any](msg T, headers []Header) *Event[T] {
	return NewEvent(msg, EventTypeNotification, headers)
}

// GetEventTypeFromHeaders extracts the event type from headers
func GetEventTypeFromHeaders(headers []Header) (EventType, error) {
	for _, header := range headers {
		if header.Key == HeaderEventName {
			return EventType(header.Value), nil
		}
	}
	return "", fmt.Errorf("event type not found in headers")
}

// IsNotificationEvent checks if the event is a notification event based on headers
func IsNotificationEvent(headers []Header) bool {
	eventType, err := GetEventTypeFromHeaders(headers)
	if err != nil {
		return false
	}
	return eventType == EventTypeNotification
}

func (e *Event[T]) GetName() (string, error) {
	for _, k := range e.H {
		if k.Key == HeaderEventName {
			return k.Value, nil
		}
	}
	return "", fmt.Errorf("no such header %s", HeaderEventName)
}

func (e *Event[T]) GetUUID() (string, error) {
	for _, k := range e.H {
		if k.Key == HeaderEventUUID {
			return k.Value, nil
		}
	}
	return "", fmt.Errorf("no such header %s", HeaderEventUUID)
}

func (e *Event[T]) GetBody() T {
	return e.Body
}

// GetType returns the event type
func (e *Event[T]) GetType() EventType {
	return e.Type
}

func (e *Event[T]) AddHeaderIfNotExists(key, value string) {
	headerExists := false
	for _, k := range e.H {
		if k.Key == key {
			headerExists = true
			break
		}
	}
	if !headerExists {
		e.H = append(e.H, Header{Key: key, Value: value})
	}
}

func calculateUUID() string {
	return uuid.New().String()
}

type LegacyEvent struct {
	H    []Header
	Body interface{}
}
