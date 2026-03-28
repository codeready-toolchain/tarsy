package memory_test

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/investigationmemory"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEmbedder returns deterministic vectors for testing.
type fakeEmbedder struct {
	vec []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string, _ memory.EmbeddingTask) ([]float32, error) {
	return f.vec, nil
}

// addMemorySearchColumns adds the pgvector and tsvector columns that Ent
// cannot manage. Mirrors the production migration columns with dim=3 for tests.
func addMemorySearchColumns(t *testing.T, db *stdsql.DB) {
	t.Helper()
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED`)
	require.NoError(t, err)
}

func newTestService(t *testing.T, vec []float32) (*memory.Service, string) {
	t.Helper()
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	// Create a source session (FK target).
	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).
		SetAlertData("test alert").
		SetAgentType("test").
		SetChainID("test-chain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{
		Enabled: true,
		Embedding: config.EmbeddingConfig{
			Dimensions: 3,
		},
	}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: vec}, cfg)
	return svc, sessionID
}

func TestService_ApplyReflectorActions_CreateAndQuery(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	result := &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Check PgBouncer health first", Category: "procedural", Valence: "positive"},
			{Content: "OOMKill uses working_set_bytes", Category: "episodic", Valence: "neutral"},
		},
	}

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, result)
	require.NoError(t, err)

	// Verify memories are queryable via FindSimilar.
	memories, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	assert.Len(t, memories, 2)

	contents := []string{memories[0].Content, memories[1].Content}
	assert.Contains(t, contents, "Check PgBouncer health first")
	assert.Contains(t, contents, "OOMKill uses working_set_bytes")

	for _, m := range memories {
		assert.InDelta(t, 0.7, m.Confidence, 0.01)
		assert.Equal(t, 1, m.SeenCount)
	}

	// Verify category and valence survive the raw INSERT → SELECT round-trip.
	// Catches parameter ordering bugs (e.g. $4/$5 swap).
	categories := []string{memories[0].Category, memories[1].Category}
	assert.Contains(t, categories, "procedural")
	assert.Contains(t, categories, "episodic")

	valences := []string{memories[0].Valence, memories[1].Valence}
	assert.Contains(t, valences, "positive")
	assert.Contains(t, valences, "neutral")
}

func TestService_ApplyReflectorActions_Reinforce(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{0, 1, 0})
	ctx := t.Context()

	// Create a memory first.
	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Always check certs", Category: "procedural", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	// Find it to get the ID.
	memories, err := svc.FindSimilar(ctx, "default", "certs", 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)

	original := memories[0]
	assert.InDelta(t, 0.7, original.Confidence, 0.01)
	assert.Equal(t, 1, original.SeenCount)

	// Reinforce it.
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Reinforce: []memory.ReflectorReinforceAction{{MemoryID: original.ID}},
	})
	require.NoError(t, err)

	// Verify: confidence bumped, seen_count incremented.
	updated, err := svc.FindSimilar(ctx, "default", "certs", 1)
	require.NoError(t, err)
	require.Len(t, updated, 1)
	assert.InDelta(t, 0.77, updated[0].Confidence, 0.01) // 0.7 * 1.1 = 0.77
	assert.Equal(t, 2, updated[0].SeenCount)
}

func TestService_ApplyReflectorActions_Deprecate(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{0, 0, 1})
	ctx := t.Context()

	// Create then deprecate.
	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Outdated fact", Category: "semantic", Valence: "neutral"},
		},
	})
	require.NoError(t, err)

	memories, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	memID := memories[0].ID

	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Deprecate: []memory.ReflectorDeprecateAction{{MemoryID: memID, Reason: "no longer true"}},
	})
	require.NoError(t, err)

	// Deprecated memories should not appear in FindSimilar.
	memories, err = svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	assert.Empty(t, memories)
}

func TestService_ApplyReflectorActions_InvalidEnums(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Bad category", Category: "invented", Valence: "positive"},
			{Content: "Bad valence", Category: "semantic", Valence: "invented"},
			{Content: "Both bad", Category: "foo", Valence: "bar"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid category")
	assert.Contains(t, err.Error(), "invalid valence")

	memories, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	assert.Empty(t, memories, "memories with invalid enums must not be persisted")
}

func TestService_ApplyReflectorActions_NilResult(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 1, 1})

	err := svc.ApplyReflectorActions(t.Context(), "default", sessionID, nil, nil, nil)
	assert.NoError(t, err)
}

func TestService_ApplyReflectorActions_WithScopeMetadata(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("infra").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	alertType := "cpu_high"
	chainID := "infra"
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, &alertType, &chainID, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Scoped memory", Category: "semantic", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	// Verify scope metadata was set via Ent query.
	mem, err := entClient.InvestigationMemory.Query().
		Where(investigationmemory.Project("default")).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, mem.AlertType)
	assert.Equal(t, "cpu_high", *mem.AlertType)
	require.NotNil(t, mem.ChainID)
	assert.Equal(t, "infra", *mem.ChainID)
}

func TestService_FindSimilarWithBoosts(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	alertType := "cpu_high"
	chainID := "infra"

	// Create memories with different scope metadata.
	// Identical embeddings → cosine distance is the same; ranking
	// differences come exclusively from the scope boosts.
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, &alertType, &chainID,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Both scopes", Category: "semantic", Valence: "positive"},
		}})
	require.NoError(t, err)

	err = svc.ApplyReflectorActions(ctx, "default", sessionID, &alertType, nil,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Alert only", Category: "semantic", Valence: "positive"},
		}})
	require.NoError(t, err)

	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "No scopes", Category: "semantic", Valence: "positive"},
		}})
	require.NoError(t, err)

	// Query with both boosts. alert_type match → +0.05, chain_id match → +0.03.
	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "anything", &alertType, &chainID, 10)
	require.NoError(t, err)
	require.Len(t, memories, 3)

	assert.Equal(t, "Both scopes", memories[0].Content)
	assert.Equal(t, "Alert only", memories[1].Content)
	assert.Equal(t, "No scopes", memories[2].Content)

	// Score should be populated and positive for all results.
	for _, m := range memories {
		assert.Greater(t, m.Score, 0.0, "Score should be populated")
	}
	// Boosted memories should have higher scores.
	assert.Greater(t, memories[0].Score, memories[1].Score)
	assert.Greater(t, memories[1].Score, memories[2].Score)
}

// TestService_FindSimilarWithBoosts_CandidateReranking verifies the two-step
// CTE approach: a memory with worse cosine distance but matching scope boosts
// can outrank a closer memory after re-ranking.
func TestService_FindSimilarWithBoosts_CandidateReranking(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Query embedding: [1, 0, 0]
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	alertType := "cpu_high"
	chainID := "infra"

	// Memory 1: identical to query (cosine distance=0), no scope match.
	// Score ≈ 1.0 * (0.7 + 0.3*0.8) * 1.0 = 0.94
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Close but no boost', 'semantic', 'positive', 0.8, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	// Memory 2: farther from query (cosine distance≈0.04), both scopes match.
	// [0.96, 0.28, 0] has |v|=1.0, so cosine_sim=0.96, distance=0.04.
	// Score ≈ 0.96 * (0.7 + 0.3*0.8) * 1.0 + 0.05 + 0.03 ≈ 0.98 → wins.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, alert_type, chain_id,
			 created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Farther with boosts', 'semantic', 'positive', 0.8, 1,
			 $2, $3, $4, NOW(), NOW(), NOW(), false, '[0.96,0.28,0]'::vector)`,
		uuid.New().String(), sessionID, alertType, chainID)
	require.NoError(t, err)

	// limit=1: the boosted memory should rank first despite worse cosine distance.
	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "anything", &alertType, &chainID, 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, "Farther with boosts", memories[0].Content)

	// limit=2: boosted first, then the closer one.
	memories, err = svc.FindSimilarWithBoosts(ctx, "default", "anything", &alertType, &chainID, 2)
	require.NoError(t, err)
	require.Len(t, memories, 2)
	assert.Equal(t, "Farther with boosts", memories[0].Content)
	assert.Equal(t, "Close but no boost", memories[1].Content)
}

