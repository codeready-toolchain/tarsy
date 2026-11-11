package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPostInvestigationTestConfig returns a config with one valid chain that has
// scoring enabled and a single post_investigation entry. mutate adjusts it per case.
func newPostInvestigationTestConfig(t *testing.T, mutate func(cfg *Config, chain *ChainConfig)) *Config {
	t.Helper()
	labelMaps, _ := injectBuiltinLabelMap(map[string]LabelMap{
		"custom": {Labels: []LabelSpec{{Label: "Escalate", Description: "needs escalation"}}},
	})
	chain := &ChainConfig{
		AlertTypes: []string{"test"},
		Stages: []StageConfig{
			{Name: "investigation", Agents: []StageAgentConfig{{Name: "Investigator"}}},
		},
		Scoring:           &ScoringConfig{Enabled: true},
		PostInvestigation: []PostInvestigationConfig{{Name: "tuning", Agent: "Proposer"}},
	}
	cfg := &Config{
		Defaults:  &Defaults{},
		LabelMaps: labelMaps,
		FallbackLists: map[string][]FallbackProviderEntry{
			"mid": {{Provider: "fast"}},
		},
		AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
			"Investigator": {},
			"Proposer":     {Type: AgentTypePostInvestigation, Description: "Proposes changes"},
			"Untyped":      {Description: "No type set"},
			"Remediator":   {Type: AgentTypeAction, Description: "Remediates"},
			"Helper":       {Description: "Helps"},
		}),
		MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{
			"tickets": {},
		}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
			"fast": {Type: LLMProviderTypeOpenAI, Model: "test-model"},
		}),
	}
	if mutate != nil {
		mutate(cfg, chain)
	}
	cfg.ChainRegistry = NewChainRegistry(map[string]*ChainConfig{"test-chain": chain})
	return cfg
}

