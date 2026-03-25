package models

import "time"

// MemoryResponse is the HTTP response for a single memory.
type MemoryResponse struct {
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

// MemoryListResponse wraps paginated memory results.
type MemoryListResponse struct {
	Memories   []MemoryResponse `json:"memories"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalPages int              `json:"total_pages"`
}

// UpdateMemoryRequest is the request body for PATCH /memories/:id.
type UpdateMemoryRequest struct {
	Content    *string `json:"content,omitempty"`
	Category   *string `json:"category,omitempty"`
	Valence    *string `json:"valence,omitempty"`
	Deprecated *bool   `json:"deprecated,omitempty"`
}
