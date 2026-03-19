package prompt

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func newTestMCPRegistry(servers map[string]*config.MCPServerConfig) *config.MCPServerRegistry {
	if servers == nil {
		servers = map[string]*config.MCPServerConfig{}
	}
	return config.NewMCPServerRegistry(servers)
}

func newTestExecCtx() *agent.ExecutionContext {
	return &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "Custom test instructions.",
		},
	}
}

func TestComposeInstructions_ThreeTiers(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {
			Instructions: "Always check node status first.",
		},
	})
	builder := NewPromptBuilder(registry)
	execCtx := newTestExecCtx()

	result := builder.ComposeInstructions(execCtx)

	// Tier 1: General instructions
	assert.Contains(t, result, "General SRE Agent Instructions")
	assert.Contains(t, result, "Site Reliability Engineer")

	// Tier 2: MCP server instructions
	assert.Contains(t, result, "kubernetes-server Instructions")
	assert.Contains(t, result, "Always check node status first.")

	// Tier 3: Custom instructions
	assert.Contains(t, result, "Agent-Specific Instructions")
	assert.Contains(t, result, "Custom test instructions.")
}

func TestComposeInstructions_NoMCPInstructions(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {
			Instructions: "", // Empty instructions
		},
	})
	builder := NewPromptBuilder(registry)
	execCtx := newTestExecCtx()

	result := builder.ComposeInstructions(execCtx)
	assert.NotContains(t, result, "kubernetes-server Instructions")
}

func TestComposeInstructions_NoCustomInstructions(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			CustomInstructions: "",
		},
	}

	result := builder.ComposeInstructions(execCtx)
	assert.Contains(t, result, "General SRE Agent Instructions")
	assert.NotContains(t, result, "Agent-Specific Instructions")
}

func TestComposeInstructions_MissingMCPServer(t *testing.T) {
	// Server referenced but not in registry — should be silently skipped
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := newTestExecCtx()

	result := builder.ComposeInstructions(execCtx)
	// Should still work, just no MCP instructions
	assert.Contains(t, result, "General SRE Agent Instructions")
	assert.NotContains(t, result, "kubernetes-server Instructions")
}

func TestComposeChatInstructions_UsesChatGeneralInstructions(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {
			Instructions: "K8s instructions.",
		},
	})
	builder := NewPromptBuilder(registry)
	execCtx := newTestExecCtx()

	result := builder.ComposeChatInstructions(execCtx)

	// Tier 1: Chat-specific (not investigation)
	assert.Contains(t, result, "Chat Assistant Instructions")
	assert.NotContains(t, result, "General SRE Agent Instructions")

	// Tier 2: MCP instructions still included
	assert.Contains(t, result, "kubernetes-server Instructions")

	// Tier 3: Custom instructions still included
	assert.Contains(t, result, "Agent-Specific Instructions")

	// Chat-specific guidelines appended
	assert.Contains(t, result, "Response Guidelines")
	assert.Contains(t, result, "Context Awareness")
}

func TestComposeInstructions_FailedServers(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{},
		FailedServers: map[string]string{
			"kubernetes-server": "connection refused",
			"github-server":     "timeout after 30s",
		},
	}

	result := builder.ComposeInstructions(execCtx)

	assert.Contains(t, result, "Unavailable MCP Servers")
	assert.Contains(t, result, "kubernetes-server")
	assert.Contains(t, result, "connection refused")
	assert.Contains(t, result, "github-server")
	assert.Contains(t, result, "timeout after 30s")
	assert.Contains(t, result, "Do not attempt to use tools from these servers")
}

func TestComposeChatInstructions_FailedServers(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{},
		FailedServers: map[string]string{
			"broken-server": "EOF",
		},
	}

	result := builder.ComposeChatInstructions(execCtx)

	assert.Contains(t, result, "Unavailable MCP Servers")
	assert.Contains(t, result, "broken-server")
	assert.Contains(t, result, "EOF")
}

func TestComposeInstructions_NoFailedServers(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{},
	}

	result := builder.ComposeInstructions(execCtx)
	assert.NotContains(t, result, "Unavailable MCP Servers")
}

func TestComposeInstructions_OrderingPreserved(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {Instructions: "MCP_TIER2_MARKER"},
	})
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "CUSTOM_TIER3_MARKER",
		},
		FailedServers: map[string]string{
			"broken-server": "FAILED_SERVER_MARKER",
		},
	}

	result := builder.ComposeInstructions(execCtx)

	// Verify ordering: Tier 1 < Tier 2 < Unavailable warnings < Tier 3
	idxT1 := strings.Index(result, "General SRE Agent Instructions")
	idxT2 := strings.Index(result, "MCP_TIER2_MARKER")
	idxWarn := strings.Index(result, "FAILED_SERVER_MARKER")
	idxT3 := strings.Index(result, "CUSTOM_TIER3_MARKER")
	assert.Greater(t, idxT2, idxT1, "Tier 2 should come after Tier 1")
	assert.Greater(t, idxWarn, idxT2, "Unavailable warnings should come after Tier 2")
	assert.Greater(t, idxT3, idxWarn, "Tier 3 should come after unavailable warnings")
}

