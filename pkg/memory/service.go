package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
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
// Used by the Reflector for dedup context — no similarity threshold (the Reflector
// benefits from seeing broadly similar memories), but temporal decay is applied
// so stale memories rank lower.
// Raw SQL: pgvector's cosine distance operator (<=>) is not supported by Ent.
//
// Like FindSimilarWithBoosts, a two-step CTE is used so the inner ORDER BY
// uses the raw distance operator, allowing pgvector's HNSW/IVFFlat ANN index.
// The outer query applies temporal decay for the final ranking.
func (s *Service) FindSimilar(ctx context.Context, project, queryText string, limit int) ([]Memory, error) {
	queryVec, err := s.embedder.Embed(ctx, queryText, EmbeddingTaskQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}

	vecStr := formatVector(queryVec)
	candidates := max(limit*candidateMultiplier, 20)

	// No similarity threshold: the Reflector needs broad context (including
	// loosely related memories) to detect near-duplicates and decide what to
	// reinforce or deprecate. Temporal decay still down-ranks stale entries.
	// See FindSimilarWithBoosts for the threshold-filtered variant.
	rows, err := s.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT memory_id, content, category, valence, confidence, seen_count,
			       created_at, updated_at,
			       (embedding <=> $2::vector) AS distance
			FROM investigation_memories
			WHERE project = $1
			  AND deprecated = false
			  AND embedding IS NOT NULL
			ORDER BY embedding <=> $2::vector
			LIMIT $3
		)
		SELECT memory_id, content, category, valence, confidence, seen_count,
		       created_at, updated_at
		FROM candidates
		ORDER BY (1 - distance)
		       * EXP(-0.0077 * EXTRACT(EPOCH FROM (NOW() - updated_at)) / 86400.0)
		  DESC
		LIMIT $4
	`, project, vecStr, candidates, limit)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.Category, &m.Valence, &m.Confidence, &m.SeenCount,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// candidateMultiplier controls how many extra rows the inner index scan
// fetches before the boost re-ranking narrows to the final limit.
const candidateMultiplier = 3

// similarityThreshold is the minimum (1 - cosine_distance) for a memory to
// be considered relevant. Candidates below this floor are discarded before
// the final LIMIT. Reviewed when the embedding model changes.
const similarityThreshold = 0.45

// Temporal decay rate 0.0077 (≈ ln(2)/90) is hardcoded in the SQL queries
// for FindSimilar and FindSimilarWithBoosts, giving a 90-day half-life.

// FindSimilarWithBoosts returns the top-N memories using hybrid search
// (vector similarity + keyword matching) with Reciprocal Rank Fusion (RRF),
// confidence weighting, temporal decay, and soft boosts for alert_type and
// chain_id scope metadata. The returned Memory.Score reflects the final
// ranking score.
//
// The query uses four CTEs:
//  0. kw_tsq — converts queryText to an OR-joined tsquery so any individual
//     term can match (plainto_tsquery uses AND which is too strict for hybrid).
//  1. vector_candidates — ANN search via pgvector HNSW, filtered by similarityThreshold.
//  2. keyword_candidates — full-text search via tsvector/GIN with 'simple' config.
//  3. fused — RRF merge (k=60) of both candidate sets.
//
// The outer query joins back to the table for full columns, applies confidence
// and decay multipliers plus scope boosts, and returns the final ranked results.
func (s *Service) FindSimilarWithBoosts(ctx context.Context, project, queryText string, alertType, chainID *string, limit int) ([]Memory, error) {
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return nil, nil
	}

	queryVec, err := s.embedder.Embed(ctx, queryText, EmbeddingTaskQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}

	vecStr := formatVector(queryVec)
	candidates := max(limit*candidateMultiplier, 20)

	rows, err := s.db.QueryContext(ctx, `
		WITH kw_tsq AS (
			SELECT (regexp_replace(plainto_tsquery('simple', $8)::text, ' & ', ' | ', 'g'))::tsquery AS q
			WHERE numnode(plainto_tsquery('simple', $8)) > 0
		),
		vector_candidates AS (
			SELECT memory_id,
			       ROW_NUMBER() OVER (ORDER BY embedding <=> $2::vector) AS pos
			FROM investigation_memories
			WHERE project = $1
			  AND deprecated = false
			  AND embedding IS NOT NULL
			  AND (1 - (embedding <=> $2::vector)) >= $7
			ORDER BY embedding <=> $2::vector
			LIMIT $3
		),
		keyword_candidates AS (
			SELECT im.memory_id,
			       ROW_NUMBER() OVER (ORDER BY ts_rank(im.search_vector, kw.q) DESC) AS pos
			FROM investigation_memories im
			CROSS JOIN kw_tsq kw
			WHERE im.project = $1
			  AND im.deprecated = false
			  AND im.search_vector @@ kw.q
			ORDER BY ts_rank(im.search_vector, kw.q) DESC
			LIMIT $3
		),
		fused AS (
			SELECT COALESCE(v.memory_id, k.memory_id) AS memory_id,
			       COALESCE(1.0 / (60 + v.pos), 0.0) +
			       COALESCE(1.0 / (60 + k.pos), 0.0) AS rrf_score
			FROM vector_candidates v
			FULL OUTER JOIN keyword_candidates k ON v.memory_id = k.memory_id
		)
		SELECT m.memory_id, m.content, m.category, m.valence, m.confidence,
		       m.seen_count, m.created_at, m.updated_at,
		       f.rrf_score
		         * (0.7 + 0.3 * m.confidence)
		         * EXP(-0.0077 * EXTRACT(EPOCH FROM (NOW() - m.updated_at)) / 86400.0)
		         + CASE WHEN m.alert_type = $4 THEN 0.05 ELSE 0 END
		         + CASE WHEN m.chain_id  = $5 THEN 0.03 ELSE 0 END
		         AS score
		FROM fused f
		JOIN investigation_memories m ON f.memory_id = m.memory_id
		ORDER BY score DESC
		LIMIT $6
	`, project, vecStr, candidates, alertType, chainID, limit, similarityThreshold, queryText)
	if err != nil {
		return nil, fmt.Errorf("hybrid search with boosts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.Category, &m.Valence, &m.Confidence, &m.SeenCount,
			&m.CreatedAt, &m.UpdatedAt, &m.Score); err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// ApplyReflectorActions processes the Reflector's output: creates new memories,
// reinforces confirmed ones, and deprecates contradicted ones.
func (s *Service) ApplyReflectorActions(ctx context.Context, project, sessionID string, alertType, chainID *string, result *ReflectorResult) error {
	if result == nil || result.IsEmpty() {
		return nil
	}

	var errs []error

	for _, action := range result.Create {
		if err := s.createMemory(ctx, project, sessionID, alertType, chainID, initialConfidence, action); err != nil {
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
func (s *Service) createMemory(ctx context.Context, project, sessionID string, alertType, chainID *string, confidence float64, action ReflectorCreateAction) error {
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

// feedbackConfidence is the initial confidence for memories created from human feedback.
const feedbackConfidence = 0.9

// AdjustConfidenceForReview adjusts confidence of all non-deprecated memories
// sourced from sessionID based on the human quality rating.
// Raw SQL: multiplicative confidence updates (LEAST(confidence * N, 1.0))
// cannot be expressed atomically in Ent's update builder.
//   - accurate:            confidence = min(confidence * 1.2, 1.0)
//   - partially_accurate:  confidence = confidence * 0.6
//   - inaccurate:          deprecated = true
func (s *Service) AdjustConfidenceForReview(ctx context.Context, project, sessionID string, rating alertsession.QualityRating) error {
	switch rating {
	case alertsession.QualityRatingAccurate:
		_, err := s.db.ExecContext(ctx, `
			UPDATE investigation_memories
			SET confidence = LEAST(confidence * 1.2, 1.0),
			    updated_at = NOW()
			WHERE source_session_id = $1 AND project = $2 AND deprecated = false
		`, sessionID, project)
		if err != nil {
			return fmt.Errorf("boost confidence for session %s: %w", sessionID, err)
		}
		return nil

	case alertsession.QualityRatingPartiallyAccurate:
		_, err := s.db.ExecContext(ctx, `
			UPDATE investigation_memories
			SET confidence = confidence * 0.6,
			    updated_at = NOW()
			WHERE source_session_id = $1 AND project = $2 AND deprecated = false
		`, sessionID, project)
		if err != nil {
			return fmt.Errorf("degrade confidence for session %s: %w", sessionID, err)
		}
		return nil

	case alertsession.QualityRatingInaccurate:
		_, err := s.db.ExecContext(ctx, `
			UPDATE investigation_memories
			SET deprecated = true,
			    updated_at = NOW()
			WHERE source_session_id = $1 AND project = $2 AND deprecated = false
		`, sessionID, project)
		if err != nil {
			return fmt.Errorf("deprecate memories for session %s: %w", sessionID, err)
		}
		return nil

	default:
		return fmt.Errorf("unknown quality rating %q", rating)
	}
}

// GetByID returns a single memory by ID.
func (s *Service) GetByID(ctx context.Context, memoryID string) (*Detail, error) {
	m, err := s.entClient.InvestigationMemory.Get(ctx, memoryID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrMemoryNotFound
		}
		return nil, fmt.Errorf("get memory %s: %w", memoryID, err)
	}
	return entToDetail(m), nil
}

// GetBySessionID returns all memories extracted from a session (source_session_id).
func (s *Service) GetBySessionID(ctx context.Context, sessionID string) ([]Detail, error) {
	memories, err := s.entClient.InvestigationMemory.Query().
		Where(investigationmemory.SourceSessionIDEQ(sessionID)).
		Order(ent.Asc(investigationmemory.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("get memories for session %s: %w", sessionID, err)
	}
	return entToDetails(memories), nil
}

// GetInjectedBySessionID returns memories that were injected into a session (M2M edge).
func (s *Service) GetInjectedBySessionID(ctx context.Context, sessionID string) ([]Detail, error) {
	memories, err := s.entClient.AlertSession.Query().
		Where(alertsession.IDEQ(sessionID)).
		QueryInjectedMemories().
		Order(ent.Asc(investigationmemory.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("get injected memories for session %s: %w", sessionID, err)
	}
	return entToDetails(memories), nil
}

// ListParams configures the List query.
type ListParams struct {
	Project         string
	Category        *string
	Valence         *string
	Deprecated      *bool
	SourceSessionID *string
	Page            int
	PageSize        int
}

// ListResult holds paginated memory results.
type ListResult struct {
	Memories   []Detail
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// List returns a paginated, filtered list of memories.
func (s *Service) List(ctx context.Context, params ListParams) (*ListResult, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}
	if params.PageSize > 200 {
		params.PageSize = 200
	}

	q := s.entClient.InvestigationMemory.Query().
		Where(investigationmemory.ProjectEQ(params.Project))

	if params.Category != nil {
		q = q.Where(investigationmemory.CategoryEQ(investigationmemory.Category(*params.Category)))
	}
	if params.Valence != nil {
		q = q.Where(investigationmemory.ValenceEQ(investigationmemory.Valence(*params.Valence)))
	}
	if params.Deprecated != nil {
		q = q.Where(investigationmemory.DeprecatedEQ(*params.Deprecated))
	}
	if params.SourceSessionID != nil {
		q = q.Where(investigationmemory.SourceSessionIDEQ(*params.SourceSessionID))
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count memories: %w", err)
	}

	totalPages := (total + params.PageSize - 1) / params.PageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if params.Page > totalPages {
		params.Page = totalPages
	}

	offset := (params.Page - 1) * params.PageSize
	memories, err := q.Clone().
		Order(ent.Desc(investigationmemory.FieldCreatedAt)).
		Limit(params.PageSize).
		Offset(offset).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}

	return &ListResult{
		Memories:   entToDetails(memories),
		Total:      total,
		Page:       params.Page,
		PageSize:   params.PageSize,
		TotalPages: totalPages,
	}, nil
}

// UpdateInput holds the fields for a partial memory update.
type UpdateInput struct {
	Content    *string
	Category   *string
	Valence    *string
	Deprecated *bool
}

// Update applies a partial update to a memory. When content changes, the
// embedding vector is regenerated and both the field updates and the embedding
// write are performed in a single atomic SQL statement. When content does not
// change, the update goes through Ent normally.
//
// The existing record is loaded first so we can skip the (expensive) embedding
// API call and DB write when the provided values are identical to what is
// already stored.
func (s *Service) Update(ctx context.Context, memoryID string, input UpdateInput) (*Detail, error) {
	if input.Content == nil && input.Category == nil && input.Valence == nil && input.Deprecated == nil {
		return s.GetByID(ctx, memoryID)
	}

	existing, err := s.GetByID(ctx, memoryID)
	if err != nil {
		return nil, err
	}

	// Strip inputs that match the persisted values so we only write
	// fields that actually differ, and avoid a needless re-embed.
	if input.Content != nil && *input.Content == existing.Content {
		input.Content = nil
	}
	if input.Category != nil && *input.Category == existing.Category {
		input.Category = nil
	}
	if input.Valence != nil && *input.Valence == existing.Valence {
		input.Valence = nil
	}
	if input.Deprecated != nil && *input.Deprecated == existing.Deprecated {
		input.Deprecated = nil
	}

	if input.Content == nil && input.Category == nil && input.Valence == nil && input.Deprecated == nil {
		return existing, nil
	}

	// Generate embedding before any DB writes so a network failure here
	// doesn't leave committed content with a stale embedding vector.
	var newEmbedding []float32
	if input.Content != nil {
		vec, err := s.embedder.Embed(ctx, *input.Content, EmbeddingTaskDocument)
		if err != nil {
			return nil, fmt.Errorf("re-embed updated content for %s: %w", memoryID, err)
		}
		newEmbedding = vec
	}

	if newEmbedding != nil {
		// Content changed: use a single raw SQL UPDATE so field values and
		// the embedding vector are committed atomically.
		return s.updateContentAndEmbedding(ctx, memoryID, input, newEmbedding)
	}

	// No content change: Ent handles the field update (no embedding concern).
	u := s.entClient.InvestigationMemory.UpdateOneID(memoryID)
	if input.Category != nil {
		u.SetCategory(investigationmemory.Category(*input.Category))
	}
	if input.Valence != nil {
		u.SetValence(investigationmemory.Valence(*input.Valence))
	}
	if input.Deprecated != nil {
		u.SetDeprecated(*input.Deprecated)
	}

	m, err := u.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrMemoryNotFound
		}
		return nil, fmt.Errorf("update memory %s: %w", memoryID, err)
	}
	return entToDetail(m), nil
}

// updateContentAndEmbedding writes all changed fields plus the new embedding
// in a single UPDATE statement, keeping them atomically consistent.
func (s *Service) updateContentAndEmbedding(ctx context.Context, memoryID string, input UpdateInput, embedding []float32) (*Detail, error) {
	// Validate enums before bypassing Ent's built-in validators.
	if input.Category != nil {
		if err := investigationmemory.CategoryValidator(investigationmemory.Category(*input.Category)); err != nil {
			return nil, err
		}
	}
	if input.Valence != nil {
		if err := investigationmemory.ValenceValidator(investigationmemory.Valence(*input.Valence)); err != nil {
			return nil, err
		}
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	setClauses = append(setClauses, fmt.Sprintf("content = $%d", argIdx))
	args = append(args, *input.Content)
	argIdx++

	setClauses = append(setClauses, fmt.Sprintf("embedding = $%d::vector", argIdx))
	args = append(args, formatVector(embedding))
	argIdx++

	if input.Category != nil {
		setClauses = append(setClauses, fmt.Sprintf("category = $%d", argIdx))
		args = append(args, *input.Category)
		argIdx++
	}
	if input.Valence != nil {
		setClauses = append(setClauses, fmt.Sprintf("valence = $%d", argIdx))
		args = append(args, *input.Valence)
		argIdx++
	}
	if input.Deprecated != nil {
		setClauses = append(setClauses, fmt.Sprintf("deprecated = $%d", argIdx))
		args = append(args, *input.Deprecated)
		argIdx++
	}

	args = append(args, memoryID)
	query := fmt.Sprintf(
		`UPDATE investigation_memories SET %s WHERE memory_id = $%d`,
		strings.Join(setClauses, ", "), argIdx,
	)

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update memory %s: %w", memoryID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, ErrMemoryNotFound
	}

	return s.GetByID(ctx, memoryID)
}

// Delete permanently removes a memory.
func (s *Service) Delete(ctx context.Context, memoryID string) error {
	err := s.entClient.InvestigationMemory.DeleteOneID(memoryID).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrMemoryNotFound
		}
		return fmt.Errorf("delete memory %s: %w", memoryID, err)
	}
	return nil
}

func entToDetail(m *ent.InvestigationMemory) *Detail {
	return &Detail{
		ID:              m.ID,
		Project:         m.Project,
		Content:         m.Content,
		Category:        string(m.Category),
		Valence:         string(m.Valence),
		Confidence:      m.Confidence,
		SeenCount:       m.SeenCount,
		SourceSessionID: m.SourceSessionID,
		AlertType:       m.AlertType,
		ChainID:         m.ChainID,
		Deprecated:      m.Deprecated,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		LastSeenAt:      m.LastSeenAt,
	}
}

func entToDetails(memories []*ent.InvestigationMemory) []Detail {
	result := make([]Detail, 0, len(memories))
	for _, m := range memories {
		result = append(result, *entToDetail(m))
	}
	return result
}

// ApplyFeedbackReflectorActions processes Reflector output from human feedback.
// New memories get fixed 0.9 confidence instead of score-derived confidence.
func (s *Service) ApplyFeedbackReflectorActions(ctx context.Context, project, sessionID string, alertType, chainID *string, result *ReflectorResult) error {
	if result == nil || result.IsEmpty() {
		return nil
	}

	var errs []error

	for _, action := range result.Create {
		if err := s.createMemory(ctx, project, sessionID, alertType, chainID, feedbackConfidence, action); err != nil {
			slog.Warn("Failed to create feedback memory",
				"session_id", sessionID, "content_prefix", truncate(action.Content, 80), "error", err)
			errs = append(errs, err)
		}
	}

	for _, action := range result.Reinforce {
		if err := s.reinforce(ctx, project, action.MemoryID); err != nil {
			slog.Warn("Failed to reinforce memory from feedback",
				"memory_id", action.MemoryID, "error", err)
			errs = append(errs, err)
		}
	}

	for _, action := range result.Deprecate {
		if err := s.deprecate(ctx, project, action.MemoryID); err != nil {
			slog.Warn("Failed to deprecate memory from feedback",
				"memory_id", action.MemoryID, "error", err)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// initialConfidence is the fixed confidence for all auto-extracted memories.
// Confidence is a pure human/reinforcement signal — the Reflector is trusted
// as the quality gate at extraction time.
const initialConfidence = 0.7

// ValidateDimensions checks that the configured embedding dimensions match the
// pgvector column size. Returns an error on mismatch.
// Raw SQL: pg_attribute introspection is not available through Ent.
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