func TestValidatePostInvestigation(t *testing.T) {
	entry := func(chain *ChainConfig) *PostInvestigationConfig { return &chain.PostInvestigation[0] }

	tests := []struct {
		name    string
		mutate  func(cfg *Config, chain *ChainConfig)
		wantErr string
	}{
		{
			name: "minimal entry",
		},
		{
			name: "fully populated entry",
			mutate: func(_ *Config, chain *ChainConfig) {
				*entry(chain) = PostInvestigationConfig{
					Name:                 "tuning",
					Agent:                "Proposer",
					MinScore:             new(60),
					LabelsAny:            []string{"action", "noise"},
					LabelsNone:           []string{"watch"},
					IncludeInvestigation: true,
					Timeout:              5 * time.Minute,
					LLMProvider:          "fast",
					LLMBackend:           LLMBackendLangChain,
					FallbackList:         "mid",
					MaxIterations:        new(10),
					MCPServers:           []string{"tickets"},
					SubAgents:            SubAgentRefs{{Name: "Helper"}},
				}
			},
		},
		{
			name:   "agent without type",
			mutate: func(_ *Config, chain *ChainConfig) { entry(chain).Agent = "Untyped" },
		},
		{
			name:   "min_score bounds are inclusive",
			mutate: func(_ *Config, chain *ChainConfig) { entry(chain).MinScore = new(100) },
		},
		{
			name:   "labels match case-insensitively",
			mutate: func(_ *Config, chain *ChainConfig) { entry(chain).LabelsAny = []string{"Action"} },
		},
		{
			name: "labels from the chain label map",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.LabelMap = "custom"
				entry(chain).LabelsAny = []string{"Escalate"}
			},
		},
		{
			name: "labels from the defaults label map",
			mutate: func(cfg *Config, chain *ChainConfig) {
				cfg.Defaults.LabelMap = "custom"
				entry(chain).LabelsNone = []string{"escalate"}
			},
		},
		{
			name:    "scoring absent",
			mutate:  func(_ *Config, chain *ChainConfig) { chain.Scoring = nil },
			wantErr: "requires scoring to be enabled",
		},
		{
			name:    "scoring disabled",
			mutate:  func(_ *Config, chain *ChainConfig) { chain.Scoring = &ScoringConfig{Enabled: false} },
			wantErr: "requires scoring to be enabled",
		},
		{
			name:    "empty name",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).Name = "" },
			wantErr: "post_investigation[0].name",
		},
		{
			name: "duplicate name",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.PostInvestigation = append(chain.PostInvestigation, PostInvestigationConfig{Name: "tuning", Agent: "Untyped"})
			},
			wantErr: "duplicate entry name 'tuning'",
		},
		{
			name:    "missing agent",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).Agent = "" },
			wantErr: "agent required",
		},
		{
			name:    "unknown agent",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).Agent = "Missing" },
			wantErr: "agent 'Missing' not found",
		},
		{
			name:    "agent with another type",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).Agent = "Remediator" },
			wantErr: `has type "action"`,
		},
		{
			name:    "min_score below range",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).MinScore = new(-1) },
			wantErr: "must be between 0 and 100",
		},
		{
			name:    "min_score above range",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).MinScore = new(101) },
			wantErr: "must be between 0 and 100",
		},
		{
			name:    "negative timeout",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).Timeout = -time.Second },
			wantErr: "timeout': must not be negative",
		},
		{
			name:    "max_iterations zero",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).MaxIterations = new(0) },
			wantErr: "max_iterations",
		},
		{
			name:    "invalid backend",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).LLMBackend = "bogus" },
			wantErr: "invalid LLM backend",
		},
		{
			name: "google-native backend with a non-google provider",
			mutate: func(_ *Config, chain *ChainConfig) {
				entry(chain).LLMProvider = "fast"
				entry(chain).LLMBackend = LLMBackendNativeGemini
			},
			wantErr: "requires a google provider",
		},
		{
			name:    "unknown provider",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).LLMProvider = "missing" },
			wantErr: "LLM provider 'missing' not found",
		},
		{
			name:    "unknown MCP server",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).MCPServers = []string{"missing"} },
			wantErr: "MCP server 'missing' not found",
		},
		{
			name:    "sub-agent without description",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).SubAgents = SubAgentRefs{{Name: "Investigator"}} },
			wantErr: "has no description",
		},
		{
			name:    "unknown label in labels_any",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).LabelsAny = []string{"urgent"} },
			wantErr: "label 'urgent' not found",
		},
		{
			name:    "unknown label in labels_none",
			mutate:  func(_ *Config, chain *ChainConfig) { entry(chain).LabelsNone = []string{"urgent"} },
			wantErr: "label 'urgent' not found",
		},
		{
			name: "label from another map",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.LabelMap = "custom"
				entry(chain).LabelsAny = []string{"action"}
			},
			wantErr: "label 'action' not found",
		},
		{
			name: "label in both lists",
			mutate: func(_ *Config, chain *ChainConfig) {
				entry(chain).LabelsAny = []string{"noise"}
				entry(chain).LabelsNone = []string{"Noise"}
			},
			wantErr: "also in labels_any",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newPostInvestigationTestConfig(t, tt.mutate)
			err := NewValidator(cfg).validateChains()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidatePostInvestigationAgentOutsideEntries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(cfg *Config, chain *ChainConfig)
	}{
		{
			name:   "stage agent",
			mutate: func(_ *Config, chain *ChainConfig) { chain.Stages[0].Agents[0].Name = "Proposer" },
		},
		{
			name:   "stage agent type override",
			mutate: func(_ *Config, chain *ChainConfig) { chain.Stages[0].Agents[0].Type = AgentTypePostInvestigation },
		},
		{
			name: "typed agent with a stage type override",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.Stages[0].Agents[0] = StageAgentConfig{Name: "Proposer", Type: AgentTypeAction}
			},
		},
		{
			name:   "synthesis agent",
			mutate: func(_ *Config, chain *ChainConfig) { chain.Stages[0].Synthesis = &SynthesisConfig{Agent: "Proposer"} },
		},
		{
			name:   "chat agent",
			mutate: func(_ *Config, chain *ChainConfig) { chain.Chat = &ChatConfig{Agent: "Proposer"} },
		},
		{
			name:   "chain scoring agent",
			mutate: func(_ *Config, chain *ChainConfig) { chain.Scoring.Agent = "Proposer" },
		},
		{
			name:   "chain sub_agents",
			mutate: func(_ *Config, chain *ChainConfig) { chain.SubAgents = SubAgentRefs{{Name: "Proposer"}} },
		},
		{
			name: "chat sub_agents",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.Chat = &ChatConfig{SubAgents: SubAgentRefs{{Name: "Proposer"}}}
				chain.Chat.Agent = "Investigator"
			},
		},
		{
			name: "post_investigation entry sub_agents",
			mutate: func(_ *Config, chain *ChainConfig) {
				chain.PostInvestigation[0].SubAgents = SubAgentRefs{{Name: "Proposer"}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newPostInvestigationTestConfig(t, tt.mutate)
			err := NewValidator(cfg).validateChains()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "can only be used in post_investigation entries")
		})
	}

	t.Run("defaults scoring agent", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(cfg *Config, _ *ChainConfig) {
			cfg.Defaults.Scoring = &ScoringConfig{Enabled: true, Agent: "Proposer"}
		})
		err := NewValidator(cfg).validateDefaults()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can only be used in post_investigation entries")
	})

	t.Run("untyped agent may be used in a stage and an entry", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(_ *Config, chain *ChainConfig) {
			chain.Stages[0].Agents[0].Name = "Untyped"
			chain.PostInvestigation[0].Agent = "Untyped"
		})
		assert.NoError(t, NewValidator(cfg).validateChains())
	})
}

