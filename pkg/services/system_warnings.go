package services

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Warning category constants for categorizing system warnings.
const (
	WarningCategoryMCPHealth = "mcp_health" // MCP server became unhealthy at runtime
)

// SystemWarning represents a non-fatal system issue.
type SystemWarning struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	ServerID  string    `json:"server_id,omitempty"` // For MCP-related warnings
	CreatedAt time.Time `json:"created_at"`
}

// SystemWarningsService manages in-memory system warnings.
// Thread-safe. Not persisted — warnings are transient and reset on restart.
type SystemWarningsService struct {
	mu       sync.RWMutex
	warnings map[string]*SystemWarning // warningID → warning
}

// NewSystemWarningsService creates a new SystemWarningsService.
func NewSystemWarningsService() *SystemWarningsService {
	return &SystemWarningsService{
		warnings: make(map[string]*SystemWarning),
	}
}

// AddWarning adds a warning and returns its ID.
// If a warning with the same category+serverID already exists, it is replaced.
func (s *SystemWarningsService) AddWarning(category, message, details, serverID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Replace existing warning with same category+serverID to avoid duplicates
	for id, w := range s.warnings {
		if w.Category == category && w.ServerID == serverID {
			delete(s.warnings, id)
			break
		}
	}

	id := uuid.New().String()
	s.warnings[id] = &SystemWarning{
		ID:        id,
		Category:  category,
		Message:   message,
		Details:   details,
		ServerID:  serverID,
		CreatedAt: time.Now(),
	}
	return id
}

// GetWarnings returns all active warnings as value copies.
// Callers may safely read or compare the returned structs without holding locks.
func (s *SystemWarningsService) GetWarnings() []*SystemWarning {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*SystemWarning, 0, len(s.warnings))
	for _, w := range s.warnings {
		cp := *w
		result = append(result, &cp)
	}
	return result
}

// ClearByServerID removes a warning matching category + serverID.
// Used by HealthMonitor to clear warnings when servers recover.
// Returns true if a warning was removed.
func (s *SystemWarningsService) ClearByServerID(category, serverID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, w := range s.warnings {
		if w.Category == category && w.ServerID == serverID {
			delete(s.warnings, id)
			return true
		}
	}
	return false
}
