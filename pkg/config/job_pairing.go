package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// JobPairing is provider/backend/list for compose and executive-summary jobs.
// Same keys as defaults.agents / scoring / summarization pairing sites.
type JobPairing struct {
	LLMProvider  string     `yaml:"llm_provider,omitempty"`
	LLMBackend   LLMBackend `yaml:"llm_backend,omitempty"`
	FallbackList string     `yaml:"fallback_list,omitempty"`

	// fromDeprecated is true when this pairing was loaded from the old
	// compose_* / executive_summary_* keys.
	fromDeprecated bool
}

// Provider returns llm_provider, or "" if p is nil.
func (p *JobPairing) Provider() string {
	if p == nil {
		return ""
	}
	return p.LLMProvider
}

// Backend returns llm_backend, or "" if p is nil.
func (p *JobPairing) Backend() LLMBackend {
	if p == nil {
		return ""
	}
	return p.LLMBackend
}

// List returns fallback_list, or "" if p is nil.
func (p *JobPairing) List() string {
	if p == nil {
		return ""
	}
	return p.FallbackList
}

func (p *JobPairing) deprecated() bool {
	return p != nil && p.fromDeprecated
}

// UnmarshalYAML rejects unknown keys (e.g. catalog `backend` instead of `llm_backend`).
func (p *JobPairing) UnmarshalYAML(value *yaml.Node) error {
	type raw JobPairing
	return decodeMapping(value, llmPairingAllowedKeys, pairingKeyHints, (*raw)(p))
}

// deprecatedJobKeys are the pre-nesting compose / executive_summary YAML fields.
type deprecatedJobKeys struct {
	ComposeProvider              string     `yaml:"compose_provider,omitempty"`
	ComposeBackend               LLMBackend `yaml:"compose_backend,omitempty"`
	ComposeFallbackList          string     `yaml:"compose_fallback_list,omitempty"`
	ExecutiveSummaryProvider     string     `yaml:"executive_summary_provider,omitempty"`
	ExecutiveSummaryBackend      LLMBackend `yaml:"executive_summary_backend,omitempty"`
	ExecutiveSummaryFallbackList string     `yaml:"executive_summary_fallback_list,omitempty"`
}

func applyDeprecatedJobPairings(compose, execSummary **JobPairing, keys deprecatedJobKeys) error {
	var err error
	*compose, err = migrateJobPairing(*compose, keys.ComposeProvider, keys.ComposeBackend, keys.ComposeFallbackList, "compose")
	if err != nil {
		return err
	}
	*execSummary, err = migrateJobPairing(*execSummary, keys.ExecutiveSummaryProvider, keys.ExecutiveSummaryBackend, keys.ExecutiveSummaryFallbackList, "executive_summary")
	return err
}

func migrateJobPairing(nested *JobPairing, provider string, backend LLMBackend, list, job string) (*JobPairing, error) {
	deprecated := provider != "" || backend != "" || list != ""
	if nested != nil && deprecated {
		return nil, fmt.Errorf("cannot set both %s and %s_provider / %s_backend / %s_fallback_list", job, job, job, job)
	}
	if nested != nil {
		return nested, nil
	}
	if !deprecated {
		return nil, nil
	}
	return &JobPairing{
		LLMProvider:    provider,
		LLMBackend:     backend,
		FallbackList:   list,
		fromDeprecated: true,
	}, nil
}
