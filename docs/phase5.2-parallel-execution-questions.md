# Phase 5.2: Parallel Execution — Open Questions

**Status**: ✅ All questions resolved — decisions reflected in design doc
**Related**: `docs/phase5.2-parallel-execution-design.md`

---

## Q1: Synthesis Placement — Separate Stage vs AgentExecution Within Parallel Stage

**Status**: ✅ Decided — **Separate Stage record**

**Decision:** Synthesis creates its own `Stage` DB record (e.g., "Investigation - Synthesis") with a single `AgentExecution` for the synthesis agent. This requires no changes to `StageService.UpdateStageStatus()` — the aggregation logic stays clean because the parallel Stage status reflects only parallel agents and the synthesis Stage status reflects synthesis independently. The `dbStageIndex` in the chain loop increments naturally; `stage_index` uniqueness constraint is satisfied. Consistent with old TARSy (synthesis gets its own stage_execution at a new index). The trade-off is that `stage_index` in DB diverges from config stage position when synthesis stages are inserted, but this is cosmetic and the `stage_name` field disambiguates clearly.

---

## Q2: Context Passed to Synthesis Agent

**Status**: ✅ Decided — **Full conversation messages from DB**

**Decision:** Query each parallel agent's timeline events from DB via `TimelineService.GetAgentTimeline()` and format the full investigation history for synthesis. Synthesis needs to evaluate the quality of each agent's investigation — not just trust its conclusions. It must see the reasoning (thinking content), tool calls and their results, and the chain of evidence to determine whether findings are well-supported or fabricated. Timeline events are the right data source over raw Messages because the Message schema has no thinking field — thinking content is recorded only in timeline events (`llm_thinking` type). Timeline events also capture tool result summaries, code execution, and grounding results in display order. Passing only `final_analysis` would reduce synthesis to a text-merging exercise; passing the full investigation lets it assess reliability, identify contradictions, and weigh evidence. This matches old TARSy behavior (`investigation_history`). The DB queries are bounded (N agents × 1 query each) and happen once at synthesis time. Adds a `FormatInvestigationForSynthesis()` function to `pkg/agent/context/` that formats timeline events per execution, reusing the same event-type formatting logic as the existing `FormatInvestigationContext()`.

---

## Q3: Context Passed to Next Stage After Synthesis

**Status**: ✅ Decided — **Only synthesis result**

**Decision:** The synthesis `stageResult.finalAnalysis` replaces the parallel `stageResult` in `completedStages`. Subsequent stages see only the synthesized output, not the raw parallel results. Synthesis exists precisely to consolidate parallel findings into a single coherent view — passing both would be redundant, waste context window (N agent analyses + synthesis), and risk confusing the next agent with duplicate or conflicting information. If the next stage needs individual agent perspectives, the chain should be designed without synthesis on that parallel stage.

---

## Q4: Default Success Policy

**Status**: ✅ Decided — **Default to `any`**

**Decision:** Fix `StageService.UpdateStageStatus()` and the executor to default to `SuccessPolicyAny` when no policy is specified. This aligns with `tarsy.yaml.example`, old TARSy, and the common use case — LLM calls have inherent variability, and one agent timing out shouldn't fail a parallel investigation when others completed successfully. The current code treats nil as `"all"`, which is a Phase 5.1 artifact (parallel stages weren't supported, so the default was never exercised). Users who want strict behavior can explicitly set `success_policy: "all"`.

---

## Q5: Synthesis Failure Handling

**Status**: ✅ Decided — **Synthesis failure = stage failure (fail-fast)**

**Decision:** Synthesis failure triggers fail-fast in the chain loop, consistent with the Phase 5.1 chain execution pattern. The session's final status reflects the synthesis failure. Parallel agents' work is preserved in DB (timeline events, messages) but no synthesis result passes forward. Synthesis is a configured chain step that influences subsequent stages, not a convenience feature — if the user configured it, they expect synthesized results. This differs from executive summary (fail-open), which runs after everything and doesn't influence subsequent execution. Users can increase synthesis timeouts or use a more reliable model if failures are common.

---

## Q6: Synthesis Invocation — Automatic vs Explicit

**Status**: ✅ Decided — **Always on, no opt-out**

**Decision:** Synthesis always runs after every successful parallel stage. The `synthesis:` config block is optional and only controls the agent/strategy/provider — if omitted, defaults apply (SynthesisAgent, `synthesis` strategy, chain's LLM provider). There is no way to disable synthesis for a parallel stage. This simplifies implementation: no need to support a special "aggregate parallel final analyses" code path for the no-synthesis case. Every parallel stage produces a single synthesized `finalAnalysis` via the synthesis agent, which is what flows to the next stage. Matches old TARSy behavior. The `synthesis:` config block customizes the synthesis (e.g., use `synthesis-native-thinking` instead of `synthesis`, or a specific LLM provider) but synthesis itself is not optional.