func TestComposeInstructions_SkillTierOrdering(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {Instructions: "MCP_TIER2_MARKER"},
	})
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "CUSTOM_TIER3_MARKER",
			RequiredSkillContent: []agent.ResolvedSkill{
				{Name: "k8s-basics", Body: "REQUIRED_SKILL_MARKER"},
			},
			OnDemandSkills: []agent.SkillCatalogEntry{
				{Name: "networking", Description: "ONDEMAND_SKILL_MARKER"},
			},
		},
		FailedServers: map[string]string{
			"broken-server": "FAILED_SERVER_MARKER",
		},
	}

	result := builder.ComposeInstructions(execCtx)

	// Tier 1 < Tier 2 < Warnings < Tier 2.5 < Tier 2.6 < Tier 3
	idxT1 := strings.Index(result, "General SRE Agent Instructions")
	idxT2 := strings.Index(result, "MCP_TIER2_MARKER")
	idxWarn := strings.Index(result, "FAILED_SERVER_MARKER")
	idxT25 := strings.Index(result, "REQUIRED_SKILL_MARKER")
	idxT26 := strings.Index(result, "ONDEMAND_SKILL_MARKER")
	idxT3 := strings.Index(result, "CUSTOM_TIER3_MARKER")

	assert.Greater(t, idxT2, idxT1, "Tier 2 should come after Tier 1")
	assert.Greater(t, idxWarn, idxT2, "Warnings should come after Tier 2")
	assert.Greater(t, idxT25, idxWarn, "Tier 2.5 (required skills) should come after warnings")
	assert.Greater(t, idxT26, idxT25, "Tier 2.6 (on-demand catalog) should come after Tier 2.5")
	assert.Greater(t, idxT3, idxT26, "Tier 3 should come after Tier 2.6")

	assert.Contains(t, result, "## Pre-loaded Skills")
	assert.Contains(t, result, "### k8s-basics")
	assert.Contains(t, result, "## Available Skills")
}

func TestComposeChatInstructions_SkillTierOrdering(t *testing.T) {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {Instructions: "MCP_TIER2_MARKER"},
	})
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "CUSTOM_TIER3_MARKER",
			RequiredSkillContent: []agent.ResolvedSkill{
				{Name: "k8s-basics", Body: "REQUIRED_SKILL_MARKER"},
			},
			OnDemandSkills: []agent.SkillCatalogEntry{
				{Name: "networking", Description: "ONDEMAND_SKILL_MARKER"},
			},
		},
	}

	result := builder.ComposeChatInstructions(execCtx)

	// Tier 1 < Tier 2 < Tier 2.5 < Tier 2.6 < Tier 3 < Chat guidelines
	idxT1 := strings.Index(result, "Chat Assistant Instructions")
	idxT2 := strings.Index(result, "MCP_TIER2_MARKER")
	idxT25 := strings.Index(result, "REQUIRED_SKILL_MARKER")
	idxT26 := strings.Index(result, "ONDEMAND_SKILL_MARKER")
	idxT3 := strings.Index(result, "CUSTOM_TIER3_MARKER")
	idxChat := strings.Index(result, "Response Guidelines")

	assert.Greater(t, idxT2, idxT1, "Tier 2 should come after Tier 1")
	assert.Greater(t, idxT25, idxT2, "Tier 2.5 should come after Tier 2")
	assert.Greater(t, idxT26, idxT25, "Tier 2.6 should come after Tier 2.5")
	assert.Greater(t, idxT3, idxT26, "Tier 3 should come after Tier 2.6")
	assert.Greater(t, idxChat, idxT3, "Chat guidelines should come after Tier 3")
}

func TestComposeInstructions_NoSkills(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{},
	}

	result := builder.ComposeInstructions(execCtx)

	assert.NotContains(t, result, "Pre-loaded Skills")
	assert.NotContains(t, result, "Available Skills")
}

func TestComposeChatInstructions_NoSkills(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{},
	}

	result := builder.ComposeChatInstructions(execCtx)

	assert.NotContains(t, result, "Pre-loaded Skills")
	assert.NotContains(t, result, "Available Skills")
}

func TestComposeInstructions_OnlyRequiredSkills(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			RequiredSkillContent: []agent.ResolvedSkill{
				{Name: "k8s-basics", Body: "Pod troubleshooting guide."},
			},
		},
	}

	result := builder.ComposeInstructions(execCtx)

	assert.Contains(t, result, "## Pre-loaded Skills")
	assert.Contains(t, result, "### k8s-basics")
	assert.Contains(t, result, "Pod troubleshooting guide.")
	assert.NotContains(t, result, "Available Skills")
}

func TestComposeInstructions_MultipleRequiredSkills(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			RequiredSkillContent: []agent.ResolvedSkill{
				{Name: "k8s-basics", Body: "Pod troubleshooting guide."},
				{Name: "networking", Body: "DNS resolution steps."},
			},
		},
	}

	result := builder.ComposeInstructions(execCtx)

	assert.Contains(t, result, "## Pre-loaded Skills")
	assert.Contains(t, result, "### k8s-basics")
	assert.Contains(t, result, "Pod troubleshooting guide.")
	assert.Contains(t, result, "### networking")
	assert.Contains(t, result, "DNS resolution steps.")

	idxFirst := strings.Index(result, "### k8s-basics")
	idxSecond := strings.Index(result, "### networking")
	assert.Less(t, idxFirst, idxSecond, "skills should appear in order")
}

func TestComposeInstructions_OnlyOnDemandSkills(t *testing.T) {
	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry)
	execCtx := &agent.ExecutionContext{
		Config: &agent.ResolvedAgentConfig{
			OnDemandSkills: []agent.SkillCatalogEntry{
				{Name: "networking", Description: "Network debugging patterns"},
			},
		},
	}

	result := builder.ComposeInstructions(execCtx)

	assert.NotContains(t, result, "Pre-loaded Skills")
	assert.Contains(t, result, "## Available Skills")
	assert.Contains(t, result, "- **networking**: Network debugging patterns")
}
