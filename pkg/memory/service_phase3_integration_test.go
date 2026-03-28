package memory_test

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────
// CRUD: GetByID, GetBySessionID, Update, Delete
// ─────────────────────────────────────────────────────────────

func seedMemory(t *testing.T) (*memory.Service, string, string) {
	t.Helper()
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Always check logs", Category: "procedural", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	memories, err := svc.FindSimilar(ctx, "default", "logs", 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	return svc, sessionID, memories[0].ID
}

func TestService_GetByID(t *testing.T) {
	svc, _, memID := seedMemory(t)
	ctx := t.Context()

	t.Run("existing memory", func(t *testing.T) {
		m, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)
		assert.Equal(t, memID, m.ID)
		assert.Equal(t, "Always check logs", m.Content)
		assert.Equal(t, "procedural", m.Category)
		assert.Equal(t, "positive", m.Valence)
		assert.False(t, m.Deprecated)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.GetByID(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, memory.ErrMemoryNotFound)
	})
}

func TestService_GetBySessionID(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "First learning", Category: "procedural", Valence: "positive"},
			{Content: "Second learning", Category: "semantic", Valence: "neutral"},
		},
	})
	require.NoError(t, err)

	t.Run("returns memories for session", func(t *testing.T) {
		memories, err := svc.GetBySessionID(ctx, sessionID)
		require.NoError(t, err)
		assert.Len(t, memories, 2)
		for _, m := range memories {
			assert.Equal(t, sessionID, m.SourceSessionID)
		}
	})

	t.Run("empty for unknown session", func(t *testing.T) {
		memories, err := svc.GetBySessionID(ctx, "unknown-session")
		require.NoError(t, err)
		assert.Empty(t, memories)
	})
}

func TestService_Update(t *testing.T) {
	svc, _, memID := seedMemory(t)
	ctx := t.Context()

	t.Run("partial update content", func(t *testing.T) {
		newContent := "Updated content"
		m, err := svc.Update(ctx, memID, memory.UpdateInput{Content: &newContent})
		require.NoError(t, err)
		assert.Equal(t, "Updated content", m.Content)
		assert.Equal(t, "procedural", m.Category)
	})

	t.Run("set deprecated", func(t *testing.T) {
		dep := true
		m, err := svc.Update(ctx, memID, memory.UpdateInput{Deprecated: &dep})
		require.NoError(t, err)
		assert.True(t, m.Deprecated)
	})

	t.Run("no-op returns current state", func(t *testing.T) {
		m, err := svc.Update(ctx, memID, memory.UpdateInput{})
		require.NoError(t, err)
		assert.Equal(t, memID, m.ID)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Update(ctx, "nonexistent", memory.UpdateInput{})
		assert.ErrorIs(t, err, memory.ErrMemoryNotFound)
	})
}

func TestService_Delete(t *testing.T) {
	svc, _, memID := seedMemory(t)
	ctx := t.Context()

	t.Run("deletes existing", func(t *testing.T) {
		err := svc.Delete(ctx, memID)
		require.NoError(t, err)

		_, err = svc.GetByID(ctx, memID)
		assert.ErrorIs(t, err, memory.ErrMemoryNotFound)
	})

	t.Run("not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		assert.ErrorIs(t, err, memory.ErrMemoryNotFound)
	})
}

// ─────────────────────────────────────────────────────────────
// List with pagination and filters
// ─────────────────────────────────────────────────────────────