// TestService_FindSimilarWithBoosts_SimilarityThreshold verifies that memories
// below the similarity threshold are filtered out, even when the database has
// matching rows.
func TestService_FindSimilarWithBoosts_SimilarityThreshold(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Query vector: [1, 0, 0]
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Memory 1: identical to query → cosine similarity = 1.0 (well above 0.45 threshold).
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Very relevant', 'semantic', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	// Memory 2: nearly orthogonal → cosine similarity ≈ 0.27 (below 0.45 threshold).
	// [0.27, 0.96, 0] is unit-length, cosine_sim([1,0,0], [0.27,0.96,0]) ≈ 0.27.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Irrelevant', 'semantic', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[0.27,0.96,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	t.Run("filters below threshold", func(t *testing.T) {
		memories, err := svc.FindSimilarWithBoosts(ctx, "default", "anything", nil, nil, 10)
		require.NoError(t, err)
		require.Len(t, memories, 1, "only the memory above threshold should be returned")
		assert.Equal(t, "Very relevant", memories[0].Content)
	})

	t.Run("all below threshold returns empty", func(t *testing.T) {
		// Query vector: [0, 0, 1] — orthogonal to both memories.
		orthogonalSvc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{0, 0, 1}}, cfg)
		memories, err := orthogonalSvc.FindSimilarWithBoosts(ctx, "default", "anything", nil, nil, 10)
		require.NoError(t, err)
		assert.Empty(t, memories)
	})
}

