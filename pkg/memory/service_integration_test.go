package memory_test

import (
	"context"
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

func newTestService(t *testing.T, vec []float32) (*memory.Service, string) {
	t.Helper()
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	// Enable pgvector in this test schema.
	_, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	require.NoError(t, err)

	// Create the embedding column (Ent doesn't manage it).
	_, err = db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)

	// Create a source session (FK target).
	sessionID := uuid.New().String()
	_, err = entClient.AlertSession.Create().
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

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 75, result)
	require.NoError(t, err)

	// Verify memories are queryable via FindSimilar.
	memories, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	assert.Len(t, memories, 2)

	contents := []string{memories[0].Content, memories[1].Content}
	assert.Contains(t, contents, "Check PgBouncer health first")
	assert.Contains(t, contents, "OOMKill uses working_set_bytes")

	// Score 75 → confidence 0.6
	for _, m := range memories {
		assert.InDelta(t, 0.6, m.Confidence, 0.01)
		assert.Equal(t, 1, m.SeenCount)
	}
}

func TestService_ApplyReflectorActions_Reinforce(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{0, 1, 0})
	ctx := t.Context()

	// Create a memory first.
	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80, &memory.ReflectorResult{
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
	assert.InDelta(t, 0.8, original.Confidence, 0.01) // score 80 → 0.8
	assert.Equal(t, 1, original.SeenCount)

	// Reinforce it.
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80, &memory.ReflectorResult{
		Reinforce: []memory.ReflectorReinforceAction{{MemoryID: original.ID}},
	})
	require.NoError(t, err)

	// Verify: confidence bumped, seen_count incremented.
	updated, err := svc.FindSimilar(ctx, "default", "certs", 1)
	require.NoError(t, err)
	require.Len(t, updated, 1)
	assert.InDelta(t, 0.88, updated[0].Confidence, 0.01) // 0.8 * 1.1 = 0.88
	assert.Equal(t, 2, updated[0].SeenCount)
}

func TestService_ApplyReflectorActions_Deprecate(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{0, 0, 1})
	ctx := t.Context()

	// Create then deprecate.
	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 60, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Outdated fact", Category: "semantic", Valence: "neutral"},
		},
	})
	require.NoError(t, err)

	memories, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	memID := memories[0].ID

	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 60, &memory.ReflectorResult{
		Deprecate: []memory.ReflectorDeprecateAction{{MemoryID: memID, Reason: "no longer true"}},
	})
	require.NoError(t, err)

	// Deprecated memories should not appear in FindSimilar.
	memories, err = svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	assert.Empty(t, memories)
}

func TestService_ApplyReflectorActions_NilResult(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 1, 1})

	err := svc.ApplyReflectorActions(t.Context(), "default", sessionID, nil, nil, 50, nil)
	assert.NoError(t, err)
}

func TestService_ApplyReflectorActions_WithScopeMetadata(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	_, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)

	sessionID := uuid.New().String()
	_, err = entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("infra").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	alertType := "cpu_high"
	chainID := "infra"
	err = svc.ApplyReflectorActions(ctx, "default", sessionID, &alertType, &chainID, 90, &memory.ReflectorResult{
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

func TestService_ValidateDimensions(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	_, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(768)`)
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
