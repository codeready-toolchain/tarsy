# Scoring Prompt Review & Recommendations

**Date:** 2026-03-09
**Scope:** `pkg/agent/prompt/judges.go` — system prompt, Turn 1 (score evaluation), Turn 2 (missing tools)

## Findings

### 1. System Prompt Is Too Thin

**Severity:** High

The system prompt is a single generic sentence:

> You are an expert evaluator with deep domain knowledge in the subject matter.

It doesn't establish the domain (incident investigation, Kubernetes/infrastructure), explain what TARSy is, what agent chains are, or what MCP tools are. The LLM must infer all of this from the session data alone, which weakens persona grounding and scoring accuracy.

**Recommendation:** Expand the system prompt with domain context that is **generic across all chains**:

- What TARSy is (automated incident investigation platform)
- What agent chains are (multi-stage pipelines — the specific stages vary per chain)
- What MCP tools are (external tool integrations — the specific tools vary per chain/server)
- The evaluator's role (assess investigation quality, not participate in it)

Keep it chain-agnostic — don't mention specific tool servers, stage names, or domain areas (e.g., Kubernetes). Chain-specific context (available tools, alert details) belongs in the user message alongside the session data, not in the system prompt. The system prompt is the strongest position for persona setup — moving the generic domain framing there (instead of burying it in the user message) improves consistency across both turns.

---

### 2. "Evaluation" vs "Investigation" Terminology Confusion

**Severity:** High

The prompt consistently uses "evaluation" and "evaluator" to describe the investigation being scored:

> Your role is to evaluate evaluation tasks with EXTREME CRITICAL RIGOR.

TARSy runs **investigations**, not evaluations. The current wording creates confusing meta-language — "evaluate an evaluation" — when it should be "evaluate an investigation." This ambiguity runs through the entire prompt: rubric criteria, checklists, calibration reminders, and examples all use "evaluation" where "investigation" is meant.

**Recommendation:** Replace throughout:

| Current | Should be |
|---------|-----------|
| "evaluation tasks" | "incident investigations" |
| "the evaluator" | "the agent" / "the investigator" |
| "evaluation paths" | "investigation paths" |
| "the evaluation" | "the investigation" |

---

### 3. Missing Tools Section Duplicated in Turn 1 (Wasted Tokens)

**Severity:** High

Turn 1 (`judgePromptScore`, lines 142–175) contains the complete "IDENTIFYING MISSING TOOLS" guidance (~35 lines). But Turn 1's purpose is scoring. The missing tools analysis happens in Turn 2 (`judgePromptFollowupMissingTools`), which repeats the exact same section verbatim.

Impact:

- Wastes input tokens on every scoring call
- Dilutes the LLM's focus — it may mix missing tool commentary into the score analysis
- Turn 2 re-sends everything anyway, so there's zero information loss from removing it

**Recommendation:** Remove the entire "IDENTIFYING MISSING TOOLS" section from `judgePromptScore`. It belongs exclusively in Turn 2.

---

### 4. No Available MCP Tools List Provided

**Severity:** High

The "Tool Relevance" rubric category (25% of the total score) asks:

> Were all relevant **existing MCP tools** utilized, or were some obvious ones ignored?

But the prompt never tells the LLM **what tools were available**. The LLM can only see tools that were actually called in the session data. It cannot identify tools that *should have been used but weren't* — which is the core purpose of this category.

This means the Tool Relevance score is fundamentally unreliable: the LLM is scoring tool completeness without knowing the complete tool set.

**Challenges:**

- Different agents within the same chain may have different tools available — not just MCP tools but also native LLM tools (e.g., Gemini code execution, Google Search). The tools list is per-agent, not per-session.
- The MCP tool list can change over time, so historical sessions may have had a different set. In practice this is minor since scoring typically runs immediately after the investigation.

