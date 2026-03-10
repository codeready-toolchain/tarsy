package api

import (
	"errors"
	"net/http"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
)

// ScoreSessionResponse is the HTTP response for POST /sessions/:id/score.
type ScoreSessionResponse struct {
	ScoreID string `json:"score_id"`
}

// scoreSessionHandler handles POST /api/v1/sessions/:id/score.
// Triggers an async scoring evaluation for a completed session.
// Returns 202 Accepted with the score ID.
func (s *Server) scoreSessionHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	if s.scoringExecutor == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scoring service is not available")
	}

	// Verify session exists and is in terminal state
	session, err := s.sessionService.GetSession(c.Request().Context(), sessionID, false)
	if err != nil {
		return mapServiceError(err)
	}
	if !queue.IsTerminalStatus(session.Status) {
		return echo.NewHTTPError(http.StatusConflict, "session is not in a terminal state")
	}

	author := extractAuthor(c)

	// API re-score bypasses the chain scoring.enabled check
	scoreID, err := s.scoringExecutor.SubmitScoring(c.Request().Context(), sessionID, author, false)
	if err != nil {
		if errors.Is(err, queue.ErrScoringInProgress) {
			return echo.NewHTTPError(http.StatusConflict, "scoring already in progress for this session")
		}
		if errors.Is(err, queue.ErrShuttingDown) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "service is shutting down")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start scoring")
	}

	return c.JSON(http.StatusAccepted, &ScoreSessionResponse{
		ScoreID: scoreID,
	})
}

// getScoreHandler handles GET /api/v1/sessions/:id/score.
// Returns the latest score for the session.
func (s *Server) getScoreHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	if s.scoringService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scoring service is not available")
	}

	score, err := s.scoringService.GetLatestScore(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, &models.SessionScoreResponse{
		ScoreID:              score.ID,
		TotalScore:           score.TotalScore,
		ScoreAnalysis:        score.ScoreAnalysis,
		MissingToolsAnalysis: score.MissingToolsAnalysis,
		PromptHash:           score.PromptHash,
		ScoreTriggeredBy:     score.ScoreTriggeredBy,
		Status:               string(score.Status),
		StageID:              score.StageID,
		StartedAt:            score.StartedAt,
		CompletedAt:          score.CompletedAt,
		ErrorMessage:         score.ErrorMessage,
	})
}