// TestService_FindSimilarWithBoosts_TemporalDecay verifies that newer memories
// rank higher than older ones when embeddings and confidence are identical.
func TestService_FindSimilarWithBoosts_TemporalDecay(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Both memories have identical embeddings and confidence, only updated_at differs.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Old memory', 'semantic', 'positive', 0.7, 1,
			 $2, NOW() - INTERVAL '180 days', NOW() - INTERVAL '180 days',
			 NOW() - INTERVAL '180 days', false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Fresh memory', 'semantic', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "anything", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 2)

	assert.Equal(t, "Fresh memory", memories[0].Content, "newer memory should rank first")
	assert.Equal(t, "Old memory", memories[1].Content)
	assert.Greater(t, memories[0].Score, memories[1].Score,
		"fresh memory score should be higher due to temporal decay")
}

// TestService_FindSimilarWithBoosts_ConfidenceRanking verifies that higher-confidence
// memories rank higher when embeddings and age are identical.
func TestService_FindSimilarWithBoosts_ConfidenceRanking(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Both memories have identical embeddings and updated_at, only confidence differs.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Low confidence', 'semantic', 'positive', 0.3, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'High confidence', 'semantic', 'positive', 0.95, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "anything", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 2)

	assert.Equal(t, "High confidence", memories[0].Content, "higher confidence should rank first")
	assert.Equal(t, "Low confidence", memories[1].Content)
	assert.Greater(t, memories[0].Score, memories[1].Score)
}

// TestService_FindSimilarWithBoosts_KeywordOnlyMatch verifies that a memory
// whose embedding is below the vector similarity threshold can still be found
// via the keyword search path of hybrid retrieval.
func TestService_FindSimilarWithBoosts_KeywordOnlyMatch(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Query embedding: [1, 0, 0]
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Memory with content containing "coolify" but embedding orthogonal to
	// query → vector similarity = 0, below threshold. Keyword path should
	// still find it because content contains the query term.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Coolify deployment requires special port config', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[0,1,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "coolify", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1, "keyword-only match should be returned via hybrid search")
	assert.Contains(t, memories[0].Content, "Coolify")
	assert.Greater(t, memories[0].Score, 0.0)
}

