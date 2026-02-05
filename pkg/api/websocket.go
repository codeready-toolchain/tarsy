package api

import (
	"net/http"
)

// WSHub manages WebSocket connections
// NOTE: This is a minimal stub for Phase 2.1
// Will be properly implemented in Phase 2.3
type WSHub struct {
}

// NewWSHub creates a new WebSocket hub
func NewWSHub() *WSHub {
	return &WSHub{}
}

// Run starts the hub
func (h *WSHub) Run() {
	// Stub - will be implemented in Phase 2.3
}

// HandleWS handles WebSocket connections
func (h *WSHub) HandleWS(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "WebSocket not implemented yet - Phase 2.3", http.StatusNotImplemented)
}