func TestPostInvestigationReferences(t *testing.T) {
	t.Run("providers and fallback lists are referenced", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(cfg *Config, chain *ChainConfig) {
			cfg.FallbackLists["entry-list"] = []FallbackProviderEntry{{Provider: "entry-catalog"}}
			cfg.FallbackLists["ref-list"] = []FallbackProviderEntry{{Provider: "ref-catalog"}}
			chain.PostInvestigation[0].LLMProvider = "entry-provider"
			chain.PostInvestigation[0].FallbackList = "entry-list"
			chain.PostInvestigation[0].SubAgents = SubAgentRefs{{Name: "Helper", LLMProvider: "ref-provider", FallbackList: "ref-list"}}
		})
		v := NewValidator(cfg)

		lists := v.referencedFallbackListNames()
		assert.True(t, lists["entry-list"])
		assert.True(t, lists["ref-list"])

		providers := v.collectReferencedLLMProviders()
		assert.True(t, providers["entry-provider"])
		assert.True(t, providers["ref-provider"])
		assert.True(t, providers["entry-catalog"])
		assert.True(t, providers["ref-catalog"])
	})

	t.Run("builtin provider pairs reachable from entries are referenced", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(cfg *Config, chain *ChainConfig) {
			cfg.AgentRegistry = NewAgentRegistry(map[string]*AgentConfig{
				"Investigator":     {},
				"PairedProposer":   {LLMProvider: "proposer-pair"},
				"PairedResearcher": {Description: "Researches", LLMProvider: "researcher-pair"},
				"UnusedPaired":     {LLMProvider: "unused-pair"},
			})
			chain.PostInvestigation[0].Agent = "PairedProposer"
			chain.PostInvestigation[0].SubAgents = SubAgentRefs{{Name: "PairedResearcher"}}
		})

		providers := NewValidator(cfg).collectReferencedLLMProviders()
		assert.True(t, providers["proposer-pair"])
		assert.True(t, providers["researcher-pair"])
		assert.False(t, providers["unused-pair"])
	})

	t.Run("unknown entry fallback list", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(_ *Config, chain *ChainConfig) {
			chain.PostInvestigation[0].FallbackList = "missing"
		})
		err := NewValidator(cfg).validateNamedFallbackLists()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "post_investigation[tuning].fallback_list")
	})

	t.Run("unknown entry sub-agent fallback list", func(t *testing.T) {
		cfg := newPostInvestigationTestConfig(t, func(_ *Config, chain *ChainConfig) {
			chain.PostInvestigation[0].SubAgents = SubAgentRefs{{Name: "Helper", FallbackList: "missing"}}
		})
		err := NewValidator(cfg).validateNamedFallbackLists()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "post_investigation[tuning].sub_agents")
	})
}