func TestService_List(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Memory A", Category: "procedural", Valence: "positive"},
			{Content: "Memory B", Category: "semantic", Valence: "negative"},
			{Content: "Memory C", Category: "procedural", Valence: "neutral"},
		},
	})
	require.NoError(t, err)

	t.Run("unfiltered", func(t *testing.T) {
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 1, PageSize: 10})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Total)
		assert.Len(t, result.Memories, 3)
		assert.Equal(t, 1, result.TotalPages)
	})

	t.Run("filter by category", func(t *testing.T) {
		cat := "procedural"
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 1, PageSize: 10, Category: &cat})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Total)
		for _, m := range result.Memories {
			assert.Equal(t, "procedural", m.Category)
		}
	})

	t.Run("filter by valence", func(t *testing.T) {
		val := "negative"
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 1, PageSize: 10, Valence: &val})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "Memory B", result.Memories[0].Content)
	})

	t.Run("filter by deprecated", func(t *testing.T) {
		dep := false
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 1, PageSize: 10, Deprecated: &dep})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Total)
	})

	t.Run("pagination", func(t *testing.T) {
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 1, PageSize: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Total)
		assert.Len(t, result.Memories, 2)
		assert.Equal(t, 2, result.TotalPages)
		assert.Equal(t, 1, result.Page)
	})

	t.Run("page beyond total clamps to last", func(t *testing.T) {
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 99, PageSize: 2})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Page)
	})

	t.Run("defaults for invalid params", func(t *testing.T) {
		result, err := svc.List(ctx, memory.ListParams{Project: "default", Page: 0, PageSize: 0})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Page)
		assert.Equal(t, 20, result.PageSize)
	})

	t.Run("empty result", func(t *testing.T) {
		result, err := svc.List(ctx, memory.ListParams{Project: "nonexistent", Page: 1, PageSize: 10})
		require.NoError(t, err)
		assert.Equal(t, 0, result.Total)
		assert.Empty(t, result.Memories)
		assert.Equal(t, 1, result.TotalPages)
	})
}

// ─────────────────────────────────────────────────────────────
// AdjustConfidenceForReview
// ─────────────────────────────────────────────────────────────

func TestService_AdjustConfidenceForReview(t *testing.T) {
	t.Run("accurate boosts confidence", func(t *testing.T) {
		svc, sessionID, memID := seedMemory(t)
		ctx := t.Context()

		original, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)

		err = svc.AdjustConfidenceForReview(ctx, "default", sessionID, alertsession.QualityRatingAccurate)
		require.NoError(t, err)

		updated, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)
		assert.InDelta(t, original.Confidence*1.2, updated.Confidence, 0.01)
		assert.False(t, updated.Deprecated)
	})

	t.Run("partially_accurate degrades confidence", func(t *testing.T) {
		svc, sessionID, memID := seedMemory(t)
		ctx := t.Context()

		original, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)

		err = svc.AdjustConfidenceForReview(ctx, "default", sessionID, alertsession.QualityRatingPartiallyAccurate)
		require.NoError(t, err)

		updated, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)
		assert.InDelta(t, original.Confidence*0.6, updated.Confidence, 0.01)
	})

	t.Run("inaccurate deprecates", func(t *testing.T) {
		svc, sessionID, memID := seedMemory(t)
		ctx := t.Context()

		err := svc.AdjustConfidenceForReview(ctx, "default", sessionID, alertsession.QualityRatingInaccurate)
		require.NoError(t, err)

		updated, err := svc.GetByID(ctx, memID)
		require.NoError(t, err)
		assert.True(t, updated.Deprecated)
	})

	t.Run("unknown rating returns error", func(t *testing.T) {
		svc, sessionID := newTestService(t, []float32{1, 0, 0})
		err := svc.AdjustConfidenceForReview(t.Context(), "default", sessionID, alertsession.QualityRating("bogus"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown quality rating")
	})
}

// ─────────────────────────────────────────────────────────────
// ApplyFeedbackReflectorActions (fixed 0.9 confidence)
// ─────────────────────────────────────────────────────────────

func TestService_ApplyFeedbackReflectorActions(t *testing.T) {
	t.Run("creates at 0.9 confidence", func(t *testing.T) {
		svc, sessionID := newTestService(t, []float32{0, 1, 0})
		ctx := t.Context()

		err := svc.ApplyFeedbackReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
			Create: []memory.ReflectorCreateAction{
				{Content: "From human feedback", Category: "procedural", Valence: "positive"},
			},
		})
		require.NoError(t, err)

		memories, err := svc.FindSimilar(ctx, "default", "anything", 1)
		require.NoError(t, err)
		require.Len(t, memories, 1)
		assert.Equal(t, "From human feedback", memories[0].Content)
		assert.InDelta(t, 0.9, memories[0].Confidence, 0.01)
	})

	t.Run("nil result is no-op", func(t *testing.T) {
		svc, sessionID := newTestService(t, []float32{1, 0, 0})
		err := svc.ApplyFeedbackReflectorActions(t.Context(), "default", sessionID, nil, nil, nil)
		assert.NoError(t, err)
	})

	t.Run("reinforce and deprecate", func(t *testing.T) {
		svc, sessionID := newTestService(t, []float32{1, 0, 0})
		ctx := t.Context()

		err := svc.ApplyFeedbackReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
			Create: []memory.ReflectorCreateAction{
				{Content: "Mem to reinforce", Category: "semantic", Valence: "positive"},
				{Content: "Mem to deprecate", Category: "semantic", Valence: "negative"},
			},
		})
		require.NoError(t, err)

		all, err := svc.FindSimilar(ctx, "default", "anything", 10)
		require.NoError(t, err)
		require.Len(t, all, 2)

		var reinforceID, deprecateID string
		for _, m := range all {
			if m.Content == "Mem to reinforce" {
				reinforceID = m.ID
			} else {
				deprecateID = m.ID
			}
		}

		err = svc.ApplyFeedbackReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
			Reinforce: []memory.ReflectorReinforceAction{{MemoryID: reinforceID}},
			Deprecate: []memory.ReflectorDeprecateAction{{MemoryID: deprecateID, Reason: "wrong"}},
		})
		require.NoError(t, err)

		reinforced, err := svc.GetByID(ctx, reinforceID)
		require.NoError(t, err)
		assert.Equal(t, 2, reinforced.SeenCount)

		active, err := svc.FindSimilar(ctx, "default", "anything", 10)
		require.NoError(t, err)
		assert.Len(t, active, 1, "deprecated memory should not appear in FindSimilar")
	})
}

