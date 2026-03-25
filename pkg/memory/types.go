package memory

import (
	"errors"
	"time"
)

// ErrMemoryNotFound is returned when a memory ID does not exist.
var ErrMemoryNotFound = errors.New("memory not found")

// Memory represents a stored investigation memory (lightweight, used in retrieval).
type Memory struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Category   string  `json:"category"`
	Valence    string  `json:"valence"`
	Confidence float64 `json:"confidence"`
	SeenCount  int     `json:"seen_count"`
}

// Detail is the full representation of a memory, used by CRUD endpoints.
type Detail struct {
	ID              string    `json:"id"`
	Project         string    `json:"project"`
	Content         string    `json:"content"`
	Category        string    `json:"category"`
	Valence         string    `json:"valence"`
	Confidence      float64   `json:"confidence"`
	SeenCount       int       `json:"seen_count"`
	SourceSessionID string    `json:"source_session_id"`
	AlertType       *string   `json:"alert_type"`
	ChainID         *string   `json:"chain_id"`
	Deprecated      bool      `json:"deprecated"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
}

// ReflectorResult holds the parsed output from a Reflector LLM call.
type ReflectorResult struct {
	Create    []ReflectorCreateAction    `json:"create"`
	Reinforce []ReflectorReinforceAction `json:"reinforce"`
	Deprecate []ReflectorDeprecateAction `json:"deprecate"`
}

// IsEmpty returns true when the Reflector produced no actions.
func (r *ReflectorResult) IsEmpty() bool {
	return len(r.Create) == 0 && len(r.Reinforce) == 0 && len(r.Deprecate) == 0
}

// ReflectorCreateAction describes a new memory to store.
type ReflectorCreateAction struct {
	Content  string `json:"content"`
	Category string `json:"category"`
	Valence  string `json:"valence"`
}

// ReflectorReinforceAction identifies an existing memory to reinforce.
type ReflectorReinforceAction struct {
	MemoryID string `json:"memory_id"`
}

// ReflectorDeprecateAction identifies an existing memory to deprecate.
type ReflectorDeprecateAction struct {
	MemoryID string `json:"memory_id"`
	Reason   string `json:"reason"`
}
