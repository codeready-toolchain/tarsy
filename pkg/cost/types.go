// Package cost provides LLM usage cost estimation from a price book
// (YAML overrides, remote LiteLLM catalog, bundled snapshot).
package cost

import "time"

// Provenance identifies how rates were resolved for an estimate.
type Provenance string

const (
	ProvenanceOverride Provenance = "override"
	ProvenanceCatalog  Provenance = "catalog"
	ProvenanceSnapshot Provenance = "snapshot"
	ProvenanceUnpriced Provenance = "unpriced"
)

// CatalogURL is the LiteLLM public model price catalog.
const CatalogURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const (
	defaultCatalogTTL   = 24 * time.Hour
	defaultFetchTimeout = 30 * time.Second
	defaultMaxBodyBytes = 20 << 20 // 20 MiB
)

// ModelRateOverride is a flat per-million USD override for a model.
type ModelRateOverride struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// Config is the resolved cost-estimation configuration used to construct a Book.
type Config struct {
	Enabled    bool
	ModelRates map[string]ModelRateOverride
}

// Status is runtime metadata for Config Viewer / debugging.
type Status struct {
	Enabled    bool                `json:"enabled"`
	ModelRates map[string]RateView `json:"model_rates,omitempty"`
	Catalog    CatalogStatus       `json:"catalog"`
}

// RateView is a read-only view of a YAML override (per-million USD).
type RateView struct {
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
}

// CatalogStatus describes the in-memory remote catalog (or snapshot fallback).
type CatalogStatus struct {
	Source     string     `json:"source"` // "catalog", "snapshot", or "none"
	EntryCount int        `json:"entry_count"`
	LastFetch  *time.Time `json:"last_fetch,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

// Rates are per-token USD rates used for estimation.
type Rates struct {
	Input     float64
	Output    float64
	Reasoning float64 // per thinking/reasoning token; falls back to Output when unset
}

// resolved holds rates plus provenance for a successful match.
type resolved struct {
	rates      Rates
	provenance Provenance
	matchKey   string // catalog/snapshot key when applicable
}
