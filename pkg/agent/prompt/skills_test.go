package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestFormatRequiredSkillsSection_Single(t *testing.T) {
	skills := []agent.ResolvedSkill{
		{Name: "kubernetes-basics", Body: "Always check pod status before node status.\n\nUse `kubectl get pods` first."},
	}

	result := formatRequiredSkillsSection(skills)

	expected := "## Pre-loaded Skills\n\n" +
		"Skills provide domain-specific knowledge for your task.\n" +
		"The following have been loaded automatically — use them as reference.\n\n" +
		"### kubernetes-basics\n\n" +
		"Always check pod status before node status.\n\nUse `kubectl get pods` first."
	assert.Equal(t, expected, result)
}

func TestFormatRequiredSkillsSection_Multiple(t *testing.T) {
	skills := []agent.ResolvedSkill{
		{Name: "k8s-basics", Body: "Pod troubleshooting guide."},
		{Name: "networking", Body: "DNS resolution steps."},
	}

	result := formatRequiredSkillsSection(skills)

	assert.Contains(t, result, "## Pre-loaded Skills")
	assert.Contains(t, result, "### k8s-basics\n\nPod troubleshooting guide.")
	assert.Contains(t, result, "### networking\n\nDNS resolution steps.")

	idxFirst := len("### k8s-basics")
	idxSecond := len("### networking")
	_ = idxFirst
	_ = idxSecond

	// Verify ordering preserved
	assert.Less(t,
		indexOf(result, "### k8s-basics"),
		indexOf(result, "### networking"),
		"skills should appear in order",
	)
}

func TestFormatSkillCatalog(t *testing.T) {
	catalogHeader := "## Available Skills\n\n" +
		"Skills provide domain-specific knowledge that may help with your task.\n" +
		"The following additional skills can be loaded on demand using the `load_skill` tool.\n" +
		"Scan the descriptions and decide:\n" +
		"- If one or more match your task: load them before proceeding.\n" +
		"- If none match: skip and proceed without them.\n\n" +
		"<available_skills>\n"

	catalogFooter := "</available_skills>"

	t.Run("multiple entries", func(t *testing.T) {
		skills := []agent.SkillCatalogEntry{
			{Name: "kubernetes-basics", Description: "Core K8s troubleshooting patterns"},
			{Name: "networking", Description: "Network debugging and DNS resolution"},
		}

		result := formatSkillCatalog(skills)

		expected := catalogHeader +
			"- **kubernetes-basics**: Core K8s troubleshooting patterns\n" +
			"- **networking**: Network debugging and DNS resolution\n" +
			catalogFooter
		assert.Equal(t, expected, result)
	})

	t.Run("single entry", func(t *testing.T) {
		skills := []agent.SkillCatalogEntry{
			{Name: "networking", Description: "Network debugging"},
		}

		result := formatSkillCatalog(skills)

		expected := catalogHeader +
			"- **networking**: Network debugging\n" +
			catalogFooter
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
			catalogFooter
		assert.Equal(t, expected, result)
	})
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
