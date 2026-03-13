package prompt

import "crypto/sha256"

const judgeSystemPrompt = `You are an expert investigation quality evaluator for TARSy, an automated incident investigation platform.

TARSy uses agent chains — multi-stage pipelines where AI agents investigate incidents by calling external tools (MCP tools), analyzing evidence, and producing findings. Different chains handle different types of incidents and may use different tools, agents, and configurations.

Your role is to critically evaluate investigation quality. The most important question is: did the investigation reach the right conclusion? Then: was the path there efficient and thorough? You evaluate both the outcome and the process, with outcome quality as the dominant factor.`

const judgePromptScore = `Your task is to critically evaluate the following automated incident investigation using a structured, outcome-first methodology.

## EVALUATION METHOD

Evaluate the investigation in two steps:

### Step 1 — Dimension Assessments

Evaluate the investigation across five dimensions. For each dimension, write a 2-4 sentence assessment. Every claim must cite specific evidence from the session data — exact tool calls, agent responses, or missing actions. Do not make assertions you cannot trace back to the investigation timeline.

Example: "Evidence Gathering: The agent concluded OOMKill after only checking pod status (tool call: test-mcp.get_pods at step 3), without verifying memory metrics despite having access to prometheus.query_range — incomplete_evidence."

If a dimension is not particularly relevant to a given investigation, note this briefly and move on.

**1. Investigation Outcome** — Is the conclusion correct, well-supported by evidence, and actionable? Were alternative explanations considered? Does the confidence level match the evidence quality?

**2. Evidence Gathering** — Did the agent collect sufficient evidence to support its conclusion? Did it verify claims with direct data, or rely on assumptions? Were relevant data sources left unexplored?

**3. Tool Utilization** — Were available tools used appropriately? Were obvious tools missed? Were tool results interpreted correctly? Did the agent recover from tool failures? Refer to the AVAILABLE TOOLS section in the session data.

**4. Analytical Reasoning** — Was the reasoning logically sound? Did the agent follow evidence to conclusions, or make unwarranted leaps? Was contradictory evidence addressed?

**5. Investigation Completeness** — Did the agent explore the problem space adequately, or stop too early? Were there wasted loops or irrelevant tangents?

### Step 2 — Holistic Narrative & Score

Synthesize the five dimension assessments into an overall narrative and a single score (0-100).

The Investigation Outcome dimension determines the score range:

| Outcome quality | Score range |
|---|---|
| Correct, well-supported conclusion | 60-100 |
| Partially correct or weakly supported conclusion | 35-59 |
| Wrong or unsupported conclusion | 0-34 |

The remaining four dimensions (Evidence Gathering, Tool Utilization, Analytical Reasoning, Investigation Completeness) determine where the score falls within that range. A flawed conclusion indicates flaws in the process — even if individual steps looked methodical, something went wrong.

**Scoring calibration:**

- 80-100: Correct conclusion, well-supported, efficient process
- 60-79: Correct conclusion with some gaps in evidence or process
- 35-59: Partially correct or weakly supported conclusion
- 0-34: Wrong conclusion, or so little evidence gathered that the conclusion is unsupported

%[3]s

## SESSION DATA

Below is the complete data for the investigation session, including the original alert, runbook (if any), available tools per agent, and the full investigation timeline.

----------------------------------- Session data start -----------------------------------
%[1]s
-----------------------------------  Session data end  -----------------------------------

## YOUR TASK NOW

Provide your evaluation following the methodology above.

1. Write your five dimension assessments (Step 1), citing specific evidence from the session data.
2. Write your holistic narrative and score (Step 2).

%[2]s
`

const judgePromptFollowupMissingTools = `Based on your analysis above, now produce a tool improvement report with two parts.

## Part 1: Missing Tools

Identify **new tools that do not currently exist** but should be created to improve future investigations.

**CRITICAL DISTINCTION:**
- If an **available tool** wasn't used when it should have been → you already covered this in Tool Utilization
- If a **new tool that doesn't exist yet** would have helped → list it here

**What qualifies as a "missing tool"?**

A new tool that doesn't currently exist and would have either:
1. Enabled capabilities that are impossible with current tools
2. Significantly simplified the investigation by combining multiple existing tools or automating complex multi-step processes

**DO NOT include:**
- Available tools that weren't used (already handled in your evaluation)
- Minor variations of existing tools
- Tools that would provide minimal simplification

**For each missing tool, provide:**
- **Tool name**: Specific name for the new tool (e.g., "auto-correlate-events")
- **Rationale**: What new capability this provides or how it simplifies the investigation

If no critical tools are missing, state "No critical missing tools identified."

## Part 2: Existing Tool Improvements

Review every tool interaction in the investigation (arguments passed, results returned, how the agent interpreted them) and identify improvements to existing tools.

**Categories to evaluate:**

- **Argument clarity** — Did the agent struggle to determine correct arguments? (e.g., tried multiple parameter combinations, guessed values)
- **Response format** — Did the tool return data that was hard for the agent to parse or extract useful information from?
- **Tool description** — Was there a relevant tool the agent didn't use, possibly because its name or description didn't indicate its relevance?
- **Missing discoverability** — Did the tool require argument values the agent had no way to discover from the available context?

**For each improvement, provide:**
- **Tool name** (as it appears in the AVAILABLE TOOLS section)
- **What to improve** (argument names, response format, description, etc.)
- **Why** (what was observed in the investigation that suggests this improvement)

If no improvements are needed, state "No existing tool improvements identified."

**Format your response as freeform text.** Number each item and provide clear explanations.
`

const judgePromptScoreReminder = `I could not parse the total score from your response. Please end your response with the total score as a single number on its own line — no other text, formatting, or explanation on that line.

%[1]s
`

var combinedPromptsHash [32]byte

func init() {
	vocabStr := FormatVocabularyForHash(FailureVocabulary)
	combinedPromptsHash = sha256.Sum256([]byte(
		judgeSystemPrompt + judgePromptScore + judgePromptScoreReminder +
			judgePromptFollowupMissingTools + vocabStr,
	))
}

// GetCurrentPromptHash returns the hash of the current version of the judge prompts.
func GetCurrentPromptHash() [32]byte {
	return combinedPromptsHash
}
