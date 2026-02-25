package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSubAgentRegistry(t *testing.T) {
	agents := map[string]*AgentConfig{
		"LogAnalyzer": {
			Description: "Analyzes logs",
			MCPServers:  []string{"loki"},
		},
		"MetricChecker": {
			Description: "Checks metrics",
			MCPServers:  []string{"prometheus"},
		},
		"WebResearcher": {
			Description: "Web research",
			NativeTools: map[GoogleNativeTool]bool{
				GoogleNativeToolGoogleSearch: true,
				GoogleNativeToolURLContext:   true,
			},
		},
		"NoDescAgent": {
			MCPServers: []string{"some-server"},
		},
		"MyOrchestrator": {
			Type:        AgentTypeOrchestrator,
			Description: "An orchestrator",
		},
	}

	registry := BuildSubAgentRegistry(agents)
	entries := registry.Entries()

	// Should include agents with Description, excluding orchestrators and no-description
	require.Len(t, entries, 3)

	// Sorted by name
	assert.Equal(t, "LogAnalyzer", entries[0].Name)
	assert.Equal(t, "Analyzes logs", entries[0].Description)
	assert.Equal(t, []string{"loki"}, entries[0].MCPServers)

	assert.Equal(t, "MetricChecker", entries[1].Name)
	assert.Equal(t, []string{"prometheus"}, entries[1].MCPServers)

	assert.Equal(t, "WebResearcher", entries[2].Name)
	assert.Equal(t, []string{"google_search", "url_context"}, entries[2].NativeTools)
}

func TestBuildSubAgentRegistry_DisabledNativeToolsExcluded(t *testing.T) {
	agents := map[string]*AgentConfig{
		"CodeExec": {
			Description: "Code execution",
			NativeTools: map[GoogleNativeTool]bool{
				GoogleNativeToolCodeExecution: true,
				GoogleNativeToolGoogleSearch:  false,
			},
		},
	}

	registry := BuildSubAgentRegistry(agents)
	entries := registry.Entries()

	require.Len(t, entries, 1)
	assert.Equal(t, []string{"code_execution"}, entries[0].NativeTools)
}

func TestBuildSubAgentRegistry_Empty(t *testing.T) {
	registry := BuildSubAgentRegistry(map[string]*AgentConfig{})
	assert.Empty(t, registry.Entries())
}

func TestBuildSubAgentRegistry_NilEntry(t *testing.T) {
	agents := map[string]*AgentConfig{
		"Valid":  {Description: "A valid agent"},
		"NilPtr": nil,
	}
	registry := BuildSubAgentRegistry(agents)
	entries := registry.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "Valid", entries[0].Name)
}

func TestSubAgentRegistry_DefensiveCopies(t *testing.T) {
	source := map[string]*AgentConfig{
		"Agent": {
			Description: "Agent",
			MCPServers:  []string{"server-a", "server-b"},
		},
	}
	registry := BuildSubAgentRegistry(source)

	t.Run("MCPServers is a copy of the source", func(t *testing.T) {
		entries := registry.Entries()
		require.Len(t, entries, 1)
		entries[0].MCPServers[0] = "mutated"
		assert.Equal(t, []string{"server-a", "server-b"}, source["Agent"].MCPServers)
		assert.Equal(t, "server-a", registry.Entries()[0].MCPServers[0])
	})

	t.Run("Entries returns a copy of the slice", func(t *testing.T) {
		first := registry.Entries()
		first[0] = SubAgentEntry{Name: "Replaced"}
		assert.Equal(t, "Agent", registry.Entries()[0].Name)
	})

	t.Run("Filter nil returns independent copy", func(t *testing.T) {
		filtered := registry.Filter(nil)
		filtered.Entries()[0] = SubAgentEntry{Name: "Replaced"}
		assert.Equal(t, "Agent", registry.Entries()[0].Name)
	})

	t.Run("Filter non-nil returns independent copy", func(t *testing.T) {
		filtered := registry.Filter([]string{"Agent"})
		e := filtered.Entries()
		e[0].MCPServers[0] = "mutated"
		assert.Equal(t, "server-a", registry.Entries()[0].MCPServers[0])
	})
}

func TestBuildSubAgentRegistry_WithBuiltinAgents(t *testing.T) {
	builtin := GetBuiltinConfig()
	merged := mergeAgents(builtin.Agents, map[string]AgentConfig{})
	registry := BuildSubAgentRegistry(merged)

	entries := registry.Entries()
	require.NotEmpty(t, entries, "built-in agents should produce non-empty registry")

	entryNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		entryNames[e.Name] = true
		assert.NotEmpty(t, e.Description, "entry %s should have a description", e.Name)
	}

	assert.True(t, entryNames["WebResearcher"], "WebResearcher should be in registry")
	assert.True(t, entryNames["CodeExecutor"], "CodeExecutor should be in registry")
	assert.True(t, entryNames["GeneralWorker"], "GeneralWorker should be in registry")

	// Verify native tools survived the merge→registry pipeline
	for _, e := range entries {
		if e.Name == "WebResearcher" {
			assert.Contains(t, e.NativeTools, "google_search")
			assert.Contains(t, e.NativeTools, "url_context")
		}
		if e.Name == "CodeExecutor" {
			assert.Contains(t, e.NativeTools, "code_execution")
		}
	}

	// Orchestrator agents (if any are built-in) must be excluded
	for _, e := range entries {
		agent := merged[e.Name]
		assert.NotEqual(t, AgentTypeOrchestrator, agent.Type, "orchestrator %s should not be in registry", e.Name)
	}
}

func TestSubAgentRegistry_Filter(t *testing.T) {
	agents := map[string]*AgentConfig{
		"A": {Description: "Agent A"},
		"B": {Description: "Agent B"},
		"C": {Description: "Agent C"},
	}
	registry := BuildSubAgentRegistry(agents)

	t.Run("nil returns copy of full registry", func(t *testing.T) {
		filtered := registry.Filter(nil)
		assert.NotSame(t, registry, filtered)
		assert.Equal(t, registry.Entries(), filtered.Entries())
	})

	t.Run("filter to subset", func(t *testing.T) {
		filtered := registry.Filter([]string{"A", "C"})
		entries := filtered.Entries()
		require.Len(t, entries, 2)
		assert.Equal(t, "A", entries[0].Name)
		assert.Equal(t, "C", entries[1].Name)
	})

	t.Run("filter with unknown names ignores them", func(t *testing.T) {
		filtered := registry.Filter([]string{"A", "Unknown"})
		entries := filtered.Entries()
		require.Len(t, entries, 1)
		assert.Equal(t, "A", entries[0].Name)
	})

	t.Run("filter with empty list returns empty", func(t *testing.T) {
		filtered := registry.Filter([]string{})
		assert.Empty(t, filtered.Entries())
	})
}
