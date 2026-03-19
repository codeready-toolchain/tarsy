package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// formatRequiredSkill wraps a required skill's body with a section header.
// Consistent with MCP's "## {serverID} Instructions" pattern.
func formatRequiredSkill(skill agent.ResolvedSkill) string {
	return fmt.Sprintf("## Skill: %s\n\n%s", skill.Name, skill.Body)
}

// formatSkillCatalog generates the behavioral nudge and bullet-point catalog
// for on-demand skills available via the load_skill tool.
func formatSkillCatalog(skills []agent.SkillCatalogEntry) string {
	var sb strings.Builder
	sb.WriteString("## Available Domain Knowledge\n\n")
	sb.WriteString("Before starting your task, scan the skill descriptions below and load any\n")
	sb.WriteString("that match the current context (alert type, environment, workload type).\n")
	sb.WriteString("These contain domain-specific knowledge that may not be in your training data.\n\n")
	for _, s := range skills {
		fmt.Fprintf(&sb, "- **%s**: %s\n", s.Name, s.Description)
	}
	sb.WriteString("\nUse the `load_skill` tool to load relevant skills by name before proceeding.\n")
	sb.WriteString("You can load multiple skills in one call. If no skill description matches\n")
	sb.WriteString("your current task, do not load any.")
	return sb.String()
}
