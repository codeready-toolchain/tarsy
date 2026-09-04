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

func applyDeprecatedJobPairings(compose, execSummary **JobPairing, keys deprecatedJobKeys, node *yaml.Node) error {
	composeNested, composeDeprecated, execNested, execDeprecated := jobKeyPresence(node)
	var err error
	*compose, err = migrateJobPairing(*compose, composeNested, keys.ComposeProvider, keys.ComposeBackend, keys.ComposeFallbackList, composeDeprecated, "compose")
	if err != nil {
		return err
	}
	*execSummary, err = migrateJobPairing(*execSummary, execNested, keys.ExecutiveSummaryProvider, keys.ExecutiveSummaryBackend, keys.ExecutiveSummaryFallbackList, execDeprecated, "executive_summary")
	return err
}

// jobKeyPresence reports whether nested and deprecated compose / executive_summary
// keys appear in the YAML mapping, including empty mappings and null values.
func jobKeyPresence(node *yaml.Node) (compose, composeDeprecated, execSummary, execDeprecated bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for j := 0; j < len(node.Content)-1; j += 2 {
		switch node.Content[j].Value {
		case "compose":
			compose = true
		case "compose_provider", "compose_backend", "compose_fallback_list":
			composeDeprecated = true
		case "executive_summary":
			execSummary = true
		case "executive_summary_provider", "executive_summary_backend", "executive_summary_fallback_list":
			execDeprecated = true
		}
	}
	return
}

func migrateJobPairing(nested *JobPairing, nestedPresent bool, provider string, backend LLMBackend, list string, deprecatedPresent bool, job string) (*JobPairing, error) {
	if nestedPresent && deprecatedPresent {
		return nil, fmt.Errorf("cannot set both %s and %s_provider / %s_backend / %s_fallback_list", job, job, job, job)
	}
	if nested != nil {
		return nested, nil
	}
	if !deprecatedPresent || (provider == "" && backend == "" && list == "") {
		return nil, nil
	}
	return &JobPairing{
		LLMProvider:    provider,
		LLMBackend:     backend,
		FallbackList:   list,
		fromDeprecated: true,
	}, nil
}
