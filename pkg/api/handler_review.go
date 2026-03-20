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

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

const maxSessionIDs = 50

// updateReviewHandler handles PATCH /api/v1/sessions/review.
// Accepts one or more session IDs in the request body.
func (s *Server) updateReviewHandler(c *echo.Context) error {
	var req models.UpdateReviewRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(req.SessionIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "session_ids is required and must not be empty")
	}
	if len(req.SessionIDs) > maxSessionIDs {
		return echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("session_ids must not exceed %d entries", maxSessionIDs))
	}
	if !models.ValidReviewAction(req.Action) {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("unknown action %q", req.Action))
	}
	if models.ReviewAction(req.Action) == models.ReviewActionComplete && req.QualityRating == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "quality_rating is required for complete action")
	}
	req.Actor = extractAuthor(c)

	resp, updated := s.sessionService.UpdateReviewStatus(c.Request().Context(), req)

	if s.eventPublisher != nil {
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pubCancel()

		for _, session := range updated {
			payload := events.ReviewStatusPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeReviewStatus,
					SessionID: session.ID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				Actor:    req.Actor,
				Assignee: session.Assignee,
			}
			if session.ReviewStatus != nil {
				rs := string(*session.ReviewStatus)
				payload.ReviewStatus = &rs
			}
			if session.QualityRating != nil {
				qr := string(*session.QualityRating)
				payload.QualityRating = &qr
			}
			if session.ActionTaken != nil {
				payload.ActionTaken = session.ActionTaken
			}
			if session.InvestigationFeedback != nil {
				payload.InvestigationFeedback = session.InvestigationFeedback
			}
			if err := s.eventPublisher.PublishReviewStatus(pubCtx, session.ID, payload); err != nil {
				slog.Warn("Failed to publish review status event",
					"session_id", session.ID, "error", err)
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
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
		if a.QualityRating != nil {
			qr := string(*a.QualityRating)
			item.QualityRating = &qr
		}
		if a.Note != nil {
			item.Note = a.Note
		}
		if a.InvestigationFeedback != nil {
			item.InvestigationFeedback = a.InvestigationFeedback
		}
		items = append(items, item)
	}

	return c.JSON(http.StatusOK, models.ReviewActivityResponse{Activities: items})
}

// getTriageGroupHandler handles GET /api/v1/sessions/triage/:group.
func (s *Server) getTriageGroupHandler(c *echo.Context) error {
	group, err := models.ParseTriageGroupKey(c.Param("group"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	params := models.TriageGroupParams{
		Page:     1,
		PageSize: defaultPageSize,
	}
	if assigneeVal := c.QueryParam("assignee"); c.Request().URL.Query().Has("assignee") {
		params.Assignee = &assigneeVal
	}

	if v := c.QueryParam("page"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "page must be a positive integer")
		}
		params.Page = p
	}

	if v := c.QueryParam("page_size"); v != "" {
		ps, err := strconv.Atoi(v)
		if err != nil || ps < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "page_size must be a positive integer")
		}
		if ps > maxPageSize {
			return echo.NewHTTPError(http.StatusBadRequest,
				fmt.Sprintf("page_size must not exceed %d", maxPageSize))
		}
		params.PageSize = ps
	}

	result, err := s.sessionService.GetTriageGroup(c.Request().Context(), group, params)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, result)
}
