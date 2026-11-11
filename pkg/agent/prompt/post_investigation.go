package prompt

import (
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// postInvestigationBehavioralInstructions is auto-injected for every
// post-investigation agent. Unlike the action layer it does not frame the job
// as remediation and allows the research the agent's task needs.
const postInvestigationBehavioralInstructions = `## Post-Investigation Agent Guidelines

The investigation is complete and has been scored. Your role is to decide whether a
follow-through outside this investigation is warranted (as defined by your instructions)
and, if so, carry it out with your tools. You do not remediate the incident.

- Base decisions on evidence from the investigation, not on speculation
- You may use your tools to gather the extra context your task needs (e.g. current state
  of the target system, existing tickets); do not redo the alert investigation
- If evidence is ambiguous or conflicting, report your assessment but do NOT act
- Explain your reasoning BEFORE executing any tool with side effects
- No action is a normal, successful outcome; prefer inaction over incorrect action
- Write a short memo: decision, evidence, tools used, outcome, links to anything you created`

const postInvestigationTaskFocus = "Focus on the follow-through defined by your instructions: carry it out via your available tools only when the investigation's evidence warrants it, otherwise explain why not. Your final text is a short memo, not a copy of the investigation."

// buildPostInvestigationMessages builds the initial conversation for a
// post-investigation agent.
// System prompt: Tier 1-3 instructions + post-investigation guidelines +
// optional orchestrator sections (when SubAgentCatalog is non-empty) + task focus.
// User message: alert + completed investigation context + task.
func (b *PromptBuilder) buildPostInvestigationMessages(
	execCtx *agent.ExecutionContext,
	investigationContext string,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	composed = composed + "\n\n" + postInvestigationBehavioralInstructions
	if len(execCtx.SubAgentCatalog) > 0 {
		composed = InjectOrchestratorSections(composed, execCtx.SubAgentCatalog)
	}
	systemContent := composed + "\n\n" + postInvestigationTaskFocus

	return []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
		{Role: agent.RoleUser, Content: b.buildPostInvestigationUserMessage(execCtx, investigationContext)},
	}
}

// buildPostInvestigationUserMessage builds the user message for post-investigation
// agents. investigationContext is pre-formatted by the executor (session metadata,
// final report, executive summary, labels, scoring verdict, optional timeline).
func (b *PromptBuilder) buildPostInvestigationUserMessage(
	execCtx *agent.ExecutionContext,
	investigationContext string,
) string {
	var sb strings.Builder

	sb.WriteString(FormatAlertSection(execCtx.AlertType, execCtx.AlertData))
	sb.WriteString("\n")
	sb.WriteString(formatCompletedInvestigation(investigationContext))
	sb.WriteString("\n")
	sb.WriteString(postInvestigationTask)

	return sb.String()
}

// formatCompletedInvestigation wraps the pre-formatted investigation context.
func formatCompletedInvestigation(investigationContext string) string {
	if investigationContext == "" {
		return "## Completed Investigation\nNo investigation context is available.\n"
	}

	var sb strings.Builder
	sb.WriteString("## Completed Investigation\n")
	sb.WriteString(investigationContext)
	sb.WriteString("\n")
	return sb.String()
}
