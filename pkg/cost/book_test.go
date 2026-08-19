package cost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimate_OverrideWins(t *testing.T) {
	book, err := NewBook(&Config{
		Enabled: true,
		ModelRates: map[string]ModelRateOverride{
			"gemini-3.1-pro-preview": {
				InputPerMillion:  1.0,
				OutputPerMillion: 2.0,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cost, prov := book.Estimate("gemini-3.1-pro-preview", 1_000_000, 1_000_000, 0)
	if cost == nil {
		t.Fatal("expected priced estimate")
	}
	if prov != ProvenanceOverride {
		t.Fatalf("provenance = %q, want %q", prov, ProvenanceOverride)
	}
	// 1.0 + 2.0 = 3.0 USD for 1M in + 1M out
	if *cost < 2.999 || *cost > 3.001 {
		t.Fatalf("cost = %v, want ~3.0", *cost)
	}
}

func TestEstimate_Unpriced(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	cost, prov := book.Estimate("totally-unknown-model-xyz", 100, 50, 0)
	if cost != nil {
		t.Fatalf("expected nil cost, got %v", *cost)
	}
	if prov != ProvenanceUnpriced {
		t.Fatalf("provenance = %q, want unpriced", prov)
	}
}

func TestEstimate_Disabled(t *testing.T) {
	book, err := NewBook(&Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}

	cost, prov := book.Estimate("gemini-3.6-flash", 1000, 500, 0)
	if cost != nil {
		t.Fatalf("expected nil when disabled, got %v", *cost)
	}
	if prov != ProvenanceUnpriced {
		t.Fatalf("provenance = %q, want unpriced", prov)
	}
	if book.Enabled() {
		t.Fatal("Enabled() should be false")
	}
}

func TestEstimate_NilBook(t *testing.T) {
	var book *Book
	cost, prov := book.Estimate("gemini-3.6-flash", 100, 50, 0)
	if cost != nil || prov != ProvenanceUnpriced {
		t.Fatalf("nil book: cost=%v prov=%q", cost, prov)
	}
	if book.Enabled() {
		t.Fatal("nil book should not be enabled")
	}
}

func TestEstimate_GeminiAbove200k(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot has gemini-3.1-pro-preview with above_200k rates:
	// base: 2e-6 / 1.2e-5; above: 4e-6 / 1.8e-5
	below, provBelow := book.Estimate("gemini-3.1-pro-preview", 100_000, 1000, 0)
	if below == nil {
		t.Fatal("expected priced below threshold")
	}
	if provBelow != Provenance("snapshot:gemini-3.1-pro-preview") {
		t.Fatalf("provenance = %q", provBelow)
	}

	above, provAbove := book.Estimate("gemini-3.1-pro-preview", 200_000, 1000, 0)
	if above == nil {
		t.Fatal("expected priced above threshold")
	}
	if provAbove != Provenance("snapshot:gemini-3.1-pro-preview") {
		t.Fatalf("provenance = %q", provAbove)
	}

	if *above <= *below {
		t.Fatalf("above-200k cost %v should exceed below cost %v", *above, *below)
	}

	// Spot-check above rate: 200k*4e-6 + 1000*1.8e-5 = 0.8 + 0.018 = 0.818
	want := 200_000*4e-6 + 1000*1.8e-5
	if diff := *above - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("above cost = %v, want %v", *above, want)
	}
}

func TestEstimate_ClaudeSnapshotRates(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		model    string
		wantUSD  float64
		wantProv Provenance
	}{
		{
			name:     "sonnet-5",
			model:    "claude-sonnet-5",
			wantUSD:  2.0 + 10.0, // 1M input @ $2 + 1M output @ $10
			wantProv: Provenance("snapshot:claude-sonnet-5"),
		},
		{
			name:     "opus-5",
			model:    "claude-opus-5",
			wantUSD:  5.0 + 25.0, // 1M input @ $5 + 1M output @ $25
			wantProv: Provenance("snapshot:claude-opus-5"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			costUSD, prov := book.Estimate(tt.model, 1_000_000, 1_000_000, 0)
			require.NotNil(t, costUSD)
			assert.Equal(t, tt.wantProv, prov)
			assert.InDelta(t, tt.wantUSD, *costUSD, 1e-9)
		})
	}
}

func TestEstimate_TieredPricing(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// dashscope/qwen-flash: tier0 [0,256k) cheaper than tier1 [256k,1M)
	low, _ := book.Estimate("dashscope/qwen-flash", 1000, 1000, 0)
	high, _ := book.Estimate("dashscope/qwen-flash", 300_000, 1000, 0)
	if low == nil || high == nil {
		t.Fatal("expected both tiers priced")
	}
	if *high <= *low {
		t.Fatalf("higher tier cost %v should exceed lower %v", *high, *low)
	}
}

func TestEstimate_ThinkingTokens(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// gemini-3.6-flash has output_cost_per_reasoning_token
	without, _ := book.Estimate("gemini-3.6-flash", 1000, 1000, 0)
	with, _ := book.Estimate("gemini-3.6-flash", 1000, 1000, 500)
	if without == nil || with == nil {
		t.Fatal("expected priced")
	}
	if *with <= *without {
		t.Fatalf("with thinking %v should exceed without %v", *with, *without)
	}
}

func TestEstimate_HeuristicSuffixMatch(t *testing.T) {
	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// TARSy stores bare model; snapshot has gemini/gemini-3.6-flash and gemini-3.6-flash (exact).
	// Bare name hits exact first.
	cost, prov := book.Estimate("gemini-3.6-flash", 1000, 100, 0)
	if cost == nil {
		t.Fatal("expected match")
	}
	if prov != Provenance("snapshot:gemini-3.6-flash") {
		t.Fatalf("provenance = %q", prov)
	}
}

func TestBook_CatalogFetch(t *testing.T) {
	payload := map[string]any{
		"test-model": map[string]any{
			"input_cost_per_token":  1e-6,
			"output_cost_per_token": 2e-6,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	book.OverrideHTTPClientForTest(srv.Client())
	book.SetCatalogURLForTest(srv.URL)

	book.refreshOnce(t.Context())

	cost, prov := book.Estimate("test-model", 1_000_000, 0, 0)
	if cost == nil {
		t.Fatal("expected catalog-priced model")
	}
	if prov != Provenance("catalog:test-model") {
		t.Fatalf("provenance = %q", prov)
	}
	if *cost < 0.999 || *cost > 1.001 {
		t.Fatalf("cost = %v, want ~1.0", *cost)
	}

	st := book.Status()
	if st.Catalog.Source != "catalog" {
		t.Fatalf("status source = %q, want catalog", st.Catalog.Source)
	}
	if st.Catalog.EntryCount < 1 {
		t.Fatal("expected catalog entries")
	}
}

func TestBook_StatusUsesSnapshotWhenFetchFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	book, err := NewBook(&Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	book.OverrideHTTPClientForTest(srv.Client())
	book.SetCatalogURLForTest(srv.URL)
	book.refreshOnce(t.Context())

	st := book.Status()
	if st.Catalog.Source != "snapshot" {
		t.Fatalf("source = %q, want snapshot", st.Catalog.Source)
	}
	if st.Catalog.LastError == "" {
		t.Fatal("expected last_error set")
	}

	// Snapshot still prices known models.
	cost, _ := book.Estimate("gemini-3.6-flash", 100, 50, 0)
	if cost == nil {
		t.Fatal("snapshot should still price")
	}
}

func TestBook_OverrideBeatsCatalog(t *testing.T) {
	payload := map[string]any{
		"gemini-3.6-flash": map[string]any{
			"input_cost_per_token":  9e-6,
			"output_cost_per_token": 9e-6,
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	book, err := NewBook(&Config{
		Enabled: true,
		ModelRates: map[string]ModelRateOverride{
			"gemini-3.6-flash": {InputPerMillion: 1.0, OutputPerMillion: 1.0},
		},
	})
	require.NoError(t, err)
	book.OverrideHTTPClientForTest(srv.Client())
	book.SetCatalogURLForTest(srv.URL)
	book.refreshOnce(t.Context())

	costUSD, prov := book.Estimate("gemini-3.6-flash", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, ProvenanceOverride, prov)
	assert.InDelta(t, 1.0, *costUSD, 1e-9)
}

func TestBook_EstimateConcurrentWithRefresh(t *testing.T) {
	payload := map[string]any{
		"concurrent-model": map[string]any{
			"input_cost_per_token":  1e-6,
			"output_cost_per_token": 2e-6,
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	book, err := NewBook(&Config{Enabled: true})
	require.NoError(t, err)
	book.OverrideHTTPClientForTest(srv.Client())
	book.SetCatalogURLForTest(srv.URL)

	// Seed the remote catalog so concurrent Estimates always have a priced path.
	book.refreshOnce(t.Context())
	st := book.Status()
	require.True(t, st.Enabled)
	require.Equal(t, "catalog", st.Catalog.Source)
	require.Equal(t, 1, st.Catalog.EntryCount)
	require.Empty(t, st.Catalog.LastError)
	require.NotNil(t, st.Catalog.LastFetch)

	const (
		snapshotCost = 100*1.5e-6 + 50*7.5e-6 // gemini-3.6-flash snapshot rates
		catalogCost  = 1000*1e-6 + 100*2e-6   // concurrent-model mock catalog rates
	)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				book.refreshOnce(t.Context())

				snapCost, snapProv := book.Estimate("gemini-3.6-flash", 100, 50, 0)
				assert.NotNil(t, snapCost)
				assert.Equal(t, Provenance("snapshot:gemini-3.6-flash"), snapProv)
				if snapCost != nil {
					assert.InDelta(t, snapshotCost, *snapCost, 1e-12)
				}

				catCost, catProv := book.Estimate("concurrent-model", 1000, 100, 0)
				assert.NotNil(t, catCost)
				assert.Equal(t, Provenance("catalog:concurrent-model"), catProv)
				if catCost != nil {
					assert.InDelta(t, catalogCost, *catCost, 1e-12)
				}

				st := book.Status()
				assert.True(t, st.Enabled)
				assert.Equal(t, "catalog", st.Catalog.Source)
				assert.Equal(t, 1, st.Catalog.EntryCount)
				assert.Empty(t, st.Catalog.LastError)
				assert.NotNil(t, st.Catalog.LastFetch)
			}
		})
	}
	wg.Wait()
}

func TestEstimate_PromotionActive(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			ID:               "gemini-3.7-flash-intro",
			Model:            "promo-only-model",
			Start:            &start,
			End:              end,
			InputPerMillion:  0.75,
			OutputPerMillion: 3.75,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("promo-only-model", 1_000_000, 1_000_000, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:gemini-3.7-flash-intro"), prov)
	assert.InDelta(t, 4.5, *costUSD, 1e-9)
}

func TestEstimate_PromotionBeatsModelRates(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		ModelRates: map[string]ModelRateOverride{
			"shared-model": {InputPerMillion: 10.0, OutputPerMillion: 20.0},
		},
		Promotions: []Promotion{{
			Model:            "shared-model",
			Start:            &start,
			End:              end,
			InputPerMillion:  1.0,
			OutputPerMillion: 2.0,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("shared-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:shared-model"), prov)
	assert.InDelta(t, 1.0, *costUSD, 1e-9)
}

func TestEstimate_PromotionBeforeStart(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		ModelRates: map[string]ModelRateOverride{
			"shared-model": {InputPerMillion: 10.0, OutputPerMillion: 20.0},
		},
		Promotions: []Promotion{{
			Model:            "shared-model",
			Start:            &start,
			End:              end,
			InputPerMillion:  1.0,
			OutputPerMillion: 2.0,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("shared-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, ProvenanceOverride, prov)
	assert.InDelta(t, 10.0, *costUSD, 1e-9)
}

func TestEstimate_PromotionAfterEnd(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		ModelRates: map[string]ModelRateOverride{
			"shared-model": {InputPerMillion: 10.0, OutputPerMillion: 20.0},
		},
		Promotions: []Promotion{{
			Model:            "shared-model",
			Start:            &start,
			End:              end,
			InputPerMillion:  1.0,
			OutputPerMillion: 2.0,
		}},
	})
	require.NoError(t, err)
	// Half-open: end instant is expired.
	book.SetNowForTest(func() time.Time { return end })

	costUSD, prov := book.Estimate("shared-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, ProvenanceOverride, prov)
	assert.InDelta(t, 10.0, *costUSD, 1e-9)
}

func TestEstimate_PromotionOmittedStart(t *testing.T) {
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			Model:            "open-start-model",
			End:              end,
			InputPerMillion:  0.5,
			OutputPerMillion: 1.5,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("open-start-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:open-start-model"), prov)
	assert.InDelta(t, 0.5, *costUSD, 1e-9)
}

func TestEstimate_PromotionExactNameMiss(t *testing.T) {
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			Model:            "gemini-3.7-flash",
			End:              end,
			InputPerMillion:  0.75,
			OutputPerMillion: 3.75,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})

	// Prefixed alias must not match promotion (exact name only).
	costUSD, prov := book.Estimate("gemini/gemini-3.7-flash", 1000, 0, 0)
	// May still be priced via snapshot heuristics, but not via promotion.
	assert.NotEqual(t, Provenance("promotion:gemini-3.7-flash"), prov)
	_ = costUSD
}

func TestEstimate_PromotionDisabledIgnored(t *testing.T) {
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: false,
		Promotions: []Promotion{{
			Model:            "promo-model",
			End:              end,
			InputPerMillion:  0.75,
			OutputPerMillion: 3.75,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("promo-model", 1000, 500, 0)
	assert.Nil(t, costUSD)
	assert.Equal(t, ProvenanceUnpriced, prov)
}

func TestEstimate_PromotionHalfOpenDateBoundary(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			Model:            "boundary-model",
			Start:            &start,
			End:              end,
			InputPerMillion:  1.0,
			OutputPerMillion: 1.0,
		}},
	})
	require.NoError(t, err)

	book.SetNowForTest(func() time.Time { return start })
	costUSD, prov := book.Estimate("boundary-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:boundary-model"), prov)

	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	})
	costUSD, prov = book.Estimate("boundary-model", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:boundary-model"), prov)

	book.SetNowForTest(func() time.Time { return end })
	costUSD, prov = book.Estimate("boundary-model", 1_000_000, 0, 0)
	assert.Nil(t, costUSD)
	assert.Equal(t, ProvenanceUnpriced, prov)
}

func TestStatus_PromotionLifecycle(t *testing.T) {
	upcomingStart := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	upcomingEnd := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	activeStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	activeEnd := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	expiredEnd := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{
			{ID: "upcoming", Model: "m1", Start: &upcomingStart, End: upcomingEnd, InputPerMillion: 1, OutputPerMillion: 1},
			{ID: "active", Model: "m2", Start: &activeStart, End: activeEnd, InputPerMillion: 1, OutputPerMillion: 1},
			{Model: "m3", End: expiredEnd, InputPerMillion: 1, OutputPerMillion: 1}, // omitted start, already expired
		},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})

	st := book.Status()
	require.Len(t, st.Promotions, 3)
	assert.Equal(t, PromotionUpcoming, st.Promotions[0].Status)
	assert.Equal(t, PromotionActive, st.Promotions[1].Status)
	assert.Equal(t, PromotionExpired, st.Promotions[2].Status)
	assert.Nil(t, st.Promotions[2].Start)
}

