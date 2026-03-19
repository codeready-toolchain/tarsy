package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// formatRequiredSkillsSection wraps all required skills under a single
// "Pre-loaded Skills" container with a brief explanation. Each skill body
// is nested under a ### heading to avoid clashing with the skill's own
// markdown headings.
func formatRequiredSkillsSection(skills []agent.ResolvedSkill) string {
	var sb strings.Builder
	sb.WriteString("## Pre-loaded Skills\n\n")
	sb.WriteString("Skills provide domain-specific knowledge for your task.\n")
	sb.WriteString("The following have been loaded automatically — use them as reference.\n")

	for _, skill := range skills {
		fmt.Fprintf(&sb, "\n### %s\n\n%s\n", skill.Name, skill.Body)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatSkillCatalog generates the on-demand skill catalog with behavioral
// instructions for the LLM to decide whether to load skills via load_skill.
// Always uses the same template regardless of whether pre-loaded skills exist.
func formatSkillCatalog(skills []agent.SkillCatalogEntry) string {
	var sb strings.Builder
	sb.WriteString("## Available Skills\n\n")
	sb.WriteString("Skills provide domain-specific knowledge that may help with your task.\n")
	sb.WriteString("The following additional skills can be loaded on demand using the `load_skill` tool.\n")
	sb.WriteString("Scan the descriptions and decide:\n")
	sb.WriteString("- If one or more match your task: load them before proceeding.\n")
	sb.WriteString("- If none match: skip and proceed without them.\n\n")
	sb.WriteString("<available_skills>\n")
	for _, s := range skills {
		fmt.Fprintf(&sb, "- **%s**: %s\n", s.Name, s.Description)
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}
