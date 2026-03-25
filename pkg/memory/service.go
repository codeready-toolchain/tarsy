package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

// candidateMultiplier controls how many extra rows the inner index scan
// fetches before the boost re-ranking narrows to the final limit.
const candidateMultiplier = 3

// FindSimilarWithBoosts returns the top-N memories using cosine similarity
// with soft boosts for alert_type and chain_id scope metadata.
//
// The query uses a two-step approach so pgvector's HNSW index is utilised:
//  1. Inner CTE fetches a larger candidate set ordered by raw cosine distance.
//  2. Outer query re-ranks candidates with scope boosts and returns the final limit.
func (s *Service) FindSimilarWithBoosts(ctx context.Context, project, queryText string, alertType, chainID *string, limit int) ([]Memory, error) {
	queryVec, err := s.embedder.Embed(ctx, queryText, EmbeddingTaskQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}

	vecStr := formatVector(queryVec)
	candidates := max(limit*candidateMultiplier, 20)

	rows, err := s.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT memory_id, content, category, valence, confidence, seen_count,
			       alert_type, chain_id,
			       (embedding <=> $2::vector) AS distance
			FROM investigation_memories
			WHERE project = $1
			  AND deprecated = false
			  AND embedding IS NOT NULL
			ORDER BY embedding <=> $2::vector
			LIMIT $3
		)
		SELECT memory_id, content, category, valence, confidence, seen_count
		FROM candidates
		ORDER BY
		  (1 - distance)
		  + CASE WHEN alert_type = $4 THEN 0.05 ELSE 0 END
		  + CASE WHEN chain_id  = $5 THEN 0.03 ELSE 0 END
		  DESC
		LIMIT $6
	`, project, vecStr, candidates, alertType, chainID, limit)
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

	var errs []error

	for _, action := range result.Create {
		if err := s.createMemory(ctx, project, sessionID, alertType, chainID, score, action); err != nil {
			slog.Warn("Failed to create memory",
				"session_id", sessionID, "content_prefix", truncate(action.Content, 80), "error", err)
			errs = append(errs, err)
		}
	}

	for _, action := range result.Reinforce {
		if err := s.reinforce(ctx, project, action.MemoryID); err != nil {
			slog.Warn("Failed to reinforce memory",
				"memory_id", action.MemoryID, "error", err)
			errs = append(errs, err)
		}
	}

	for _, action := range result.Deprecate {
		if err := s.deprecate(ctx, project, action.MemoryID); err != nil {
			slog.Warn("Failed to deprecate memory",
				"memory_id", action.MemoryID, "error", err)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// createMemory uses raw SQL instead of Ent so the row and its embedding
// (pgvector type, not managed by Ent) are written in a single atomic INSERT.
func (s *Service) createMemory(ctx context.Context, project, sessionID string, alertType, chainID *string, score int, action ReflectorCreateAction) error {
	if err := investigationmemory.CategoryValidator(investigationmemory.Category(action.Category)); err != nil {
		return fmt.Errorf("invalid category %q: %w", action.Category, err)
	}
	if err := investigationmemory.ValenceValidator(investigationmemory.Valence(action.Valence)); err != nil {
		return fmt.Errorf("invalid valence %q: %w", action.Valence, err)
	}

	vec, err := s.embedder.Embed(ctx, action.Content, EmbeddingTaskDocument)
	if err != nil {
		return fmt.Errorf("embed memory content: %w", err)
	}

	memoryID := uuid.New().String()
	confidence := initialConfidence(score)
	vecStr := formatVector(vec)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, alert_type, chain_id, created_at, updated_at,
			 last_seen_at, deprecated, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, $9, NOW(), NOW(), NOW(), false, $10::vector)`,
		memoryID, project, action.Content, action.Category, action.Valence, confidence,
		sessionID, alertType, chainID, vecStr,
	); err != nil {
		return fmt.Errorf("create memory: %w", err)
	}

	return nil
}

// reinforce uses raw SQL instead of Ent because the confidence update
// (LEAST(confidence * 1.1, 1.0)) requires a multiplicative expression
// that Ent's update builder cannot express atomically.
func (s *Service) reinforce(ctx context.Context, project, memoryID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE investigation_memories
		SET confidence = LEAST(confidence * 1.1, 1.0),
		    seen_count = seen_count + 1,
		    last_seen_at = NOW(),
		    updated_at = NOW()
		WHERE memory_id = $1 AND project = $2
	`, memoryID, project)
	if err != nil {
		return fmt.Errorf("reinforce memory %s: %w", memoryID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("memory %s not found in project %s", memoryID, project)
	}
	return nil
}

func (s *Service) deprecate(ctx context.Context, project, memoryID string) error {
	n, err := s.entClient.InvestigationMemory.Update().
		Where(
			investigationmemory.ID(memoryID),
			investigationmemory.Project(project),
		).
		SetDeprecated(true).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("deprecate memory %s: %w", memoryID, err)
	}
	if n == 0 {
		return fmt.Errorf("memory %s not found in project %s", memoryID, project)
	}
	return nil
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
