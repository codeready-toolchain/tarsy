package memory

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
)

// ReflectorInput carries the data needed to build Reflector prompts.
type ReflectorInput struct {
	InvestigationContext string
	ScoringResult        controller.ScoringResult
	ExistingMemories     []Memory
	AlertType            string
	ChainID              string
}

// NewReflectorController creates a SingleShotController configured for memory extraction.
// The returned controller uses the standard infrastructure: retries, fallback,
// metrics, timeline events, message persistence, and LLM interaction recording.
func NewReflectorController(input ReflectorInput) *controller.SingleShotController {
	return controller.NewSingleShotController(controller.SingleShotConfig{
		BuildMessages: func(_ *agent.ExecutionContext, _ string) []agent.ConversationMessage {
			return []agent.ConversationMessage{
				{Role: agent.RoleSystem, Content: buildReflectorSystemPrompt()},
				{Role: agent.RoleUser, Content: buildReflectorUserPrompt(input)},
			}
		},
		ThinkingFallback: true,
		InteractionLabel: llminteraction.InteractionTypeMemoryExtraction,
	})
}

func buildReflectorSystemPrompt() string {
	return reflectorSystemPrompt
}

func buildReflectorUserPrompt(input ReflectorInput) string {
	var sb strings.Builder

	sb.WriteString("Below is a completed TARSy investigation and its quality evaluation. Extract discrete\nlearnings for future investigations.\n\n")

	sb.WriteString("## Investigation\n\n<investigation_context>\n")
	sb.WriteString(input.InvestigationContext)
	sb.WriteString("\n</investigation_context>\n\n")

	sb.WriteString("## Quality Evaluation\n\n")
	fmt.Fprintf(&sb, "Score: %d/100\n", input.ScoringResult.TotalScore)
	if len(input.ScoringResult.FailureTags) > 0 {
		fmt.Fprintf(&sb, "Failure tags: %s\n", strings.Join(input.ScoringResult.FailureTags, ", "))
	} else {
		sb.WriteString("Failure tags: none\n")
	}
	sb.WriteString("\n<score_analysis>\n")
	sb.WriteString(input.ScoringResult.ScoreAnalysis)
	sb.WriteString("\n</score_analysis>\n\n")

	sb.WriteString("<tool_improvement_report>\n")
	sb.WriteString(input.ScoringResult.ToolImprovementReport)
	sb.WriteString("\n</tool_improvement_report>\n\n")

	sb.WriteString("## Existing Memories\n\n")
	sb.WriteString("These memories were previously extracted from past investigations in this project. Use them\nto avoid creating duplicates and to decide what to reinforce or deprecate.\n\n<existing_memories>\n")
	if len(input.ExistingMemories) > 0 {
		memoriesJSON, _ := json.Marshal(input.ExistingMemories)
		sb.Write(memoriesJSON)
	} else {
		sb.WriteString("[]")
	}
	sb.WriteString("\n</existing_memories>\n\n")

	sb.WriteString("## Your Task\n\n")
	sb.WriteString("For each learning you identify, select one or more actions:\n")
	sb.WriteString("- **CREATE**: Genuinely new knowledge not covered by existing memories.\n")
	sb.WriteString("- **REINFORCE**: An existing memory is confirmed by this investigation — return its ID.\n")
	sb.WriteString("- **DEPRECATE**: An existing memory is contradicted or proven outdated — return its ID with\n  a reason.\n\n")
	sb.WriteString("When an existing memory is contradicted, emit both a DEPRECATE for the stale memory and a\nCREATE for the corrected replacement.\n\n")

	sb.WriteString("Alert context for scoping:\n")
	fmt.Fprintf(&sb, "- Alert type: %s\n", input.AlertType)
	fmt.Fprintf(&sb, "- Chain: %s\n\n", input.ChainID)

	sb.WriteString("Respond with a JSON object (and nothing else):\n")
	sb.WriteString(reflectorOutputSchema)

	return sb.String()
}

// FeedbackReflectorInput carries the data needed for the feedback Reflector variant.
type FeedbackReflectorInput struct {
	FeedbackText         string
	QualityRating        string
	InvestigationContext string // full context: alert data, runbook, timeline, tools
	ExistingMemories     []Memory
	AlertType            string
	ChainID              string
}

// NewFeedbackReflectorController creates a SingleShotController for extracting
// memories from human review feedback text.
func NewFeedbackReflectorController(input FeedbackReflectorInput) *controller.SingleShotController {
	return controller.NewSingleShotController(controller.SingleShotConfig{
		BuildMessages: func(_ *agent.ExecutionContext, _ string) []agent.ConversationMessage {
			return []agent.ConversationMessage{
				{Role: agent.RoleSystem, Content: feedbackReflectorSystemPrompt},
				{Role: agent.RoleUser, Content: buildFeedbackReflectorUserPrompt(input)},
			}
		},
		ThinkingFallback: true,
		InteractionLabel: llminteraction.InteractionTypeMemoryExtraction,
	})
}

