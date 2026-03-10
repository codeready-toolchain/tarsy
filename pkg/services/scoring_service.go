package services

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
)

// ScoringService provides read access to session score data.
type ScoringService struct {
	client *ent.Client
}

// NewScoringService creates a new ScoringService.
func NewScoringService(client *ent.Client) *ScoringService {
	return &ScoringService{client: client}
}

// GetLatestScore returns the most recent score for a session.
// Returns ErrNotFound if no scores exist.
func (s *ScoringService) GetLatestScore(ctx context.Context, sessionID string) (*ent.SessionScore, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	score, err := s.client.SessionScore.Query().
		Where(sessionscore.SessionIDEQ(sessionID)).
		Order(sessionscore.ByStartedAt(sql.OrderDesc())).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get latest score: %w", err)
	}
	return score, nil
}