func TestEstimate_PromotionThinkingUsesOutputRate(t *testing.T) {
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			Model:            "think-model",
			End:              end,
			InputPerMillion:  1.0,
			OutputPerMillion: 2.0,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})

	// 1M in + 0.5M out + 0.1M thinking@output = 1 + 1 + 0.2 = 2.2
	costUSD, prov := book.Estimate("think-model", 1_000_000, 500_000, 100_000)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:think-model"), prov)
	assert.InDelta(t, 2.2, *costUSD, 1e-9)
}

func TestEstimate_PromotionBeatsSnapshot(t *testing.T) {
	// Snapshot has gemini-3.7-flash intro rates; promo with different rates must win.
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	book, err := NewBook(&Config{
		Enabled: true,
		Promotions: []Promotion{{
			ID:               "flash-intro",
			Model:            "gemini-3.7-flash",
			End:              end,
			InputPerMillion:  0.1,
			OutputPerMillion: 0.2,
		}},
	})
	require.NoError(t, err)
	book.SetNowForTest(func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})

	costUSD, prov := book.Estimate("gemini-3.7-flash", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("promotion:flash-intro"), prov)
	assert.InDelta(t, 0.1, *costUSD, 1e-9)

	// After window: falls through to snapshot (not promo rates).
	book.SetNowForTest(func() time.Time { return end })
	costUSD, prov = book.Estimate("gemini-3.7-flash", 1_000_000, 0, 0)
	require.NotNil(t, costUSD)
	assert.Equal(t, Provenance("snapshot:gemini-3.7-flash"), prov)
	assert.InDelta(t, 0.75, *costUSD, 1e-9) // snapshot intro rate
}
