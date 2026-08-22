# Tool Summarization LLM Provider — Design Questions

**Status:** All decisions made
**Related:** [Design document](tool-summarization-provider-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where should the summarization provider be configured?

Operators need a place to say “compress tool dumps with Flash.” That can live globally, per MCP server, per chain, or some combination. Too little hierarchy forces copy-paste; too much copies the agent resolution stack for a job that does not vary by chain.

Today `summarization:` already exists on each MCP server for enablement and size limits. Scoring uses `defaults.scoring.llm_provider`. Executive summaries use chain-level `executive_summary_provider`. None of those patterns is a perfect fit: summarization is about the **tool payload**, not the investigation role.

### Option C: Defaults + per-MCP-server overlay (no chain/stage/agent)

- **Pro:** One global line for the 90% case; per-server override for the 10% (e.g. a security MCP server names Opus while kubectl inherits Flash).
- **Pro:** Reuses one `SummarizationConfig` shape in both places (`llm_provider` / `llm_backend` next to existing size knobs).
- **Pro:** Does not invent chain-level `summarization_provider` parallel to `executive_summary_provider`. Summarization does not vary by alert type.
- **Con:** Two lookup sites. Resolver must be explicit: agent → defaults → server.
- **Con:** `defaults.summarization` must document that only provider/backend inherit — enablement and thresholds stay per-server so a global block does not rewrite every server’s 5k threshold.

**Decision:** Option C — `defaults.summarization.llm_provider` with optional `mcp_servers.*.summarization.llm_provider` overlay. No chain/stage/agent level. Only provider/backend inherit from defaults; enablement and size thresholds stay per-server.

_Considered and rejected: Option A (per MCP server only — copy-paste across every server, cannot cover `search_past_sessions`), Option B (defaults only — forces a follow-up the first time one server needs a different model), Option D (full hierarchy — unused chain/stage complexity; MCP servers are shared across chains)._

---

## Q2: What happens when no summarization provider is set?

This is the backward-compatibility decision. Existing configs (including production overlays that never heard of this feature) must keep working.

### Option A: Unset = calling agent’s provider and backend (today’s behavior)

- **Pro:** Zero behavior change for current deployments.
- **Pro:** Explicit opt-in: expensive-model shops add `defaults.summarization`; everyone else is untouched.
- **Con:** The cost leak stays until operators configure it. Docs have to mention the knob.

**Decision:** Option A — unset means the calling agent’s provider and backend. Operators opt in via `defaults.summarization` (or a per-server overlay). Document the Sandbox snippet rather than changing implicit defaults.

_Considered and rejected: Option B (built-in `google-default` — surprise model swap, Claude-only shops would start needing `GOOGLE_API_KEY`), Option C (built-in Sonnet — still a surprise swap, weaker cost win than Flash)._

---

## Q3: Should `search_past_sessions` (RequiredSummarization) use the same provider?

`search_past_sessions` always runs an LLM summary of matched session rows. It is not size-gated, not MCP, and **fail-closes** on LLM error (the agent sees an error string, not the raw bundle). The prompt is “summarize past investigations matching this query,” which is more judgment-like than “compress this kubectl dump.”

Both paths already share `callSummarizationLLM`.

### Option A: Same resolver (defaults.summarization; no per-server overlay)

- **Pro:** One knob, one call site, one `model_name` story. Operators who set Flash for cost get the savings on session search too.
- **Pro:** Avoids a second YAML field (`defaults.memory.summarization_provider` or similar).
- **Con:** Session-history digests are slightly more quality-sensitive; Flash may drop admin-feedback nuance.
- **Con:** Fail-closed + a flaky cheap provider means “no history” rather than a worse summary (unless Q4 retries).

**Decision:** Option A — `search_past_sessions` uses the same resolver as MCP summarization. There is no MCP server to overlay, so it takes `defaults.summarization` (or the calling agent, per Q2, when that is unset). This was already implied by Q1 Option C: the global default exists in part so the one built-in summarization call is not left on a per-server-only knob.

_Considered and rejected: Option B (always agent model — would make `defaults.summarization` lie for the built-in that justified having defaults), Option C (separate YAML field — extra surface for a rare call)._

---

## Q4: What if the summarization provider fails?

MCP summarization **fail-opens** to the raw result. That preserves information but can dump 50k–100k tokens into the next investigation turn — often **more expensive** than retrying the summary on Opus. `search_past_sessions` fail-closes.

Agent `fallback_providers` do not apply today.

### Option D: Reuse the agent’s `fallback_providers` list (skip current name; do not mutate the investigator)

- **Pro:** No new YAML. Sandbox order is Sonnet → Opus → google-default → 3.1 Pro → 3.6 Flash; a Flash summarizer skips itself and lands on **Sonnet 5**, then Opus — not 3.1 Pro.
- **Pro:** Same list already encodes “stay in Claude before crossing Gemini families” for this deployment.
- **Pro:** Applies even when `defaults.summarization` is unset (Q2), so today’s agent-model summaries also fail over before dumping raw logs.
- **Con:** The list is authored for investigation failover; another deployment could order it poorly for a compressor. Operators who want a different summarization failover must reorder (or later add) a dedicated list.
- **Con:** Must **not** call `tryFallback` as-is — that helper mutates `execCtx.Config` and `original_llm_provider` on the agent execution, which would switch the investigator for the rest of the run.

**Decision:** Option D, with these constraints:

1. Walk `ResolvedFallbackProviders` from **list index 0** on the first failure of a primary (do not reuse the investigator’s `FallbackState` index). Skip the summarization provider currently in use (same skip-same-name rule as investigation fallback).
2. Local state only — never mutate the investigating agent’s provider or execution record. Do not reuse `tryFallback`.
3. `SingleShot` thresholds (trip on the first error), like scoring/exec summary.
4. Do not apply `RequiresNativeTools` skipping; summarization never needs Gemini native tools, so Flash can fail over to Sonnet even on a native-tool agent.
5. Stick the chosen summarization provider for the rest of **this** execution so a downed Flash is not retried on every large tool result. If the sticky provider later fails, continue **forward** in the list; do not retry the original primary.
6. After the list is exhausted, keep today’s fail-open (MCP) / fail-closed (`search_past_sessions`).

_Considered and rejected: Option A (fail-open-only — raw dumps after a cheap-model failure are the worst cost outcome), Option B (one agent-model retry — bounded but ignores the list operators already tuned; Sandbox would skip Sonnet and jump to Opus), Option C (dedicated summarization fallback list — extra YAML for v1)._

---

## Q5: How is `llm_backend` chosen for summarization?

TARSy has `langchain` and `google-native`. Summarization never binds MCP tools. Google-native still attaches **provider** native tools unless they are stripped (the design strips them either way).

### Option B: Explicit `llm_backend` next to `llm_provider`; default LangChain if provider is set and backend is omitted

- **Pro:** Same pair as `defaults.scoring` and stage agents. Sandbox can set `google-default` + `google-native` if they want.
- **Pro:** Omitted backend does not inherit the **agent’s** backend (an Opus+langchain investigator would otherwise force LangChain even when the summarizer is Gemini — that is actually fine; inheriting google-native from a Gemini clone onto a Sonnet summarizer would not be).
- **Con:** Two fields to set for the common Flash case.

**Decision:** Option B — `llm_backend` is explicit beside `llm_provider`. If a summarization provider is set and backend is omitted, default to **langchain**. Never inherit the investigating agent’s backend. Always clone the provider and clear `NativeTools`.

_Considered and rejected: Option A (always LangChain — simpler, but blocks google-native for Flash), Option C (infer from provider type — magic; `type: google` already works on LangChain)._

---

## Q6: If defaults set a summarization provider, can one MCP server go back to the agent model?

With Q1 Option C, last non-empty `llm_provider` wins. There is no YAML for “unset the inherited default.” A security MCP server might want Opus-quality summaries while kubectl uses Flash.

### Option A: No opt-out sentinel. Per-server can only name another provider.

- **Pro:** No magic strings. Resolver stays “last non-empty name.”
- **Pro:** “This MCP server should use Opus” is `llm_provider: vertexai-claude-opus` on that server; fallback then skip-same-name walks the shared list.
- **Con:** Cannot express “use whoever is investigating” (Opus on the Opus clone, Flash on the Gemini clone) while a global cheap default is set. Naming a concrete provider applies to both parallel agents.

**Decision:** Option A — no sentinel. A per-server overlay names another provider. That is enough for “kubectl on Flash, this security MCP on Opus.” The only thing a sentinel would add is “same as this agent,” which is not a v1 requirement.

_Considered and rejected: Option B (sentinel `agent` / `inherit` — magic name, extra resolver branch, not needed if the overlay names a real provider), Option C (`use_agent_model: true` — two ways to spell which model, easy to combine with `llm_provider` incorrectly)._

---

## Implementation clarifications (not new product questions)

Recorded during `/verify-design`. Details live in the design doc’s “Design-review clarifications” section.

- Named-provider layers do not inherit each other’s `llm_backend` (omit → langchain at that layer).
- `llm_backend` without `llm_provider` at the same level, and size/enabled fields on `defaults.summarization`, are validation errors.
- Summarization `GenerateInput.ExecutionID` is suffixed so google-native thought-signature cache stays on the investigator.
- Scoring / exec-summary / feedback reflector do not execute MCP or memory tools; they do not need the new `ExecutionContext` fields.

---
