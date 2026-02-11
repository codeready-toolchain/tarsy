# Phase 5.2: Parallel Execution — Open Questions

**Status**: Pending review
**Related**: `docs/phase5.2-parallel-execution-design.md`

---

## Q1: Synthesis Placement — Separate Stage vs AgentExecution Within Parallel Stage

**Context**: After parallel agents complete, synthesis needs its own execution tracked somewhere. Two options for where in the data model it lives.

### Option A: Separate Stage Record (recommended)

Synthesis creates its own `Stage` DB record (e.g., "Investigation - Synthesis") with a single `AgentExecution` for the synthesis agent.

**Pros**:
- No changes to `StageService.UpdateStageStatus()` — aggregation logic remains clean
- Clear separation: parallel Stage status reflects only parallel agents; synthesis Stage reflects synthesis
- Dashboard shows two distinct stages — user sees the investigation and synthesis as separate steps
- `dbStageIndex` increments naturally; `stage_index` uniqueness constraint satisfied
- Consistent with old TARSy (synthesis gets its own stage_execution record at a new index)

**Cons**:
- The "extra" Stage doesn't appear in `chain.Stages` config — it's auto-generated at runtime
- `stage_index` in DB diverges from config stage position when synthesis stages are inserted (e.g., config stage 1 → DB stages 1+2, config stage 2 → DB stage 3)
- Session progress events (`current_stage_index`) count synthesis stages, so total stage count is higher than the config suggests

### Option B: AgentExecution Within Parallel Stage

Synthesis is another `AgentExecution` within the same parallel `Stage`, distinguished by metadata (e.g., `agent_name = "SynthesisAgent"`, special `agent_index`).

**Pros**:
- Single Stage record per config stage — `stage_index` matches config
- Simpler chain loop (no synthesis stage insertion)

**Cons**:
- `UpdateStageStatus()` must be modified to exclude synthesis from success policy aggregation (or synthesis status conflates with parallel agent status)
- The synthesis `AgentExecution` would be "pending" during parallel execution, preventing `UpdateStageStatus()` from finalizing until we add special-case logic
- Semantically muddled: synthesis is a fundamentally different operation (post-processing) from parallel investigation

**Recommendation**: Option A. The separation is cleaner, requires fewer changes to existing code, and matches the mental model (investigate in parallel, then synthesize). The `stage_index` divergence is a minor cosmetic concern and consistent with old TARSy.

---

## Q2: Context Passed to Synthesis Agent

**Context**: Synthesis needs to see the parallel agents' findings to produce a unified analysis. The question is how much detail to provide.

### Option A: Final analyses with metadata (recommended)

Pass each agent's `final_analysis` text, annotated with agent name, strategy, provider, and status. Built in-memory from `agentResult` structs — no DB queries.

Format:
```
#### Agent 1: KubernetesAgent (native-thinking, gemini-2.5-pro)
**Status**: completed

[agent 1's final_analysis text]
```

**Pros**:
- Consistent with Phase 5.1's "only pass final_analysis between stages" pattern
- No additional DB queries (in-memory from execution results)
- Final analysis is the agent's own synthesis of its investigation — purpose-built for handoff
- Keeps synthesis context window manageable

**Cons**:
- Synthesis cannot see intermediate reasoning, tool calls, or raw observations
- May miss nuances that the agent's final analysis didn't capture
- Old TARSy passed full `investigation_history` (richer context)

### Option B: Full conversation messages

Query each parallel agent's `Message` records from DB and format the entire conversation (user/assistant/tool messages, excluding system prompt) for synthesis.

**Pros**:
- Synthesis has maximum information to work with
- Matches old TARSy behavior (`investigation_history` = full conversation minus system messages)
- Can catch insights that agents didn't include in final analysis

