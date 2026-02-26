package queue

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/orchestrator"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMCPSelection(t *testing.T) {
	// Build a test registry with known servers
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {},
		"argocd-server":     {},
		"prometheus-server": {},
	})

	t.Run("no override returns chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: nil,
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server", "argocd-server"},
		}

		serverIDs, toolFilter, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server", "argocd-server"}, serverIDs)
		assert.Nil(t, toolFilter)
	})

	t.Run("empty map returns chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		serverIDs, toolFilter, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server"}, serverIDs)
		assert.Nil(t, toolFilter)
	})

	t.Run("override replaces chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "prometheus-server"},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server", "argocd-server"},
		}

		serverIDs, toolFilter, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)
		assert.Equal(t, []string{"prometheus-server"}, serverIDs)
		assert.Nil(t, toolFilter) // No tool filter specified
	})

	t.Run("override with tool filter", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{
						"name":  "kubernetes-server",
						"tools": []interface{}{"get_pods", "describe_pod"},
					},
					map[string]interface{}{
						"name": "argocd-server",
					},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"prometheus-server"},
		}

		serverIDs, toolFilter, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server", "argocd-server"}, serverIDs)
		require.NotNil(t, toolFilter)
		assert.Equal(t, []string{"get_pods", "describe_pod"}, toolFilter["kubernetes-server"])
		_, hasArgoFilter := toolFilter["argocd-server"]
		assert.False(t, hasArgoFilter, "argocd-server should not have a filter")
	})

	t.Run("unknown server in override returns error", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "nonexistent-server"},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		_, _, err := resolveMCPSelection(session, resolved, registry)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent-server")
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("override with native tools sets NativeToolsOverride", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
				},
				"native_tools": map[string]interface{}{
					"google_search": false,
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"argocd-server"},
		}

		// We only care about the side-effect on resolved.NativeToolsOverride;
		// also verify the returned serverIDs are from the override.
		serverIDs, _, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server"}, serverIDs)
		require.NotNil(t, resolved.NativeToolsOverride)
		require.NotNil(t, resolved.NativeToolsOverride.GoogleSearch)
		assert.False(t, *resolved.NativeToolsOverride.GoogleSearch)
	})

	t.Run("native tools override is merged into LLMProvider clone", func(t *testing.T) {
		origProvider := &config.LLMProviderConfig{
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch:  true,
				config.GoogleNativeToolCodeExecution: false,
				config.GoogleNativeToolURLContext:    true,
			},
		}
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
				},
				"native_tools": map[string]interface{}{
					"google_search":  false,
					"code_execution": true,
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers:  []string{"argocd-server"},
			LLMProvider: origProvider,
		}

		_, _, err := resolveMCPSelection(session, resolved, registry)
		require.NoError(t, err)

		// LLMProvider should be a different pointer (cloned, not shared).
		assert.NotSame(t, origProvider, resolved.LLMProvider)

		// The clone should have merged native tools.
		assert.False(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution])
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolURLContext]) // unchanged

		// Original provider should NOT be mutated.
		assert.True(t, origProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
		assert.False(t, origProvider.NativeTools[config.GoogleNativeToolCodeExecution])
	})

	t.Run("empty servers in override returns error", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		_, _, err := resolveMCPSelection(session, resolved, registry)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one server")
	})
}

func TestExtractFinalAnalysis(t *testing.T) {
	tests := []struct {
		name   string
		stages []stageResult
		want   string
	}{
		{
			name:   "empty stages returns empty",
			stages: nil,
			want:   "",
		},
		{
			name: "single stage with analysis",
			stages: []stageResult{
				{finalAnalysis: "Root cause: OOM"},
			},
			want: "Root cause: OOM",
		},
		{
			name: "returns last stage analysis (reverse search)",
			stages: []stageResult{
				{finalAnalysis: "Stage 1 findings"},
				{finalAnalysis: "Stage 2 diagnosis"},
			},
			want: "Stage 2 diagnosis",
		},
		{
			name: "skips empty analysis, returns earlier stage",
			stages: []stageResult{
				{finalAnalysis: "Only this one has analysis"},
				{finalAnalysis: ""},
			},
			want: "Only this one has analysis",
		},
		{
			name: "all empty analysis returns empty",
			stages: []stageResult{
				{finalAnalysis: ""},
				{finalAnalysis: ""},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFinalAnalysis(tt.stages)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMapAgentStatusToSessionStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  agent.ExecutionStatus
		expect alertsession.Status
	}{
		{"completed", agent.ExecutionStatusCompleted, alertsession.StatusCompleted},
		{"failed", agent.ExecutionStatusFailed, alertsession.StatusFailed},
		{"timed_out", agent.ExecutionStatusTimedOut, alertsession.StatusTimedOut},
		{"cancelled", agent.ExecutionStatusCancelled, alertsession.StatusCancelled},
		{"pending defaults to failed", agent.ExecutionStatusPending, alertsession.StatusFailed},
		{"active defaults to failed", agent.ExecutionStatusActive, alertsession.StatusFailed},
		{"unknown defaults to failed", agent.ExecutionStatus("unknown"), alertsession.StatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, mapAgentStatusToSessionStatus(tt.input))
		})
	}
}

func TestMapAgentStatusToEntStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  agent.ExecutionStatus
		expect agentexecution.Status
	}{
		{"completed", agent.ExecutionStatusCompleted, agentexecution.StatusCompleted},
		{"failed", agent.ExecutionStatusFailed, agentexecution.StatusFailed},
		{"timed_out", agent.ExecutionStatusTimedOut, agentexecution.StatusTimedOut},
		{"cancelled", agent.ExecutionStatusCancelled, agentexecution.StatusCancelled},
		{"pending defaults to failed", agent.ExecutionStatusPending, agentexecution.StatusFailed},
		{"active defaults to failed", agent.ExecutionStatusActive, agentexecution.StatusFailed},
		{"unknown defaults to failed", agent.ExecutionStatus("unknown"), agentexecution.StatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, mapAgentStatusToEntStatus(tt.input))
		})
	}
}

func TestMapCancellation(t *testing.T) {
	executor := &RealSessionExecutor{}

	t.Run("active context returns nil", func(t *testing.T) {
		result := executor.mapCancellation(context.Background())
		assert.Nil(t, result)
	})

	t.Run("cancelled context returns cancelled status", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result := executor.mapCancellation(ctx)
		require.NotNil(t, result)
		assert.Equal(t, alertsession.StatusCancelled, result.Status)
		assert.ErrorIs(t, result.Error, context.Canceled)
	})

	t.Run("deadline exceeded returns timed_out status", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		// Wait for deadline to be exceeded
		<-ctx.Done()

		result := executor.mapCancellation(ctx)
		require.NotNil(t, result)
		assert.Equal(t, alertsession.StatusTimedOut, result.Status)
		assert.Contains(t, result.Error.Error(), "timed out")
	})
}

func TestResolveOrchestratorGuardrails(t *testing.T) {
	dur := func(d time.Duration) *time.Duration { return &d }
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name     string
		cfg      *config.Config
		agentDef *config.AgentConfig
		want     *orchestrator.OrchestratorGuardrails
	}{
		{
			name:     "hardcoded fallbacks when no config",
			cfg:      &config.Config{},
			agentDef: &config.AgentConfig{},
			want: &orchestrator.OrchestratorGuardrails{
				MaxConcurrentAgents: 5,
				AgentTimeout:        300 * time.Second,
				MaxBudget:           600 * time.Second,
			},
		},
		{
			name: "global defaults override fallbacks",
			cfg: &config.Config{
				Defaults: &config.Defaults{
					Orchestrator: &config.OrchestratorConfig{
						MaxConcurrentAgents: intPtr(10),
						AgentTimeout:        dur(60 * time.Second),
					},
				},
			},
			agentDef: &config.AgentConfig{},
			want: &orchestrator.OrchestratorGuardrails{
				MaxConcurrentAgents: 10,
				AgentTimeout:        60 * time.Second,
				MaxBudget:           600 * time.Second,
			},
		},
		{
			name: "per-agent overrides global defaults",
			cfg: &config.Config{
				Defaults: &config.Defaults{
					Orchestrator: &config.OrchestratorConfig{
						MaxConcurrentAgents: intPtr(10),
						AgentTimeout:        dur(60 * time.Second),
						MaxBudget:           dur(120 * time.Second),
					},
				},
			},
			agentDef: &config.AgentConfig{
				Orchestrator: &config.OrchestratorConfig{
					MaxConcurrentAgents: intPtr(3),
				},
			},
			want: &orchestrator.OrchestratorGuardrails{
				MaxConcurrentAgents: 3,
				AgentTimeout:        60 * time.Second,
				MaxBudget:           120 * time.Second,
			},
		},
		{
			name: "per-agent only without global defaults",
			cfg:  &config.Config{},
			agentDef: &config.AgentConfig{
				Orchestrator: &config.OrchestratorConfig{
					MaxBudget: dur(30 * time.Second),
				},
			},
			want: &orchestrator.OrchestratorGuardrails{
				MaxConcurrentAgents: 5,
				AgentTimeout:        300 * time.Second,
				MaxBudget:           30 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOrchestratorGuardrails(tt.cfg, tt.agentDef)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveSubAgents(t *testing.T) {
	tests := []struct {
		name     string
		chain    *config.ChainConfig
		stage    config.StageConfig
		agentCfg config.StageAgentConfig
		want     []string
	}{
		{
			name:     "no override at any level",
			chain:    &config.ChainConfig{},
			stage:    config.StageConfig{},
			agentCfg: config.StageAgentConfig{},
			want:     nil,
		},
		{
			name:     "chain level",
			chain:    &config.ChainConfig{SubAgents: []string{"LogAnalyzer", "MetricChecker"}},
			stage:    config.StageConfig{},
			agentCfg: config.StageAgentConfig{},
			want:     []string{"LogAnalyzer", "MetricChecker"},
		},
		{
			name:     "stage overrides chain",
			chain:    &config.ChainConfig{SubAgents: []string{"LogAnalyzer"}},
			stage:    config.StageConfig{SubAgents: []string{"WebResearcher"}},
			agentCfg: config.StageAgentConfig{},
			want:     []string{"WebResearcher"},
		},
		{
			name:     "agent overrides stage and chain",
			chain:    &config.ChainConfig{SubAgents: []string{"LogAnalyzer"}},
			stage:    config.StageConfig{SubAgents: []string{"WebResearcher"}},
			agentCfg: config.StageAgentConfig{SubAgents: []string{"CodeExecutor", "GeneralWorker"}},
			want:     []string{"CodeExecutor", "GeneralWorker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSubAgents(tt.chain, tt.stage, tt.agentCfg)
			assert.Equal(t, tt.want, got)
		})
	}
}
