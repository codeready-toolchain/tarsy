package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager manages sessions in memory
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// Create creates a new session with the given initial message
func (m *Manager) Create(userMessage string) (*Session, error) {
	sessionID := uuid.New().String()
	now := time.Now()

	session := &Session{
		ID:        sessionID,
		Messages:  []Message{},
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Add system message
	session.Messages = append(session.Messages, Message{
		Role:    RoleSystem,
		Content: "You are a helpful AI assistant.",
	})

	// Add user message
	session.Messages = append(session.Messages, Message{
		Role:    RoleUser,
		Content: userMessage,
	})

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	return session, nil
}

// Get retrieves a session by ID
func (m *Manager) Get(sessionID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return session, nil
}

// List returns all sessions
func (m *Manager) List() []Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s.Clone())
	}

	return sessions
}

// Delete removes a session
func (m *Manager) Delete(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[sessionID]; !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	delete(m.sessions, sessionID)
	return nil
}
