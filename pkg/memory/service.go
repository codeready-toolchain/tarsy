package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/investigationmemory"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/google/uuid"
)

// Service manages investigation memories: CRUD, embedding, and similarity search.
type Service struct {
	entClient *ent.Client
	db        *sql.DB
	embedder  Embedder
	cfg       *config.MemoryConfig
}

// NewService creates a MemoryService.
func NewService(entClient *ent.Client, db *sql.DB, embedder Embedder, cfg *config.MemoryConfig) *Service {
	return &Service{
		entClient: entClient,
		db:        db,
		embedder:  embedder,
		cfg:       cfg,
	}
}

// FindSimilar returns the top-N memories most similar to queryText within a project.
func (s *Service) FindSimilar(ctx context.Context, project, queryText string, limit int) ([]Memory, error) {
	queryVec, err := s.embedder.Embed(ctx, queryText, EmbeddingTaskQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}

	vecStr := formatVector(queryVec)

	rows, err := s.db.QueryContext(ctx, `
		SELECT memory_id, content, category, valence, confidence, seen_count
		FROM investigation_memories
		WHERE project = $1
		  AND deprecated = false
		  AND embedding IS NOT NULL
		ORDER BY (embedding <=> $2::vector)
		LIMIT $3
	`, project, vecStr, limit)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.Category, &m.Valence, &m.Confidence, &m.SeenCount); err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// FindSimilarWithBoosts returns the top-N memories using cosine similarity
// with soft boosts for alert_type and chain_id scope metadata.
func (s *Service) FindSimilarWithBoosts(ctx context.Context, project, queryText string, alertType, chainID *string, limit int) ([]Memory, error) {
	queryVec, err := s.embedder.Embed(ctx, queryText, EmbeddingTaskQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}

	vecStr := formatVector(queryVec)

	rows, err := s.db.QueryContext(ctx, `
		SELECT memory_id, content, category, valence, confidence, seen_count
		FROM investigation_memories
		WHERE project = $1
		  AND deprecated = false
		  AND embedding IS NOT NULL
		ORDER BY
		  (1 - (embedding <=> $2::vector))
		  + CASE WHEN alert_type = $3 THEN 0.05 ELSE 0 END
		  + CASE WHEN chain_id  = $4 THEN 0.03 ELSE 0 END
		  DESC
		LIMIT $5
	`, project, vecStr, alertType, chainID, limit)
	if err != nil {
		return nil, fmt.Errorf("similarity search with boosts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.Category, &m.Valence, &m.Confidence, &m.SeenCount); err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// ApplyReflectorActions processes the Reflector's output: creates new memories,
// reinforces confirmed ones, and deprecates contradicted ones.
func (s *Service) ApplyReflectorActions(ctx context.Context, project, sessionID string, alertType, chainID *string, score int, result *ReflectorResult) error {
	if result == nil || result.IsEmpty() {
		return nil
	}

	for _, action := range result.Create {
		if err := s.createMemory(ctx, project, sessionID, alertType, chainID, score, action); err != nil {
			slog.Warn("Failed to create memory",
				"session_id", sessionID, "content_prefix", truncate(action.Content, 80), "error", err)
		}
	}

	for _, action := range result.Reinforce {
		if err := s.reinforce(ctx, action.MemoryID); err != nil {
			slog.Warn("Failed to reinforce memory",
				"memory_id", action.MemoryID, "error", err)
		}
	}

	for _, action := range result.Deprecate {
		if err := s.deprecate(ctx, action.MemoryID); err != nil {
			slog.Warn("Failed to deprecate memory",
				"memory_id", action.MemoryID, "error", err)
		}
	}

	return nil
}

func (s *Service) createMemory(ctx context.Context, project, sessionID string, alertType, chainID *string, score int, action ReflectorCreateAction) error {
	vec, err := s.embedder.Embed(ctx, action.Content, EmbeddingTaskDocument)
	if err != nil {
		return fmt.Errorf("embed memory content: %w", err)
	}

	memoryID := uuid.New().String()
	confidence := initialConfidence(score)

	builder := s.entClient.InvestigationMemory.Create().
		SetID(memoryID).
		SetProject(project).
		SetContent(action.Content).
		SetCategory(investigationmemory.Category(action.Category)).
		SetValence(investigationmemory.Valence(action.Valence)).
		SetConfidence(confidence).
		SetSourceSessionID(sessionID)

	if alertType != nil {
		builder.SetAlertType(*alertType)
	}
	if chainID != nil {
		builder.SetChainID(*chainID)
	}

	if _, err := builder.Save(ctx); err != nil {
		return fmt.Errorf("save memory: %w", err)
	}

	vecStr := formatVector(vec)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE investigation_memories SET embedding = $1::vector WHERE memory_id = $2`,
		vecStr, memoryID,
	); err != nil {
		return fmt.Errorf("store embedding: %w", err)
	}

	return nil
}

func (s *Service) reinforce(ctx context.Context, memoryID string) error {
	mem, err := s.entClient.InvestigationMemory.Get(ctx, memoryID)
	if err != nil {
		return fmt.Errorf("get memory %s: %w", memoryID, err)
	}

	newConfidence := math.Min(mem.Confidence*1.1, 1.0)

	return s.entClient.InvestigationMemory.UpdateOneID(memoryID).
		SetConfidence(newConfidence).
		SetSeenCount(mem.SeenCount + 1).
		SetLastSeenAt(time.Now()).
		Exec(ctx)
}

func (s *Service) deprecate(ctx context.Context, memoryID string) error {
	return s.entClient.InvestigationMemory.UpdateOneID(memoryID).
		SetDeprecated(true).
		Exec(ctx)
}

// initialConfidence derives initial confidence from the investigation's quality score.
func initialConfidence(score int) float64 {
	switch {
	case score >= 80:
		return 0.8
	case score >= 60:
		return 0.6
	case score >= 40:
		return 0.4
	default:
		return 0.3
	}
}

// ValidateDimensions checks that the configured embedding dimensions match the
// pgvector column size. Returns an error on mismatch.
func (s *Service) ValidateDimensions(ctx context.Context) error {
	var atttypmod int
	err := s.db.QueryRowContext(ctx, `
		SELECT atttypmod
		FROM pg_attribute
		WHERE attrelid = 'investigation_memories'::regclass
		  AND attname = 'embedding'
	`).Scan(&atttypmod)
	if err != nil {
		return fmt.Errorf("query embedding column dimensions: %w", err)
	}

	if atttypmod != s.cfg.Embedding.Dimensions {
		return fmt.Errorf(
			"configured embedding dimensions (%d) does not match pgvector column size (%d) — re-embedding required",
			s.cfg.Embedding.Dimensions, atttypmod,
		)
	}
	return nil
}

// formatVector converts a float32 slice to the pgvector string format: [1.0,2.0,3.0]
func formatVector(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%g", f)
	}
	sb.WriteByte(']')
	return sb.String()
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
