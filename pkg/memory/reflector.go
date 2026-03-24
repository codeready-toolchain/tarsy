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
	ScoringResult       controller.ScoringResult
	ExistingMemories    []Memory
	AlertType           string
	ChainID             string
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
	sb.WriteString("For each learning you identify, choose an action:\n")
	sb.WriteString("- **CREATE**: Genuinely new knowledge not covered by existing memories.\n")
	sb.WriteString("- **REINFORCE**: An existing memory is confirmed by this investigation — return its ID.\n")
	sb.WriteString("- **DEPRECATE**: An existing memory is contradicted or proven outdated — return its ID with\n  a reason.\n\n")

	sb.WriteString("Alert context for scoping:\n")
	fmt.Fprintf(&sb, "- Alert type: %s\n", input.AlertType)
	fmt.Fprintf(&sb, "- Chain: %s\n\n", input.ChainID)

	sb.WriteString("Respond with a JSON object (and nothing else):\n")
	sb.WriteString(reflectorOutputSchema)

	return sb.String()
}

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

## Quality Guidelines

- Extract only learnings that would **concretely help** a future investigation. Ask: "If an
  agent saw this memory before investigating a similar alert, would it change what it does?"
- Ground every learning in **specific evidence** from the investigation — tool call results,
  agent reasoning, or scoring critique. Do not extract generic SRE knowledge the agent already
  has.
- **Do not duplicate skill content.** The investigation timeline includes the agent's skills —
  both pre-loaded (at the start of the timeline) and dynamically loaded via ` + "`load_skill`" + ` tool
  calls. If a learning is already covered by a skill (e.g., classification criteria, report
  format, environment facts), do not extract it — the agent already knows it.
- Prefer **specific and actionable** over vague and general. "Check PgBouncer health before
  blaming the database" is better than "Consider all components in the request path."
- Negative learnings from mistakes are especially valuable — they prevent repeating errors.
- If the investigation was routine and existing memories already cover the lessons, return
  empty arrays. Not every investigation produces new learnings.`