func buildFeedbackReflectorUserPrompt(input FeedbackReflectorInput) string {
	var sb strings.Builder

	sb.WriteString("A human reviewer has completed their review of a TARSy investigation. Extract learnings from their feedback.\n\n")

	sb.WriteString("## Full Investigation Context\n\n")
	sb.WriteString("This is the complete investigation the reviewer evaluated, including the original alert,\n")
	sb.WriteString("runbook, tools used, and the full timeline of agent actions and findings.\n\n")
	sb.WriteString("<investigation_context>\n")
	sb.WriteString(input.InvestigationContext)
	sb.WriteString("\n</investigation_context>\n\n")

	sb.WriteString("## Human Review\n\n")
	fmt.Fprintf(&sb, "Quality rating: %s\n\n", input.QualityRating)
	sb.WriteString("<feedback>\n")
	sb.WriteString(input.FeedbackText)
	sb.WriteString("\n</feedback>\n\n")

	sb.WriteString("## Existing Memories\n\n")
	sb.WriteString("These memories were previously extracted from this and past investigations. Use them\nto avoid duplicates and to decide what to reinforce or deprecate.\n\n<existing_memories>\n")
	if len(input.ExistingMemories) > 0 {
		memoriesJSON, _ := json.Marshal(input.ExistingMemories)
		sb.Write(memoriesJSON)
	} else {
		sb.WriteString("[]")
	}
	sb.WriteString("\n</existing_memories>\n\n")

	sb.WriteString("## Your Task\n\n")
	sb.WriteString("For each learning from the human feedback, select one or more actions:\n")
	sb.WriteString("- **CREATE**: New knowledge from the feedback not covered by existing memories.\n")
	sb.WriteString("- **REINFORCE**: An existing memory is confirmed by the feedback — return its ID.\n")
	sb.WriteString("- **DEPRECATE**: An existing memory is contradicted by the feedback — return its ID with a reason.\n\n")
	sb.WriteString("When an existing memory is contradicted, emit both a DEPRECATE for the stale memory and a\nCREATE for the corrected replacement.\n\n")

	sb.WriteString("Alert context for scoping:\n")
	fmt.Fprintf(&sb, "- Alert type: %s\n", input.AlertType)
	fmt.Fprintf(&sb, "- Chain: %s\n\n", input.ChainID)

	sb.WriteString("Respond with a JSON object (and nothing else):\n")
	sb.WriteString(reflectorOutputSchema)

	return sb.String()
}

const feedbackReflectorSystemPrompt = `You are a memory extraction specialist for TARSy, an automated incident investigation platform.

A human reviewer has examined a completed investigation and provided feedback — their assessment
of what the automated investigation got right, got wrong, or missed. Your role is to extract
discrete, reusable learnings from this human feedback that will help future investigations.

Human feedback is the strongest signal TARSy receives. The reviewer has domain expertise and
has verified findings against reality. Pay close attention to:
- Corrections: what the investigation got wrong (create negative/procedural memories)
- Confirmations: what the investigation got right (reinforce existing memories)
- Missing context: facts the investigation should have known (create semantic memories)
- Better approaches: alternative investigation strategies (create procedural memories)

## Memory Categories

- **semantic** — Facts about infrastructure, services, alert patterns, or environment behavior.
- **episodic** — Specific investigation experiences: what worked, failed, or was surprising.
- **procedural** — Investigation strategies, tool usage patterns, or anti-patterns.

## Memory Valence

- **positive** — A pattern that worked well and should be repeated.
- **negative** — A mistake, dead end, or anti-pattern to avoid.
- **neutral** — A factual observation with no clear positive/negative implication.

## Extraction Boundaries

The following do NOT belong in memories even when mentioned in feedback:

- **Tool limitations, bugs, and workarounds.** If the reviewer notes a tool failure or
  workaround, that is a tooling issue — not a learning for future investigations.
- **Domain-generic procedures.** General investigation steps that apply to every alert of a
  given type belong in skills or runbooks, not memories.
- **Speculative findings.** Only extract learnings the reviewer confirmed with evidence or
  domain expertise. Do not extract unverified theories.

## Guidelines

- Extract only learnings that would **concretely help** a future investigation.
- The human's corrections are especially valuable — they represent ground truth.
- If the feedback merely says "good job" with no specific learnings, return empty arrays.
- Do not duplicate existing memories. If the feedback confirms an existing memory, reinforce it.
- If the feedback contradicts an existing memory, deprecate it and create a corrected version.`

