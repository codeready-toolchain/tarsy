package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestFormatRequiredSkill(t *testing.T) {
	skill := agent.ResolvedSkill{
		Name: "kubernetes-basics",
		Body: "Always check pod status before node status.\n\nUse `kubectl get pods` first.",
	}

	result := formatRequiredSkill(skill)

	assert.Equal(t, "## Skill: kubernetes-basics\n\nAlways check pod status before node status.\n\nUse `kubectl get pods` first.", result)
}

func TestFormatSkillCatalog(t *testing.T) {
	catalogSuffix := "\nUse the `load_skill` tool to load relevant skills by name before proceeding.\n" +
		"You can load multiple skills in one call. If no skill description matches\n" +
		"your current task, do not load any."

	catalogHeader := "## Available Domain Knowledge\n\n" +
		"Before starting your task, scan the skill descriptions below and load any\n" +
		"that match the current context (alert type, environment, workload type).\n" +
		"These contain domain-specific knowledge that may not be in your training data.\n\n"

	t.Run("multiple entries", func(t *testing.T) {
		skills := []agent.SkillCatalogEntry{
			{Name: "kubernetes-basics", Description: "Core K8s troubleshooting patterns"},
			{Name: "networking", Description: "Network debugging and DNS resolution"},
		}

		result := formatSkillCatalog(skills)

		expected := catalogHeader +
			"- **kubernetes-basics**: Core K8s troubleshooting patterns\n" +
			"- **networking**: Network debugging and DNS resolution\n" +
			catalogSuffix
		assert.Equal(t, expected, result)
	})

	t.Run("single entry", func(t *testing.T) {
		skills := []agent.SkillCatalogEntry{
			{Name: "networking", Description: "Network debugging"},
		}

		result := formatSkillCatalog(skills)

		expected := catalogHeader +
			"- **networking**: Network debugging\n" +
			catalogSuffix
		assert.Equal(t, expected, result)
	})

	t.Run("preserves entry order", func(t *testing.T) {
		skills := []agent.SkillCatalogEntry{
			{Name: "z-skill", Description: "Last alphabetically"},
			{Name: "a-skill", Description: "First alphabetically"},
		}

		result := formatSkillCatalog(skills)

		expected := catalogHeader +
			"- **z-skill**: Last alphabetically\n" +
			"- **a-skill**: First alphabetically\n" +
			catalogSuffix
		assert.Equal(t, expected, result)
	})
}
