package api

import (
	"github.com/coder/websocket"
	echo "github.com/labstack/echo/v5"
)

// wsHandler upgrades HTTP connections to WebSocket and delegates to ConnectionManager.
func (s *Server) wsHandler(c *echo.Context) error {
	if s.connManager == nil {
		return echo.NewHTTPError(503, "WebSocket not available")
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		OriginPatterns: s.wsOriginPatterns,
	})
	if err != nil {
		return err
	}

	// Register connection with the ConnectionManager.
	// HandleConnection blocks until the WebSocket closes.
	s.connManager.HandleConnection(c.Request().Context(), conn)
	return nil
}