const reflectorOutputSchema = `{
  "create": [
    {
      "content": "string — the learning (a sentence to a short paragraph)",
      "category": "semantic | episodic | procedural",
      "valence": "positive | negative | neutral"
    }
  ],
  "reinforce": [
    {
      "memory_id": "string — ID of existing memory to reinforce"
    }
  ],
  "deprecate": [
    {
      "memory_id": "string — ID of existing memory to deprecate",
      "reason": "string — why this memory is no longer valid"
    }
  ]
}`

const reflectorSystemPrompt = `You are a memory extraction specialist for TARSy, an automated incident investigation platform.

TARSy uses agent chains — multi-stage pipelines where AI agents investigate incidents by
calling external tools (MCP tools), analyzing evidence, and producing findings. Different
chains handle different types of incidents and may use different tools, agents, and
configurations. Agents are expert Site Reliability Engineers with access to infrastructure
tools (Kubernetes, Prometheus, cloud APIs, log systems, etc.).

After each investigation, a quality evaluator scores the session (0-100) based on outcome
correctness, evidence gathering, tool utilization, analytical reasoning, and completeness.
It also produces failure tags and a tool improvement report.

Your role is to analyze the full investigation and its quality evaluation to extract discrete,
reusable learnings that will help future investigations of similar alerts. You receive:
- The original alert and runbook
- The full investigation timeline, which includes:
  - Pre-loaded skills (domain knowledge injected into the agent's prompt before investigation)
  - Agent reasoning, tool calls with arguments and results, final analysis
- The quality score, analysis, failure tags, and tool improvement report
- Existing memories from past investigations (for deduplication)

## Memory Categories

Each learning falls into one category:

- **semantic** — Facts about infrastructure, services, alert patterns, or environment behavior.
  Example: "The payments-api service connects to PostgreSQL on port 5432 via PgBouncer, not
  directly — connection timeout alerts should check PgBouncer health first."

- **episodic** — Specific investigation experiences: what approach worked, what failed, what
  was surprising. Tied to a concrete event.
  Example: "When investigating OOMKill in the order-processor pod, checking the Prometheus
  container_memory_working_set_bytes metric was more reliable than container_memory_usage_bytes
  because the latter includes cache."

- **procedural** — Investigation strategies, tool usage patterns, or anti-patterns that apply
  across multiple investigations.
  Example: "For certificate expiry alerts, always check both the ingress certificate and the
  backend service certificate — they can expire independently."

## Memory Valence

- **positive** — A pattern that worked well and should be repeated.
- **negative** — A mistake, dead end, or anti-pattern to avoid in the future.
- **neutral** — A factual observation with no clear positive/negative implication.

## Extraction Boundaries

The following do NOT belong in memories and must NOT be extracted:

- **Tool limitations, bugs, and workarounds.** If a tool returned an error, timed out, or
  required a non-obvious workaround, that is a tooling issue — not a learning for future
  investigations. These belong in tool improvement reports, skills, or runbook updates.
- **Domain-generic procedures.** Investigation steps that apply to every alert of a given type
  (e.g., "always check logs first") belong in skills or runbooks, not memories. Memories are
  for specific, surprising, or non-obvious findings.
- **Speculative findings.** Only extract learnings confirmed by tool output or concrete
  evidence from the investigation. Do not extract guesses, hypotheses, or unverified theories.
- **Skill content.** If a learning is already covered by the agent's skills (visible in the
  investigation timeline as pre-loaded or dynamically loaded via ` + "`load_skill`" + `), do not extract
  it — the agent already knows it.

Aim for **0–3 new memories** per investigation. Most investigations confirm existing knowledge
rather than produce new learnings — reinforcing existing memories or returning empty arrays is
the expected outcome, not the exception. Exceeding 3 creates should be rare and justified by
a genuinely rich investigation. When in doubt, reinforce an existing memory rather than
creating a near-duplicate.

If there is nothing to create, reinforce, or deprecate, return the JSON structure with all
empty arrays: {"create": [], "reinforce": [], "deprecate": []}. This is the correct response
for routine investigations — do not invent learnings to fill the output.

## Quality Guidelines

- Extract only learnings that would **concretely help** a future investigation. Ask: "If an
  agent saw this memory before investigating a similar alert, would it change what it does?"
- Ground every learning in **specific evidence** from the investigation — tool call results,
  agent reasoning, or scoring critique. Do not extract generic SRE knowledge the agent already
  has.
- Prefer **specific and actionable** over vague and general. "Check PgBouncer health before
  blaming the database" is better than "Consider all components in the request path."
- Negative learnings from mistakes are especially valuable — they prevent repeating errors.`
