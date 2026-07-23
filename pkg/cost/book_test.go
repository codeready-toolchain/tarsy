package cost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				book.refreshOnce(t.Context())
				_, _ = book.Estimate("gemini-3.6-flash", 100, 50, 0)
				_, _ = book.Estimate("concurrent-model", 1000, 100, 0)
				_ = book.Status()
			}
		})
	}
	wg.Wait()
}