**Recommendation:** Include per-agent tool availability in the scoring context. Since the session data already shows per-agent sections, the tools list can follow the same structure. Use the tool list from the current configuration at scoring time (don't try to reconstruct the historical list — not worth the complexity).

This requires `buildScoringContext` in `ScoringExecutor` to gather tool metadata (MCP tools from server configs + any native tools from agent config) and include it per agent. The tool discovery calls (`tools/list`) already happen at agent startup, so the data is available.

Format suggestion:

```
## AVAILABLE TOOLS PER AGENT

### Investigator
MCP tools:
- k8s.get_pods(namespace) — List pods in a namespace with status and restart counts
- k8s.get_logs(pod, namespace, lines) — Retrieve recent log lines from a pod
- prometheus.query_metrics(query, range) — Execute a PromQL query
Native tools: Google Search, Code Execution

### Remediator
MCP tools:
- k8s.restart_pod(pod, namespace) — Restart a pod
- k8s.scale_deployment(deployment, replicas) — Scale a deployment
Native tools: none
```

---

### 5. No Original Alert Context

**Severity:** High

The session data shows the investigation timeline but not **what triggered the investigation**. The scoring LLM doesn't know the original alert — its severity, description, affected resources, or the question the investigation was supposed to answer.

Without this, the LLM cannot properly assess:

- Whether the investigation addressed the right problem
- Whether the scope was appropriate for the alert severity
- Whether the conclusion actually answered the original question
- Whether tool selection was appropriate given the alert type

**Recommendation:** Prepend the raw `alert_data` and the matched runbook (fetched from `runbook_url`, if set) before the investigation timeline in the session data section. The alert data is the raw payload as received — the scoring LLM can parse it. The runbook is especially important — it defines what the investigation *should* have done, making it the ground truth for evaluating whether the agents followed the intended procedure and used the right tools.

---

### 6. "Language Patterns to Use" Section Is Prompt Bloat

**Severity:** Medium

Lines 118–144 dictate exact phrases the LLM should use:

> - "However, there are significant logical issues..."
> - "This represents a critical logical shortcut because..."
> - "The agent should have immediately..."

This section (~30 lines) micromanages output style without improving scoring quality. The calibration reminders and "prefer criticism over praise" philosophy already establish the critical tone sufficiently. Prescribing exact phrases makes responses feel templated and wastes tokens.

**Recommendation:** Remove the "LANGUAGE PATTERNS TO USE" section entirely. Also remove the companion instruction:

> Use critical language ("however," "failed to," "should have," "never attempted")

The rubric's requirement to "explain point deductions explicitly for each category" already ensures substantive critique.

---

### 7. Calibration Bias Is Overly Harsh

**Severity:** Medium

The prompt contains strong anchoring toward low scores:

> If you're scoring above 70, you're being too lenient. Re-examine for missed opportunities.

> Your average score should be 55-75 out of 100.

This compresses the useful scoring range into a narrow 55–75 band where differentiation is difficult. A genuinely well-executed investigation — thorough tool usage, consistent logic, strong synthesis, appropriate confidence — should be able to score 80+. The current calibration would penalize good work and make it hard to distinguish "adequate" from "excellent."

**Recommendation:**

- Remove "If you're scoring above 70, you're being too lenient"
- Widen the average guidance: "Your average score should be 50–70"
- State explicitly that 80+ is achievable for thorough, well-executed investigations
- The rubric criteria themselves are strict enough — artificial deflation on top of a strict rubric produces unfairly low scores

---

### 8. "200 Words Minimum" Incentivizes Padding

**Severity:** Low

> Your evaluation must: Be at least 200 words

This incentivizes the LLM to pad its response to meet a threshold. A concise, precise 150-word analysis pointing out two key issues is more valuable than a 250-word response that stretches to fill space. For straightforward investigations (good or bad), forced length produces filler.

**Recommendation:** Remove the minimum word count. The requirement to "explain point deductions explicitly for each category" already ensures substantive output without encouraging padding.

---

### 9. Score Extraction Is Fragile

**Severity:** Low

The output format relies on a bare number on the last line:

> End your response with the total score as a standalone number on the last line.

The controller includes a retry mechanism (`maxExtractionRetries = 5`) for when the LLM writes `**Total: 62**` or `Total Score: 62` instead. This works but each retry is a full LLM call.

**Recommendation:** No urgent change — the retry mechanism handles this pragmatically. However, if the LLM provider supports structured output (JSON mode), switching to it would eliminate retries entirely and make extraction deterministic.

---

## Summary

| # | Finding | Severity | Type |
|---|---------|----------|------|
| 1 | System prompt is too thin | High | Context |
| 2 | "Evaluation" vs "investigation" terminology | High | Clarity |
| 3 | Missing tools section duplicated in Turn 1 | High | Token waste |
| 4 | No available MCP tools list | High | Missing data |
| 5 | No original alert context | High | Missing data |
| 6 | "Language patterns" section is bloat | Medium | Token waste |
| 7 | Calibration bias is overly harsh | Medium | Scoring accuracy |
| 8 | "200 words minimum" incentivizes padding | Low | Output quality |
| 9 | Score extraction is fragile | Low | Robustness |

### Effort Estimate

- **Prompt-only changes** (1, 2, 3, 6, 7, 8): Pure text edits in `judges.go`. Low effort, no backend changes.
- **Context pipeline changes** (4, 5): Require modifications to `ScoringExecutor.buildScoringContext` to gather MCP tool metadata and alert data, and to `FormatStructuredInvestigation` or the prompt builder to include them. Medium effort.
- **Output format change** (9): Requires LLM provider support for structured output. Deferred.
