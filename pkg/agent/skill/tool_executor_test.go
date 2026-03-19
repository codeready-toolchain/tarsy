package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRegistry() *config.SkillRegistry {
	return config.NewSkillRegistry(map[string]*config.SkillConfig{
		"kubernetes-debugging": {
			Name:        "kubernetes-debugging",
			Description: "K8s pod debugging techniques",
			Body:        "# Kubernetes Debugging\n\nCheck pod logs first.",
		},
		"aws-rds": {
			Name:        "aws-rds",
			Description: "AWS RDS troubleshooting",
			Body:        "# AWS RDS\n\nCheck connection limits.",
		},
		"redis-ops": {
			Name:        "redis-ops",
			Description: "Redis operations playbook",
			Body:        "# Redis Ops\n\nMonitor memory usage.",
		},
	})
}

func allSkillNames() map[string]struct{} {
	return map[string]struct{}{
		"kubernetes-debugging": {},
		"aws-rds":              {},
		"redis-ops":            {},
	}
}

func loadSkillArgs(names ...string) string {
	args, _ := json.Marshal(map[string][]string{"names": names})
	return string(args)
}

func TestSkillToolExecutor_ListTools_PrependToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server1.read_file", Description: "Reads a file"},
		{Name: "server1.write_file", Description: "Writes a file"},
	})
	s := NewSkillToolExecutor(inner, testRegistry(), allSkillNames())

	tools, err := s.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 3)
	assert.Equal(t, "load_skill", tools[0].Name)
	assert.Equal(t, "server1.read_file", tools[1].Name)
	assert.Equal(t, "server1.write_file", tools[2].Name)
}

func TestSkillToolExecutor_ListTools_NoInnerTools(t *testing.T) {
	inner := agent.NewStubToolExecutor(nil)
	s := NewSkillToolExecutor(inner, testRegistry(), allSkillNames())

	tools, err := s.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 1)
	assert.Equal(t, "load_skill", tools[0].Name)
}

func TestSkillToolExecutor_ListTools_DeduplicatesLoadSkill(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "load_skill", Description: "duplicate from inner"},
		{Name: "server1.read_file", Description: "Reads a file"},
	})
	s := NewSkillToolExecutor(inner, testRegistry(), allSkillNames())

	tools, err := s.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 2)
	assert.Equal(t, "load_skill", tools[0].Name)
	assert.Equal(t, loadSkillTool.Description, tools[0].Description)
	assert.Equal(t, "server1.read_file", tools[1].Name)
}

func TestSkillToolExecutor_Execute_LoadSkill(t *testing.T) {
	tests := []struct {
		name         string
		allowed      map[string]struct{}
		args         string
		wantError    bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:    "single valid skill",
			allowed: allSkillNames(),
			args:    loadSkillArgs("kubernetes-debugging"),
			wantContains: []string{
				"## Skill: kubernetes-debugging",
				"Check pod logs first.",
			},
		},
		{
			name:    "multiple valid skills",
			allowed: allSkillNames(),
			args:    loadSkillArgs("kubernetes-debugging", "aws-rds"),
			wantContains: []string{
				"## Skill: kubernetes-debugging",
				"## Skill: aws-rds",
				"Check connection limits.",
			},
		},
		{
			name:    "all invalid names",
			allowed: allSkillNames(),
			args:    loadSkillArgs("nonexistent", "also-fake"),
			wantContains: []string{
				"no valid skills found",
				"nonexistent",
				"also-fake",
				"Available skills:",
			},
			wantError: true,
		},
		{
			name:    "mix of valid and invalid",
			allowed: allSkillNames(),
			args:    loadSkillArgs("kubernetes-debugging", "nonexistent"),
			wantContains: []string{
				"## Skill: kubernetes-debugging",
				"Note: the following skill names were not found: nonexistent",
				"Available skills:",
			},
			wantError: false,
		},
		{
			name:    "name in registry but not in allowedNames",
			allowed: map[string]struct{}{"kubernetes-debugging": {}},
			args:    loadSkillArgs("aws-rds"),
			wantContains: []string{
				"no valid skills found",
				"aws-rds",
				"Available skills: kubernetes-debugging",
			},
			wantAbsent: []string{"aws-rds, kubernetes-debugging"},
			wantError:  true,
		},
		{
			name:    "partial: one allowed one not",
			allowed: map[string]struct{}{"kubernetes-debugging": {}},
			args:    loadSkillArgs("kubernetes-debugging", "aws-rds"),
			wantContains: []string{
				"## Skill: kubernetes-debugging",
				"Note: the following skill names were not found: aws-rds",
			},
			wantError: false,
		},
		{
			name:      "empty names array",
			allowed:   allSkillNames(),
			args:      loadSkillArgs(),
			wantError: true,
			wantContains: []string{
				"'names' must contain at least one skill name",
			},
		},
		{
			name:      "invalid JSON args",
			allowed:   allSkillNames(),
			args:      `{invalid}`,
			wantError: true,
			wantContains: []string{
				"invalid arguments",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := agent.NewStubToolExecutor(nil)
			s := NewSkillToolExecutor(inner, testRegistry(), tt.allowed)

			result, err := s.Execute(t.Context(), agent.ToolCall{
				ID:        "call-1",
				Name:      "load_skill",
				Arguments: tt.args,
			})
			require.NoError(t, err)
			assert.Equal(t, "call-1", result.CallID)
			assert.Equal(t, "load_skill", result.Name)
			assert.Equal(t, tt.wantError, result.IsError)

			for _, want := range tt.wantContains {
				assert.Contains(t, result.Content, want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, result.Content, absent)
			}
		})
	}
}

func TestSkillToolExecutor_Execute_DelegatesToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server1.read_file"},
	})
	s := NewSkillToolExecutor(inner, testRegistry(), allSkillNames())

	result, err := s.Execute(t.Context(), agent.ToolCall{
		ID:        "call-2",
		Name:      "server1.read_file",
		Arguments: `{"path": "/etc/hosts"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "call-2", result.CallID)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "server1.read_file")
}

func TestSkillToolExecutor_Close_DelegatesToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor(nil)
	s := NewSkillToolExecutor(inner, testRegistry(), allSkillNames())

	err := s.Close()
	assert.NoError(t, err)
}

func TestSkillToolExecutor_Close_NilInner(t *testing.T) {
	s := NewSkillToolExecutor(nil, testRegistry(), allSkillNames())

	err := s.Close()
	assert.NoError(t, err)
}

func TestSkillToolExecutor_Execute_NilRegistry(t *testing.T) {
	s := NewSkillToolExecutor(nil, nil, allSkillNames())

	result, err := s.Execute(context.Background(), agent.ToolCall{
		ID:        "call-nil-reg",
		Name:      "load_skill",
		Arguments: loadSkillArgs("kubernetes-debugging"),
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "no valid skills found")
}

func TestSkillToolExecutor_Execute_UnknownToolNilInner(t *testing.T) {
	s := NewSkillToolExecutor(nil, testRegistry(), allSkillNames())

	result, err := s.Execute(context.Background(), agent.ToolCall{
		ID:   "call-3",
		Name: "unknown_tool",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unknown tool")
}
