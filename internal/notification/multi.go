package notification

import "github.com/go-logr/logr"

// MultiNotifier fans out notifications to multiple Notifier implementations.
type MultiNotifier struct {
	notifiers []Notifier
	logger    logr.Logger
}

// NewMultiNotifier creates a notifier that sends to all provided notifiers.
func NewMultiNotifier(notifiers []Notifier, logger logr.Logger) *MultiNotifier {
	return &MultiNotifier{
		notifiers: notifiers,
		logger:    logger,
	}
}

func (m *MultiNotifier) Send(payload JanitorPayload) error {
	var firstErr error
	for _, n := range m.notifiers {
		if err := n.Send(payload); err != nil {
			m.logger.Error(err, "notification channel failed")
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m *MultiNotifier) Close() error {
	var firstErr error
	for _, n := range m.notifiers {
		if err := n.Close(); err != nil {
			m.logger.Error(err, "failed to close notifier")
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
