package api

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for PoC
	},
}

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type      string      `json:"type"`
	SessionID string      `json:"session_id,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

// WSHub manages WebSocket connections
type WSHub struct {
	clients    map[*websocket.Conn]bool
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	broadcast  chan WSMessage
	mu         sync.RWMutex
}

// NewWSHub creates a new WebSocket hub
func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*websocket.Conn]bool),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		broadcast:  make(chan WSMessage, 256),
	}
}

// Run starts the hub
func (h *WSHub) Run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()
			log.Printf("WebSocket client connected (total: %d)", len(h.clients))

		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				conn.Close()
			}
			h.mu.Unlock()
			log.Printf("WebSocket client disconnected (total: %d)", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.clients {
				err := conn.WriteJSON(message)
				if err != nil {
					log.Printf("Error writing to WebSocket: %v", err)
					go func(c *websocket.Conn) {
						h.unregister <- c
					}(conn)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients
func (h *WSHub) Broadcast(msgType string, sessionID string, data interface{}) {
	h.broadcast <- WSMessage{
		Type:      msgType,
		SessionID: sessionID,
		Data:      data,
	}
}

// HandleWS handles WebSocket connections
func (h *WSHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	h.register <- conn

	// Send welcome message
	conn.WriteJSON(WSMessage{
		Type: "connected",
		Data: map[string]string{"message": "Connected to TARSy"},
	})

	// Read loop (for keepalive/ping)
	go func() {
		defer func() {
			h.unregister <- conn
		}()

		for {
			var msg map[string]interface{}
			err := conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}

			// Handle ping
			if msgType, ok := msg["type"].(string); ok && msgType == "ping" {
				conn.WriteJSON(WSMessage{Type: "pong"})
			}
		}
	}()
}