// TestService_FindSimilarWithBoosts_VectorOnlyMatch verifies that a memory
// found via vector similarity but not matching any query keywords is still
// returned through the hybrid search path.
func TestService_FindSimilarWithBoosts_VectorOnlyMatch(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Query embedding: [1, 0, 0]
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Memory with embedding identical to query (cosine sim = 1.0) but content
	// shares no keywords with the query text "xyzzyplugh".
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Check PgBouncer health before blaming the database', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "xyzzyplugh", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1, "vector-only match should be returned via hybrid search")
	assert.Contains(t, memories[0].Content, "PgBouncer")
}

// TestService_FindSimilarWithBoosts_BothMatchRRFBoost verifies that a memory
// matching both vector and keyword paths ranks higher (via RRF fusion) than
// memories matching only one path.
func TestService_FindSimilarWithBoosts_BothMatchRRFBoost(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Query embedding: [1, 0, 0]; query text: "coolify"
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	// Memory A: matches both vector (identical embedding) AND keyword ("coolify" in content).
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Coolify needs special port configuration', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	// Memory B: matches vector only (identical embedding, no "coolify" in content).
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Check PgBouncer health before blaming the database', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "coolify", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 2)

	assert.Contains(t, memories[0].Content, "Coolify",
		"memory matching both vector+keyword should rank first due to RRF boost")
	assert.Contains(t, memories[1].Content, "PgBouncer")
	assert.Greater(t, memories[0].Score, memories[1].Score,
		"dual-match should have higher score than vector-only match")
}

// TestService_FindSimilarWithBoosts_ExtraQueryTermsShiftEmbedding reproduces
// the production failure: querying "quantiaia coolify VM" shifts the embedding
// vector away from "coolify", but keyword search catches the exact term match.
func TestService_FindSimilarWithBoosts_ExtraQueryTermsShiftEmbedding(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	// Simulate embedding drift: query "quantiaia coolify VM" produces an
	// embedding that is far from the Coolify memory's embedding. In the
	// fakeEmbedder, query returns [0.5, 0.5, 0.71] (shifted away from [1,0,0]).
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{0.5, 0.5, 0.71}}, cfg)

	// Coolify memory with embedding [1, 0, 0]. Cosine similarity with
	// query [0.5, 0.5, 0.71] ≈ 0.5 — right around threshold, may or may not
	// pass vector search. But keyword "coolify" is an exact match.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Coolify deployments on this host use non-standard ports', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "quantiaia coolify VM", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1, "keyword match should find coolify memory despite embedding drift")
	assert.Contains(t, memories[0].Content, "Coolify")
}

// TestService_FindSimilarWithBoosts_DeprecatedExcludedFromKeywordPath verifies
// that deprecated memories are excluded from both the vector and keyword CTEs,
// so a deprecated memory cannot leak through keyword matching alone.
func TestService_FindSimilarWithBoosts_DeprecatedExcludedFromKeywordPath(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	memID := uuid.New().String()
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'default', 'Coolify needs special port config', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[1,0,0]'::vector)`,
		memID, sessionID)
	require.NoError(t, err)

	// Verify findable before deprecation (both vector + keyword match).
	memories, err := svc.FindSimilarWithBoosts(ctx, "default", "coolify", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1)

	// Deprecate.
	_, err = db.ExecContext(ctx,
		`UPDATE investigation_memories SET deprecated = true WHERE memory_id = $1`, memID)
	require.NoError(t, err)

	// After deprecation: neither vector nor keyword path should return it.
	memories, err = svc.FindSimilarWithBoosts(ctx, "default", "coolify", nil, nil, 10)
	require.NoError(t, err)
	assert.Empty(t, memories, "deprecated memory must not leak through keyword search path")
}

