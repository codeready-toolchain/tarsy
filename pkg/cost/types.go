// Package cost provides LLM usage cost estimation from a price book
// (YAML promotions, overrides, remote LiteLLM catalog, bundled snapshot).
package cost

import "time"

// Provenance identifies how rates were resolved for an estimate.
type Provenance string

// ProvenancePromotion, ProvenanceOverride, ProvenanceCatalog, ProvenanceSnapshot,
// and ProvenanceUnpriced identify how rates were resolved for an estimate.
const (
	ProvenancePromotion Provenance = "promotion"
	ProvenanceOverride  Provenance = "override"
	ProvenanceCatalog   Provenance = "catalog"
	ProvenanceSnapshot  Provenance = "snapshot"
	ProvenanceUnpriced  Provenance = "unpriced"
)

// PromotionLifecycle is the wall-clock status of a configured promotion.
type PromotionLifecycle string

const (
	// PromotionActive means the promotion window includes now (half-open [start, end)).
	PromotionActive PromotionLifecycle = "active"
	// PromotionUpcoming means start is set and now is before start.
	PromotionUpcoming PromotionLifecycle = "upcoming"
	// PromotionExpired means now is at or after end.
	PromotionExpired PromotionLifecycle = "expired"
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

// Promotion is a time-bounded flat rate for one exact model_name.
// Start nil means already active (-∞). Window is half-open [start, end).
type Promotion struct {
	ID               string
	Model            string
	Start            *time.Time // nil = already active
	End              time.Time
	InputPerMillion  float64
	OutputPerMillion float64
}

// Config is the resolved cost-estimation configuration used to construct a Book.
type Config struct {
	Enabled    bool
	ModelRates map[string]ModelRateOverride
	Promotions []Promotion
}

// Status is runtime metadata for Config Viewer / debugging.
type Status struct {
	Enabled    bool                `json:"enabled"`
	ModelRates map[string]RateView `json:"model_rates,omitempty"`
	Promotions []PromotionStatus   `json:"promotions,omitempty"`
	Catalog    CatalogStatus       `json:"catalog"`
}

// RateView is a read-only view of a YAML override (per-million USD).
type RateView struct {
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
}

// PromotionStatus is a read-only view of a configured promotion with lifecycle.
type PromotionStatus struct {
	ID               string             `json:"id,omitempty"`
	Model            string             `json:"model"`
	InputPerMillion  float64            `json:"input_per_million"`
	OutputPerMillion float64            `json:"output_per_million"`
	Start            *time.Time         `json:"start,omitempty"`
	End              time.Time          `json:"end"`
	Status           PromotionLifecycle `json:"status"`
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
	Reasoning *float64 // nil → Estimate uses Output for thinking tokens
}

// resolved holds rates plus provenance for a successful match.
type resolved struct {
	rates      Rates
	provenance Provenance
	matchKey   string // catalog/snapshot key when applicable
}
