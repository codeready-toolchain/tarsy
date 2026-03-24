package services

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
)

// SessionCounter implements metrics.SessionCounter using Ent queries.
type SessionCounter struct{ client *ent.Client }

// NewSessionCounter returns a SessionCounter backed by the given ent client.
func NewSessionCounter(client *ent.Client) *SessionCounter {
	return &SessionCounter{client: client}
}

// PendingCount returns the number of sessions waiting to be claimed.
func (c *SessionCounter) PendingCount(ctx context.Context) (int, error) {
	return c.client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusPending), alertsession.DeletedAtIsNil()).
		Count(ctx)
}

// ActiveCount returns the number of sessions currently being processed.
// Both in_progress and cancelling sessions are counted as active.
func (c *SessionCounter) ActiveCount(ctx context.Context) (int, error) {
	return c.client.AlertSession.Query().
		Where(
			alertsession.StatusIn(alertsession.StatusInProgress, alertsession.StatusCancelling),
			alertsession.DeletedAtIsNil(),
		).
		Count(ctx)
}

// ReviewCountsByRating returns session counts grouped by quality rating.
func (c *SessionCounter) ReviewCountsByRating(ctx context.Context) (metrics.ReviewCounts, error) {
	var rows []struct {
		QualityRating string `json:"quality_rating"`
		Count         int    `json:"count"`
	}
	err := c.client.AlertSession.Query().
		Where(alertsession.QualityRatingNotNil(), alertsession.DeletedAtIsNil()).
		GroupBy(alertsession.FieldQualityRating).
		Aggregate(ent.Count()).
		Scan(ctx, &rows)
	if err != nil {
		return metrics.ReviewCounts{}, fmt.Errorf("review counts query: %w", err)
	}

	var rc metrics.ReviewCounts
	for _, row := range rows {
		switch alertsession.QualityRating(row.QualityRating) {
		case alertsession.QualityRatingAccurate:
			rc.Accurate = row.Count
		case alertsession.QualityRatingPartiallyAccurate:
			rc.Partial = row.Count
		case alertsession.QualityRatingInaccurate:
			rc.Inaccurate = row.Count
		}
	}
	return rc, nil
}

// ActionOutcomesByAgent returns counts of action stages grouped by
// agent name and whether actions were executed.
func (c *SessionCounter) ActionOutcomesByAgent(ctx context.Context) ([]metrics.ActionOutcomeRow, error) {
	stages, err := c.client.Stage.Query().
		Where(
			stage.StageTypeEQ(stage.StageTypeAction),
			stage.ActionsExecutedNotNil(),
			stage.HasSessionWith(alertsession.DeletedAtIsNil()),
		).
		WithAgentExecutions(func(q *ent.AgentExecutionQuery) {
			q.Where(agentexecution.ParentExecutionIDIsNil()).
				Select(agentexecution.FieldAgentName)
		}).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("action outcomes query: %w", err)
	}

	type key struct {
		agentName       string
		actionsExecuted bool
	}
	counts := make(map[key]int)
	for _, stg := range stages {
		executed := stg.ActionsExecuted != nil && *stg.ActionsExecuted
		agentName := stg.StageName
		if execs := stg.Edges.AgentExecutions; len(execs) > 0 {
			agentName = execs[0].AgentName
		}
		counts[key{agentName, executed}]++
	}

	rows := make([]metrics.ActionOutcomeRow, 0, len(counts))
	for k, count := range counts {
		rows = append(rows, metrics.ActionOutcomeRow{
			AgentName:       k.agentName,
			ActionsExecuted: k.actionsExecuted,
			Count:           count,
		})
	}
	return rows, nil
}
