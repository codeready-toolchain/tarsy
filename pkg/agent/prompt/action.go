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
- Your final report becomes the finalAnalysis that the exec summary stage will summarize. Preserve the investigation report from previous stages and amend it with an actions section covering: what actions were taken (or why none were taken), the reasoning behind each decision, and the outcome of each action. Do not replace the investigation report with a purely action-oriented summary`

const actionTaskFocus = "Focus on evaluating the upstream investigation findings and executing justified remediation actions via your available tools. When no action is warranted, explain why and preserve the investigation report as-is."

// buildActionMessages builds the initial conversation for an action agent.
// System prompt: Tier 1-3 instructions + safety preamble + task focus.
// User message: alert + runbook + chain context + action-specific task.
func (b *PromptBuilder) buildActionMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	systemContent := composed + "\n\n" + actionBehavioralInstructions + "\n\n" + actionTaskFocus

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
