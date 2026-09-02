package prompt

import (
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// actionBehavioralInstructions is auto-injected for every action agent.
// Provides safety guardrails so that action agents get consistent behavioral
// guidance without duplicating it in their CustomInstructions.
const actionBehavioralInstructions = `## Action Agent Safety Guidelines

You are an action evaluation agent. Your role is to evaluate the analysis provided by previous
investigation stages and decide whether automated remediation actions are warranted.

Principles:
- Require hard evidence before acting — never act on speculation or low-confidence findings
- Your role is to evaluate the analysis provided by previous stages and decide whether to act — avoid re-investigating what has already been thoroughly analyzed
- If evidence is ambiguous or conflicting, report your assessment but do NOT act
- Explain your reasoning BEFORE executing any action tool
- Prefer inaction over incorrect action
- Write a short action memo: the decision (act or not), the evidence that justified it, which tools you ran (or why none), and the outcome. Do not reprint or re-author the investigation report`

const actionTaskFocus = "Focus on evaluating the upstream investigation findings and executing justified remediation actions via your available tools. When no action is warranted, explain why. Your final text is a short action memo, not a copy of the investigation."

// buildActionMessages builds the initial conversation for an action agent.
// System prompt: Tier 1-3 instructions + safety preamble + optional
// orchestrator sections (when SubAgentCatalog is non-empty) + action task focus.
// User message: alert + runbook + chain context + action-specific task.
func (b *PromptBuilder) buildActionMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	composed = composed + "\n\n" + actionBehavioralInstructions
	if len(execCtx.SubAgentCatalog) > 0 {
		composed = InjectOrchestratorSections(composed, execCtx.SubAgentCatalog)
	}
	systemContent := composed + "\n\n" + actionTaskFocus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
	}

	userContent := b.buildActionUserMessage(execCtx, prevStageContext)
	messages = append(messages, agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: userContent,
	})

	return messages
}

// buildActionUserMessage builds the user message for action agents.
// Uses the same alert/runbook/context sections as investigation but with
// action-specific task instructions so changes to analysisTask don't leak here.
func (b *PromptBuilder) buildActionUserMessage(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) string {
	var sb strings.Builder

	sb.WriteString(FormatAlertSection(execCtx.AlertType, execCtx.AlertData))
	sb.WriteString("\n")
	sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
	sb.WriteString("\n")
	sb.WriteString(FormatChainContext(prevStageContext))
	sb.WriteString("\n")
	sb.WriteString(actionTask)

	return sb.String()
}
