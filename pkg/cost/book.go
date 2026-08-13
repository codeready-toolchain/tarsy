package cost

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Book is the process-local price book: active promotions > YAML overrides > remote catalog > snapshot.
type Book struct {
	mu sync.RWMutex

	enabled     bool
	overrides   map[string]ModelRateOverride
	promotions  []Promotion
	catalog     map[string]catalogEntry
	snapshot    map[string]catalogEntry
	lastFetch   time.Time
	lastError   string
	usingRemote bool

	httpClient *http.Client
	catalogURL string
	ttl        time.Duration
	maxBody    int64
	now        func() time.Time

	stopRefresh context.CancelFunc
}

// NewBook constructs a Book from config and the bundled snapshot.
// Pass a nil or zero Config for defaults (enabled=true, no overrides).
func NewBook(cfg *Config) (*Book, error) {
	snapshot, err := loadSnapshot()
	if err != nil {
		return nil, err
	}

	enabled := true
	overrides := map[string]ModelRateOverride{}
	var promotions []Promotion
	if cfg != nil {
		enabled = cfg.Enabled
		if cfg.ModelRates != nil {
			overrides = cfg.ModelRates
		}
		if cfg.Promotions != nil {
			promotions = cfg.Promotions
		}
	}

	return &Book{
		enabled:    enabled,
		overrides:  overrides,
		promotions: promotions,
		snapshot:   snapshot,
		httpClient: &http.Client{Timeout: defaultFetchTimeout},
		catalogURL: CatalogURL,
		ttl:        defaultCatalogTTL,
		maxBody:    defaultMaxBodyBytes,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetNowForTest replaces the wall clock used for promotion windows (tests only).
func (b *Book) SetNowForTest(now func() time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now == nil {
		b.now = func() time.Time { return time.Now().UTC() }
		return
	}
	b.now = now
}

// Enabled reports whether cost estimation is on.
func (b *Book) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enabled
}

// Estimate resolves rates for modelName and returns estimated USD cost.
// Returns (nil, ProvenanceUnpriced) when estimation is disabled or the model is unpriced.
func (b *Book) Estimate(modelName string, inputTokens, outputTokens, thinkingTokens int) (*float64, Provenance) {
	if b == nil {
		return nil, ProvenanceUnpriced
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.enabled {
		return nil, ProvenanceUnpriced
	}

	res, ok := b.resolveLocked(modelName, inputTokens, b.now())
	if !ok {
		return nil, ProvenanceUnpriced
	}
	cost := Estimate(res.rates, inputTokens, outputTokens, thinkingTokens)
	return &cost, res.provenance
}

// Status returns a snapshot of config + catalog metadata for Config Viewer.
func (b *Book) Status() Status {
	if b == nil {
		return Status{Enabled: false, Catalog: CatalogStatus{Source: "none"}}
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	rates := make(map[string]RateView, len(b.overrides))
	for k, v := range b.overrides {
		rates[k] = RateView(v)
	}

	now := b.now()
	promos := make([]PromotionStatus, len(b.promotions))
	for i, p := range b.promotions {
		promos[i] = PromotionStatus{
			ID:               p.ID,
			Model:            p.Model,
			InputPerMillion:  p.InputPerMillion,
			OutputPerMillion: p.OutputPerMillion,
			Start:            p.Start,
			End:              p.End,
			Status:           promotionLifecycle(p, now),
		}
	}

	src := "snapshot"
	count := len(b.snapshot)
	if b.usingRemote && len(b.catalog) > 0 {
		src = "catalog"
		count = len(b.catalog)
	}

	st := Status{
		Enabled:    b.enabled,
		ModelRates: rates,
		Promotions: promos,
		Catalog: CatalogStatus{
			Source:     src,
			EntryCount: count,
			LastError:  b.lastError,
		},
	}
	if !b.lastFetch.IsZero() {
		t := b.lastFetch
		st.Catalog.LastFetch = &t
	}
	return st
}

// Start kicks an async catalog fetch and periodic TTL refresh until ctx is cancelled.
func (b *Book) Start(ctx context.Context) {
	if b == nil {
		return
	}
	refreshCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.stopRefresh = cancel
	b.mu.Unlock()

	go b.refreshLoop(refreshCtx)
}

// Stop cancels the background refresh loop.
func (b *Book) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	cancel := b.stopRefresh
	b.stopRefresh = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// OverrideHTTPClientForTest replaces the HTTP client (tests only).
func (b *Book) OverrideHTTPClientForTest(client *http.Client) {
	b.mu.Lock()
	b.httpClient = client
	b.mu.Unlock()
}

// SetCatalogURLForTest overrides the catalog URL (tests only).
func (b *Book) SetCatalogURLForTest(url string) {
	b.mu.Lock()
	b.catalogURL = url
	b.mu.Unlock()
}

func (b *Book) refreshLoop(ctx context.Context) {
	b.refreshOnce(ctx)

	ticker := time.NewTicker(b.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.refreshOnce(ctx)
		}
	}
}

func (b *Book) refreshOnce(ctx context.Context) {
	b.mu.RLock()
	client := b.httpClient
	url := b.catalogURL
	maxBody := b.maxBody
	b.mu.RUnlock()

	fetchCtx, cancel := context.WithTimeout(ctx, defaultFetchTimeout)
	defer cancel()

	entries, err := fetchCatalog(fetchCtx, client, url, maxBody)

	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastFetch = time.Now()
	if err != nil {
		b.lastError = err.Error()
		slog.Warn("Failed to refresh LiteLLM price catalog; using snapshot/overrides",
			"error", err)
		return
	}
	b.catalog = entries
	b.usingRemote = true
	b.lastError = ""
}

// resolveLocked requires b.mu held for reading.
func (b *Book) resolveLocked(modelName string, inputTokens int, now time.Time) (resolved, bool) {
	// 1. Active promotion (exact model_name, wall-clock window).
	if p, ok := findActivePromotion(b.promotions, modelName, now); ok {
		label := p.Model
		if p.ID != "" {
			label = p.ID
		}
		return resolved{
			rates: overrideRates(ModelRateOverride{
				InputPerMillion:  p.InputPerMillion,
				OutputPerMillion: p.OutputPerMillion,
			}),
			provenance: Provenance(string(ProvenancePromotion) + ":" + label),
			matchKey:   modelName,
		}, true
	}

	// 2. YAML overrides (exact model_name).
	if o, ok := b.overrides[modelName]; ok {
		return resolved{
			rates:      overrideRates(o),
			provenance: ProvenanceOverride,
			matchKey:   modelName,
		}, true
	}

	// 3. Remote catalog.
	if e, key, ok := findInCatalog(b.catalog, modelName); ok {
		if rates, ok := e.ratesForInput(inputTokens); ok {
			return resolved{
				rates:      rates,
				provenance: Provenance(string(ProvenanceCatalog) + ":" + key),
				matchKey:   key,
			}, true
		}
	}

	// 4. Bundled snapshot.
	if e, key, ok := findInCatalog(b.snapshot, modelName); ok {
		if rates, ok := e.ratesForInput(inputTokens); ok {
			return resolved{
				rates:      rates,
				provenance: Provenance(string(ProvenanceSnapshot) + ":" + key),
				matchKey:   key,
			}, true
		}
	}

	return resolved{provenance: ProvenanceUnpriced}, false
}

func findActivePromotion(promos []Promotion, modelName string, now time.Time) (Promotion, bool) {
	for _, p := range promos {
		if p.Model != modelName {
			continue
		}
		if promotionActive(p, now) {
			return p, true
		}
	}
	return Promotion{}, false
}

func promotionActive(p Promotion, now time.Time) bool {
	if !now.Before(p.End) {
		return false
	}
	if p.Start != nil && now.Before(*p.Start) {
		return false
	}
	return true
}

func promotionLifecycle(p Promotion, now time.Time) PromotionLifecycle {
	if promotionActive(p, now) {
		return PromotionActive
	}
	if p.Start != nil && now.Before(*p.Start) {
		return PromotionUpcoming
	}
	return PromotionExpired
}