// ─────────────────────────────────────────────────────────────
// GetInjectedBySessionID
// ─────────────────────────────────────────────────────────────

func TestService_GetInjectedBySessionID(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := t.Context()

	_, err := db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)

	sessionID := uuid.New().String()
	_, err = entClient.AlertSession.Create().
		SetID(sessionID).SetAlertData("test").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").Save(ctx)
	require.NoError(t, err)

	cfg := &config.MemoryConfig{Enabled: true, Embedding: config.EmbeddingConfig{Dimensions: 3}}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	err = svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, &memory.ReflectorResult{
		Create: []memory.ReflectorCreateAction{
			{Content: "Injected memory", Category: "procedural", Valence: "positive"},
		},
	})
	require.NoError(t, err)

	memories, err := svc.FindSimilar(ctx, "default", "anything", 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	memID := memories[0].ID

	targetSessionID := uuid.New().String()
	_, err = entClient.AlertSession.Create().
		SetID(targetSessionID).SetAlertData("test2").SetAgentType("test").
		SetChainID("test-chain").SetStatus("completed").
		AddInjectedMemoryIDs(memID).
		Save(ctx)
	require.NoError(t, err)

	t.Run("returns injected memories", func(t *testing.T) {
		injected, err := svc.GetInjectedBySessionID(ctx, targetSessionID)
		require.NoError(t, err)
		require.Len(t, injected, 1)
		assert.Equal(t, memID, injected[0].ID)
	})

	t.Run("empty for session with no injected memories", func(t *testing.T) {
		injected, err := svc.GetInjectedBySessionID(ctx, sessionID)
		require.NoError(t, err)
		assert.Empty(t, injected)
	})
}
