package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// SendChatMessageRequest is the HTTP request body for POST /sessions/:id/chat/messages.
type SendChatMessageRequest struct {
	Content string `json:"content"`
}

// SendChatMessageResponse is the HTTP response for POST /sessions/:id/chat/messages.
type SendChatMessageResponse struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	StageID   string `json:"stage_id"`
}

// sendChatMessageHandler handles POST /api/v1/sessions/:id/chat/messages.
// Creates/gets a chat, adds the user message, and submits it for async processing.
func (s *Server) sendChatMessageHandler(c *echo.Context) error {
	// 1. Validate session ID
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	// 1b. Verify chat dependencies are initialized
	if s.chatService == nil || s.chatExecutor == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "chat service is not available")
	}

	// 2. Get session, validate terminal status
	session, err := s.sessionService.GetSession(c.Request().Context(), sessionID, false)
	if err != nil {
		return mapServiceError(err)
	}

	// 3. Resolve chain config, validate chat is available
	chain, err := s.cfg.GetChain(session.ChainID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "chain configuration not found")
	}

	if reason := isChatAvailable(session.Status, chain); reason != "" {
		return echo.NewHTTPError(http.StatusBadRequest, reason)
	}

	// 4. Bind and validate request body
	var req SendChatMessageRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Content == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "content is required")
	}
	if len(req.Content) > 100_000 {
		return echo.NewHTTPError(http.StatusBadRequest, "content exceeds maximum length of 100,000 characters")
	}

	// 5. Extract author
	author := extractAuthor(c)

	// 6. Get or create chat
	chatObj, created, err := s.chatService.GetOrCreateChat(c.Request().Context(), sessionID, author)
	if err != nil {
		return mapServiceError(err)
	}

	// 7. Publish chat.created event if chat was just created
	if created && s.eventPublisher != nil {
		if pubErr := s.eventPublisher.PublishChatCreated(c.Request().Context(), sessionID, events.ChatCreatedPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeChatCreated,
				SessionID: sessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			ChatID:    chatObj.ID,
			CreatedBy: author,
		}); pubErr != nil {
			slog.Warn("Failed to publish chat.created event",
				"session_id", sessionID, "error", pubErr)
		}
	}

	// 8. Add chat message
	msg, err := s.chatService.AddChatMessage(c.Request().Context(), models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: req.Content,
		Author:  author,
	})
	if err != nil {
		return mapServiceError(err)
	}

	// 9. Submit to ChatMessageExecutor
	stageID, err := s.chatExecutor.Submit(c.Request().Context(), queue.ChatExecuteInput{
		Chat:    chatObj,
		Message: msg,
		Session: session,
	})
	if err != nil {
		// Clean up orphaned message on rejection errors
		if errors.Is(err, queue.ErrChatExecutionActive) || errors.Is(err, queue.ErrShuttingDown) {
			if delErr := s.chatService.DeleteChatMessage(c.Request().Context(), msg.ID); delErr != nil {
				slog.Warn("Failed to clean up rejected chat message",
					"message_id", msg.ID, "error", delErr)
			}
		}
		return mapChatExecutorError(err)
	}

	// 10. Return 202 Accepted
	return c.JSON(http.StatusAccepted, &SendChatMessageResponse{
		ChatID:    chatObj.ID,
		MessageID: msg.ID,
		StageID:   stageID,
	})
}

// isChatAvailable checks if a chat can be started for a session.
// Returns an empty string if available, or an error reason otherwise.
func isChatAvailable(sessionStatus alertsession.Status, chain *config.ChainConfig) string {
	// Session must be in a terminal state (completed, failed, timed_out)
	switch sessionStatus {
	case alertsession.StatusCompleted, alertsession.StatusFailed, alertsession.StatusTimedOut:
		// OK — session is terminal
	case alertsession.StatusPending, alertsession.StatusInProgress:
		return "chat is not available while session is still processing"
	case alertsession.StatusCancelling:
		return "chat is not available while session is being cancelled"
	case alertsession.StatusCancelled:
		return "chat is not available for cancelled sessions"
	default:
		return "chat is not available for sessions in this state"
	}

	// Chat is enabled by default; only disabled if explicitly set to false.
	if chain.Chat != nil && !chain.Chat.Enabled {
		return "chat is not enabled for this chain"
	}

	return ""
}

// mapChatExecutorError maps ChatMessageExecutor errors to HTTP errors.
func mapChatExecutorError(err error) *echo.HTTPError {
	if errors.Is(err, queue.ErrChatExecutionActive) {
		return echo.NewHTTPError(http.StatusConflict, "a chat response is already being generated")
	}
	if errors.Is(err, queue.ErrShuttingDown) {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "service is shutting down")
	}

	var validErr *services.ValidationError
	if errors.As(err, &validErr) {
		return echo.NewHTTPError(http.StatusBadRequest, validErr.Error())
	}

	return echo.NewHTTPError(http.StatusInternalServerError, "failed to process chat message")
}
