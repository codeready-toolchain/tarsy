package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

const maxResolvedLimit = 200

// updateReviewHandler handles PATCH /api/v1/sessions/:id/review.
func (s *Server) updateReviewHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	var req models.UpdateReviewRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Actor = extractAuthor(c)

	session, err := s.sessionService.UpdateReviewStatus(c.Request().Context(), sessionID, req)
	if err != nil {
		return mapServiceError(err)
	}

	// Publish review.status event (caller-owns-publishing pattern).
	// Use a detached context so client disconnects don't cancel the publish.
	if s.eventPublisher != nil {
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()

		payload := events.ReviewStatusPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeReviewStatus,
				SessionID: sessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			Actor:    req.Actor,
			Assignee: session.Assignee,
		}
		if session.ReviewStatus != nil {
			rs := string(*session.ReviewStatus)
			payload.ReviewStatus = &rs
		}
		if session.ResolutionReason != nil {
			reason := string(*session.ResolutionReason)
			payload.ResolutionReason = &reason
		}
		if err := s.eventPublisher.PublishReviewStatus(pubCtx, sessionID, payload); err != nil {
			slog.Warn("Failed to publish review status from handler",
				"session_id", sessionID, "error", err)
		}
	}

	var reviewStatus *string
	if session.ReviewStatus != nil {
		statusStr := string(*session.ReviewStatus)
		reviewStatus = &statusStr
	}
	var resolutionReason *string
	if session.ResolutionReason != nil {
		reasonStr := string(*session.ResolutionReason)
		resolutionReason = &reasonStr
	}

	return c.JSON(http.StatusOK, map[string]any{
		"id":                session.ID,
		"review_status":     reviewStatus,
		"assignee":          session.Assignee,
		"assigned_at":       session.AssignedAt,
		"resolved_at":       session.ResolvedAt,
		"resolution_reason": resolutionReason,
		"resolution_note":   session.ResolutionNote,
	})
}

// getReviewActivityHandler handles GET /api/v1/sessions/:id/review-activity.
func (s *Server) getReviewActivityHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	activities, err := s.sessionService.GetReviewActivity(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	items := make([]models.ReviewActivityItem, 0, len(activities))
	for _, a := range activities {
		item := models.ReviewActivityItem{
			ID:        a.ID,
			Actor:     a.Actor,
			Action:    string(a.Action),
			ToStatus:  string(a.ToStatus),
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		}
		if a.FromStatus != nil {
			statusStr := string(*a.FromStatus)
			item.FromStatus = &statusStr
		}
		if a.ResolutionReason != nil {
			reasonStr := string(*a.ResolutionReason)
			item.ResolutionReason = &reasonStr
		}
		if a.Note != nil {
			item.Note = a.Note
		}
		items = append(items, item)
	}

	return c.JSON(http.StatusOK, models.ReviewActivityResponse{Activities: items})
}

// getTriageHandler handles GET /api/v1/sessions/triage.
func (s *Server) getTriageHandler(c *echo.Context) error {
	params := models.TriageParams{
		ResolvedLimit: 20,
		Assignee:      c.QueryParam("assignee"),
	}

	if limitStr := c.QueryParam("resolved_limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "resolved_limit must be a non-negative integer")
		}
		if limit > maxResolvedLimit {
			return echo.NewHTTPError(http.StatusBadRequest,
				fmt.Sprintf("resolved_limit must not exceed %d", maxResolvedLimit))
		}
		params.ResolvedLimit = limit
	}

	result, err := s.sessionService.GetTriageSessions(c.Request().Context(), params)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, result)
}