// TestService_FindSimilarWithBoosts_ProjectIsolationKeywordPath verifies that
// keyword matches in one project do not leak into queries for a different project.
func TestService_FindSimilarWithBoosts_ProjectIsolationKeywordPath(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{0, 1, 0}}, cfg)

	// Memory in project "alpha" — embedding orthogonal to query, keyword matches.
	_, err = db.ExecContext(ctx, `
		INSERT INTO investigation_memories
			(memory_id, project, content, category, valence, confidence, seen_count,
			 source_session_id, created_at, updated_at, last_seen_at, deprecated, embedding)
		VALUES ($1, 'alpha', 'Coolify deployment requires special ports', 'procedural', 'positive', 0.7, 1,
			 $2, NOW(), NOW(), NOW(), false, '[0,1,0]'::vector)`,
		uuid.New().String(), sessionID)
	require.NoError(t, err)

	// Query project "beta" — keyword "coolify" matches the alpha memory's content
	// but must not cross the project boundary.
	memories, err := svc.FindSimilarWithBoosts(ctx, "beta", "coolify", nil, nil, 10)
	require.NoError(t, err)
	assert.Empty(t, memories, "keyword match from another project must not leak across project boundary")

	// Sanity: same keyword query against "alpha" should find it.
	memories, err = svc.FindSimilarWithBoosts(ctx, "alpha", "coolify", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Contains(t, memories[0].Content, "Coolify")
}

// TestService_FindSimilarWithBoosts_EmptyQueryText verifies that empty or
// whitespace-only queryText short-circuits without hitting the embedder or DB.
func TestService_FindSimilarWithBoosts_EmptyQueryText(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Memory that would match via vector", Category: "semantic", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	for _, queryText := range []string{"", " ", "\t\n "} {
		t.Run("query="+fmt.Sprintf("%q", queryText), func(t *testing.T) {
			memories, err := svc.FindSimilarWithBoosts(ctx, "default", queryText, nil, nil, 10)
			require.NoError(t, err)
			assert.Empty(t, memories, "empty/whitespace query should return no results")
		})
	}
}

func TestService_CreateMemory_PersistsAllColumns(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	addMemorySearchColumns(t, db)

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}}, cfg)

	alertType := "cpu_high"
	chainID := "infra"
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, &alertType, &chainID, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Always check logs first", Category: "procedural", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	// Read back via Ent to verify every column written by the raw INSERT.
	mem, err := entClient.InvestigationMemory.Query().
		Where(investigationmemory.Project("default")).
		Only(ctx)
	require.NoError(t, err)

	assert.NotEmpty(t, mem.ID)
	assert.Equal(t, "default", mem.Project)
	assert.Equal(t, "Always check logs first", mem.Content)
	assert.Equal(t, investigationmemory.CategoryProcedural, mem.Category)
	assert.Equal(t, investigationmemory.ValencePositive, mem.Valence)
	assert.InDelta(t, 0.7, mem.Confidence, 0.01)
	assert.Equal(t, 1, mem.SeenCount)
	assert.Equal(t, sessionID, mem.SourceSessionID)
	require.NotNil(t, mem.AlertType)
	assert.Equal(t, "cpu_high", *mem.AlertType)
	require.NotNil(t, mem.ChainID)
	assert.Equal(t, "infra", *mem.ChainID)
	assert.False(t, mem.Deprecated)
	assert.False(t, mem.CreatedAt.IsZero())
	assert.False(t, mem.UpdatedAt.IsZero())
	assert.False(t, mem.LastSeenAt.IsZero())

	// Verify the embedding (pgvector, not managed by Ent) was written
	// atomically by confirming the record is returned by similarity search.
	memories, err := svc.FindSimilar(ctx, "default", "anything", 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, mem.ID, memories[0].ID)
}

func TestService_ValidateDimensions(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	_, err := db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(768)`)
	require.NoError(t, err)

	t.Run("matching dimensions", func(t *testing.T) {
		cfg := &config.MemoryConfig{Embedding: config.EmbeddingConfig{Dimensions: 768}}
		svc := memory.NewService(entClient, db, nil, cfg)
		assert.NoError(t, svc.ValidateDimensions(ctx))
	})

	t.Run("mismatched dimensions", func(t *testing.T) {
		cfg := &config.MemoryConfig{Embedding: config.EmbeddingConfig{Dimensions: 1024}}
		svc := memory.NewService(entClient, db, nil, cfg)
		err := svc.ValidateDimensions(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "1024")
		assert.Contains(t, err.Error(), "768")
	})
}
