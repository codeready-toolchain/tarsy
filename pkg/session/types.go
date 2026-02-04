package session

import (
	"context"
	"sync"
	"time"
)

// MessageRole represents the role of a message sender
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
)

// Message represents a single conversation message
type Message struct {
	Role    MessageRole `json:"role"`
	Content string      `json:"content"`
}

// SessionStatus represents the current state of a session
type SessionStatus string

const (
	StatusPending    SessionStatus = "pending"
	StatusProcessing SessionStatus = "processing"
	StatusCompleted  SessionStatus = "completed"
	StatusFailed     SessionStatus = "failed"
	StatusCancelled  SessionStatus = "cancelled"
	StatusTimedOut   SessionStatus = "timed_out"
)

// Session represents a conversation session
type Session struct {
	ID         string             `json:"id"`
	Messages   []Message          `json:"messages"`
	Status     SessionStatus      `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Error      string             `json:"error,omitempty"`
	mu         sync.RWMutex       // Protects concurrent access to session fields
	cancelFunc context.CancelFunc `json:"-"` // Function to cancel processing
}

// AddMessage adds a message to the session (thread-safe)
func (s *Session) AddMessage(role MessageRole, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		Role:    role,
		Content: content,
	})
	s.UpdatedAt = time.Now()
}

// SetStatus updates the session status (thread-safe)
func (s *Session) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Status = status
	s.UpdatedAt = time.Now()
}

// SetError sets the error message and status (thread-safe)
func (s *Session) SetError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Error = err
	s.Status = StatusFailed
	s.UpdatedAt = time.Now()
}

// SetCancelFunc stores the cancel function for this session (thread-safe)
func (s *Session) SetCancelFunc(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelFunc = cancel
}

// Cancel cancels the session processing (thread-safe)
func (s *Session) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelFunc != nil {
		s.cancelFunc()
		s.Status = StatusCancelled
		s.UpdatedAt = time.Now()
		return true
	}
	return false
}

// SetTimedOut marks the session as timed out (thread-safe)
func (s *Session) SetTimedOut(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Error = message
	s.Status = StatusTimedOut
	s.UpdatedAt = time.Now()
}

// Clone creates a safe copy of the session for reading
func (s *Session) Clone() Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Deep copy messages
	messages := make([]Message, len(s.Messages))
	copy(messages, s.Messages)

	return Session{
		ID:        s.ID,
		Messages:  messages,
		Status:    s.Status,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Error:     s.Error,
	}
}
