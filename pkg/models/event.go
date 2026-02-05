package models

import "github.com/codeready-toolchain/tarsy/ent"

// CreateEventRequest contains fields for creating an event
type CreateEventRequest struct {
	SessionID string         `json:"session_id"`
	Channel   string         `json:"channel"`
	Payload   map[string]any `json:"payload"`
}

// EventResponse wraps an Event
type EventResponse struct {
	*ent.Event
}

// EventsResponse contains list of events since a given ID
type EventsResponse struct {
	Events []*ent.Event `json:"events"`
}
