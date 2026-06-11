package controller

import (
	"sync"

	"github.com/snapp-incubator/namespacejanitor/internal/notification"
)

type MockNotifier struct {
	mu       sync.Mutex
	Payloads []notification.JanitorPayload
}

func (m *MockNotifier) Send(payload notification.JanitorPayload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Payloads = append(m.Payloads, payload)
	return nil
}

func (m *MockNotifier) Close() error {
	return nil
}

func (m *MockNotifier) GetPayloads() []notification.JanitorPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]notification.JanitorPayload, len(m.Payloads))
	copy(cp, m.Payloads)
	return cp
}

func (m *MockNotifier) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Payloads = nil
}