**Cons**:
- Requires DB queries per agent (N queries for N parallel agents)
- Large context window consumption (each agent's full conversation could be 10K+ tokens)
- For 3 agents with 20 iterations each, context could exceed model limits
- Introduces DB dependency in the context-building path (currently all in-memory)
- Breaks the "lazy context building, no DB queries" pattern established in Phase 5.1

### Option C: Hybrid — final analyses + key observations

Pass final analyses (Option A) plus a summary of tool calls made (tool name + result snippet, not full content). Middle ground between richness and size.

**Pros**:
- Richer than Option A without the full weight of Option B
- Gives synthesis visibility into what tools were used

**Cons**:
- Requires DB queries for tool call data (timeline events or messages)
- More complex formatting logic
- Unclear how much value tool call summaries add over the final analysis

**Recommendation**: Option A. Start with final analyses only. This is consistent with the existing architecture and avoids DB queries in the critical path. If synthesis quality needs improvement, enhancing to Option C is backward-compatible and can be done in a future iteration. The old TARSy approach (Option B) was designed for a Python codebase with different memory/async patterns; the Go architecture benefits from keeping context building in-memory.

---

## Q3: Context Passed to Next Stage After Synthesis

**Context**: When a parallel stage with synthesis completes, the next stage needs context. Should it see only the synthesis output, or both the parallel results and synthesis?

### Option A: Only synthesis result (recommended)

The synthesis `stageResult.finalAnalysis` replaces the parallel `stageResult.finalAnalysis` in `completedStages`. The next stage sees only the synthesized output.

**Pros**:
- Synthesis is the authoritative consolidated view — its purpose is to replace the raw parallel results with a unified analysis
- Keeps context smaller (one synthesis instead of N agent analyses)
- Avoids confusing the next agent with both raw and processed versions of the same data
- Matches the mental model: synthesis produces the "final word" on the parallel stage

**Cons**:
- Next stage loses access to individual agent perspectives
- If synthesis missed something, the next stage can't recover it

### Option B: Both parallel and synthesis results (old TARSy approach)

Both the parallel stage result and synthesis result appear in `completedStages`. The next stage sees all individual agent analyses AND the synthesis.

**Pros**:
- Maximum information available to next stage
- Next stage can cross-reference synthesis with individual agents
- Matches old TARSy behavior

**Cons**:
- Redundant — synthesis already incorporates all parallel findings
- Significantly larger context (N agent analyses + synthesis, often 2-3x the information)
- Risk of confusing the next agent with duplicate/conflicting information
- Complicates `buildStageContext()` formatting (need to handle parallel results differently)

**Recommendation**: Option A. Synthesis exists precisely to consolidate parallel results into a single coherent view. Passing both defeats the purpose and wastes context window. If the next stage needs individual perspectives, the chain should be designed without synthesis on that parallel stage.

---

## Q4: Default Success Policy

**Context**: There's a discrepancy between the configured default and the code default.

| Source | Default |
|--------|---------|
| `tarsy.yaml.example` (`defaults.success_policy`) | `"any"` |
| Old TARSy | `SuccessPolicy.ANY` |
| `StageService.UpdateStageStatus()` (nil check) | Treats nil as `"all"` |

### Option A: Default to `any` (recommended)

Update the code to default to `SuccessPolicyAny` when no policy is specified. Aligns with:
- The example config
- Old TARSy behavior
- The common use case (parallel agents for diverse perspectives — one failure shouldn't block everything)

### Option B: Default to `all`

Keep the current `UpdateStageStatus()` behavior. More strict by default — all agents must succeed.

**Pros**: Stricter validation, no silent partial failures

**Cons**: Breaks from old TARSy, inconsistent with documented default, makes replica stages fragile (one LLM timeout kills the stage)

**Recommendation**: Option A. Fix `UpdateStageStatus()` and the executor to default to `any`. This is the safer default for production use — LLM calls have inherent variability, and one agent timing out shouldn't fail a parallel investigation when others completed successfully. Users who want strict behavior can explicitly set `success_policy: "all"`.

---

## Q5: Synthesis Failure Handling

**Context**: Synthesis is a post-processing step after parallel agents succeed. If synthesis fails, what happens?

### Option A: Synthesis failure = stage failure (recommended)

Synthesis failure triggers fail-fast in the chain loop. The session's final status reflects the synthesis failure. The parallel agents' work is preserved in DB (timeline events, messages) but no synthesis result passes forward.

**Pros**:
- Consistent with fail-fast chain execution (Phase 5.1 pattern)
- No ambiguity about what the next stage receives
- Matches old TARSy behavior (synthesis failure stops chain)
- Clear signal to the user that synthesis didn't complete

**Cons**:
- Parallel agents' work is "wasted" (investigation happened but can't be used)
- Could be retried if synthesis failures are transient (LLM timeout)

### Option B: Fail-open (fall back to parallel aggregate)

If synthesis fails, use the parallel aggregate final_analysis (`buildParallelFinalAnalysis()`) instead. Log the synthesis failure but continue the chain.

**Pros**:
- Preserves parallel agents' work
- Investigation can continue even without synthesis
- Matches "investigation-availability-first" philosophy (like executive summary fail-open)

**Cons**:
- Next stage receives unsynthesized results (potentially lower quality)
- Inconsistent with fail-fast pattern (why does synthesis get special treatment?)
- Creates an implicit contract where synthesis is "optional" even though it was configured
- Executive summary is different — it's a convenience feature at the very end; synthesis is a critical chain step that influences subsequent stages

**Recommendation**: Option A. Synthesis is a configured chain step, not a convenience feature. If the user configured synthesis, they expect synthesized results to flow to the next stage. Unlike executive summary (which runs after everything and doesn't influence subsequent execution), synthesis failure mid-chain means the chain can't produce its intended quality of output. Users can increase synthesis timeouts or use a more reliable model if synthesis failures are common.

---

## Q6: Synthesis Invocation — Automatic vs Explicit

**Context**: Old TARSy always invokes synthesis after any successful parallel stage (with default SynthesisAgent config if none specified). New TARSy could require explicit configuration.

### Option A: Explicit only (recommended)

Synthesis runs only when `synthesis:` is present in the stage config. No synthesis config → parallel agents' aggregate final_analysis passes forward.

```yaml
# Synthesis runs:
stages:
  - name: "investigation"
    agents: [...]
    synthesis:
      agent: "SynthesisAgent"
      iteration_strategy: "synthesis-native-thinking"

# Synthesis does NOT run:
stages:
  - name: "investigation"
    agents: [...]
```

**Pros**:
- Explicit is better than implicit — user controls exactly what happens
- No surprise LLM calls (synthesis costs tokens)
- Simpler to reason about chain behavior
- Parallel stages without synthesis are valid (e.g., parallel data collection feeding a sequential analysis stage)

**Cons**:
- Breaks from old TARSy behavior (always auto-invoked)
- User might forget to add synthesis and get raw aggregate results

### Option B: Automatic with opt-out

Synthesis always runs after parallel stages using defaults if not configured. Opt-out via explicit `synthesis: null` or `synthesis: disabled`.

**Pros**:
- Matches old TARSy behavior
- Parallel results are always synthesized (higher quality output)

**Cons**:
- Surprise LLM calls when user didn't expect synthesis
- Implicit behavior is harder to debug
- Need a way to opt-out (new config mechanism)
- "Magic" default behavior that's not visible in config

**Recommendation**: Option A. Explicit configuration aligns with the new TARSy philosophy of being clear about what happens in the chain. The config example (`tarsy.yaml.example`) already shows `synthesis:` configured on parallel stages, establishing the pattern. Users who want synthesis will configure it; those who don't won't get surprise LLM calls.
