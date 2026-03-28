package memory

import (
	"errors"
	"fmt"
	"time"
)

// ErrMemoryNotFound is returned when a memory ID does not exist.
var ErrMemoryNotFound = errors.New("memory not found")

// Memory represents a stored investigation memory (lightweight, used in retrieval).
type Memory struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Category   string    `json:"category"`
	Valence    string    `json:"valence"`
	Confidence float64   `json:"confidence"`
	SeenCount  int       `json:"seen_count"`
	Score      float64   `json:"score,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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

// updatedAtThreshold is the minimum difference between created_at and
// updated_at before we show "updated X ago" alongside "learned X ago".
const updatedAtThreshold = time.Hour

// FormatMemoryAge returns the age label for a memory, e.g.
// "learned 3 days ago" or "learned 6 months ago, updated 1 day ago".
func FormatMemoryAge(createdAt, updatedAt time.Time) string {
	return FormatMemoryAgeSince(createdAt, updatedAt, time.Now())
}

// FormatMemoryAgeSince is the testable variant with a fixed "now".
// Returns "" if createdAt is zero (legacy rows with NULL timestamps).
func FormatMemoryAgeSince(createdAt, updatedAt, now time.Time) string {
	if createdAt.IsZero() {
		return ""
	}
	learned := humanizeAge(createdAt, now)
	if updatedAt.Sub(createdAt) > updatedAtThreshold {
		return fmt.Sprintf("learned %s, updated %s", learned, humanizeAge(updatedAt, now))
	}
	return "learned " + learned
}

func humanizeAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return pluralize(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return pluralize(int(d.Hours()), "hour")
	case d < 7*24*time.Hour:
		return pluralize(int(d.Hours()/24), "day")
	case d < 30*24*time.Hour:
		return pluralize(int(d.Hours()/(24*7)), "week")
	case d < 365*24*time.Hour:
		months := max(int(d.Hours()/(24*30)), 1)
		return pluralize(months, "month")
	default:
		years := max(int(d.Hours()/(24*365)), 1)
		return pluralize(years, "year")
	}
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
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
