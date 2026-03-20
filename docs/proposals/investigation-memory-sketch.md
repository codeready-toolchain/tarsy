# Investigation Memory — Cross-Session Learning for TARSy

**Status:** Sketch complete — all questions decided, see [investigation-memory-questions.md](investigation-memory-questions.md)

## Problem

TARSy investigations are isolated. Every investigation starts from a blank slate — the same type of alert on the same service produces the same exploratory steps, the same dead ends, and the same "discoveries" of environment-specific facts that a previous investigation already uncovered. There is no mechanism for institutional knowledge to accumulate across investigations.

This manifests as:

- **Repeated dead ends** — the agent queries a metric that doesn't exist under that name in this environment, every time
- **Missed shortcuts** — a senior SRE would check the database connection pool first for this type of alert, but the agent doesn't know that
- **Lost baselines** — "200 errors/hr during batch processing is normal for this service" is discovered and forgotten
- **No learning from quality feedback** — investigations scored poorly (or reviewed by humans) don't improve future investigations

TARSy already has the raw material: full investigation timelines in the database, automated quality scoring (0-100 with failure tags and tool improvement reports), and a human review workflow (claim → resolve with notes). None of this feeds back into future investigations.

## Goal

Enable TARSy to accumulate and apply institutional knowledge from past investigations, so that each investigation benefits from what was learned before. The system should learn both **what works** (effective investigation strategies, environment-specific facts) and **what doesn't** (dead ends, common mistakes, misleading patterns).

## How It Relates to the Existing System

### Signals already available

| Signal | Source | What it tells us |
|--------|--------|------------------|
| **Score (0-100)** | `SessionScore.total_score` | Overall investigation quality |
| **Score analysis** | `SessionScore.score_analysis` | Detailed critique of what went well/poorly |
| **Failure tags** | `SessionScore.failure_tags` | Standardized failure patterns (`premature_conclusion`, `missed_available_tool`, etc.) |
| **Tool improvement report** | `SessionScore.tool_improvement_report` | What tools were misused, missed, or need improvement |
| **Human quality rating** | `SessionReviewActivity.quality_rating` | `accurate` / `partially_accurate` / `inaccurate` — explicit investigation quality (see Q4 prerequisite) |
| **Investigation feedback** | `SessionReviewActivity.investigation_feedback` | Free-text explaining why the investigation was good or bad |
| **Final analysis** | `AlertSession.final_analysis` | The investigation's conclusion |
| **Executive summary** | `AlertSession.executive_summary` | Brief summary |
| **Investigation timeline** | `TimelineEvent` rows | Full tool calls, reasoning, findings |
| **Alert metadata** | `AlertSession.alert_type`, `chain_id`, `alert_data` | Categorization and context |

### Integration points

Memory injection fits naturally into the existing prompt hierarchy:

```
Tier 1:  General SRE instructions (generalInstructions)
Tier 2:  MCP server instructions (per-server from registry)
Tier 2.5: Required skill content (from SkillRegistry)
Tier 2.6: On-demand skill catalog
Tier 3:  Custom agent instructions
──────── NEW ────────
Tier 4:  Investigation memory (from past investigations)
```

The user message already contains: alert data → runbook → chain context → analysis task.

Memory is injected via a **hybrid approach** (see [Q3 decision](investigation-memory-questions.md)):

1. **Auto-inject** the top 3-5 most semantically similar memories into the system prompt as a new Tier 4 section ("Lessons from Past Investigations"). Retrieved by pgvector cosine similarity against the current alert context — no rigid scope filtering, just "which past learnings are most relevant to what I'm looking at?" (see [Q6 decision](investigation-memory-questions.md)). This solves the cold-start problem — the agent always has critical context without needing to search.
2. **Provide a `recall_past_investigations` tool** (pseudo-MCP, similar to `load_skill`) for the agent to search for deeper context when the auto-injected briefing triggers further questions.

The auto-injected set is the briefing; the tool is the library. Memories are framed as hints to consider, not rules to follow — preserving the LLM's investigative creativity.

**Chat sessions** (follow-up conversations about completed investigations) get the `recall_past_investigations` tool but no auto-injection. The human is driving with specific questions, so there's no cold-start problem — but "has this happened before?" queries benefit from tool access at near-zero incremental cost (the tool already exists for investigations).

### What runs after an investigation completes

Currently, after a session reaches terminal status:

1. **Executive summary stage** — single-shot LLM summarization
2. **Scoring stage** — multi-turn LLM evaluation producing score, analysis, failure tags, tool improvement report
3. **Review workflow** — human claims and resolves (asynchronous, may happen hours/days later)

Memory extraction is **embedded in the scoring stage** as a third LLM turn — see [Q5 decision](investigation-memory-questions.md). It runs for every scored investigation (see [Q8 decision](investigation-memory-questions.md)).

## Key Concepts

### Memory taxonomy

Based on research into production agent memory systems (ACE/ICLR 2026, Mem0, RubixKube, LangMem SDK), TARSy memories fall into three categories:

**Semantic memories** — Infrastructure facts discovered during investigations:
- "Service X runs on namespace Y in cluster Z"
- "The monitoring endpoint for this service uses non-standard path /healthz/ready"
- "Normal error rate for batch-processor during 2-4am window is ~200/hr"

**Episodic memories** — What happened in past investigations:
- "Alert type X on service Y — root cause was connection pool exhaustion, resolved by scaling pool from 50 to 100"
- "Previous investigation scored 85/100, agent correctly identified memory leak"

**Procedural memories** — Investigation strategies and anti-strategies:
- "For API latency alerts on this service, check DB connection pool before upstream dependencies"
- "Tool `monitoring.query_range` returns empty for this cluster — use `monitoring.instant_query` instead"
- "AVOID: querying metric `container_memory_rss` — doesn't exist in this monitoring setup, use `container_memory_working_set_bytes`"

These categories are stored as an explicit enum (`semantic`, `episodic`, `procedural`) in the schema. Categories enable differentiated display in the dashboard and lifecycle rules (e.g., procedural memories are more durable than episodic), but retrieval is semantic-first (Q6) — categories don't hard-filter, they provide context alongside the memory content. See [Q2 decision](investigation-memory-questions.md).

### What makes a memory "good" vs "bad"

This is the core challenge. A memory must not just capture what happened — it must capture whether what happened was **worth repeating** or **worth avoiding**.

**Automated signals (from scoring):**
- Score 0-100 determines the score range (high = positive patterns, low = anti-patterns)
- Failure tags (`premature_conclusion`, `wrong_conclusion`, etc.) identify specific anti-patterns
- Tool improvement report identifies tool usage mistakes to avoid

**Human signals (from redesigned review workflow):**
- `quality_rating`: `accurate` / `partially_accurate` / `inaccurate` — unambiguous investigation quality
- `investigation_feedback`: free-text explaining *why* the investigation was right or wrong

**The memory itself is tagged with valence** — whether it represents a pattern to repeat or a pattern to avoid. This prevents the agent from learning the wrong lesson from a bad investigation.

Memory quality is determined in two phases (see [Q4 decision](investigation-memory-questions.md)):
1. **Immediate**: Reflector runs after scoring, using score + failure tags to set initial valence and confidence
2. **Refinement**: When a human reviews, their `quality_rating` adjusts confidence (`accurate` boosts, `inaccurate` reduces/flips valence), and `investigation_feedback` can trigger targeted memory updates

**Prerequisite: review workflow redesign.** The current `actioned`/`dismissed` resolution reasons are ambiguous — they describe what the human did about the alert, not whether the investigation was good. This sketch assumes they are replaced with `quality_rating` (investigation accuracy) + `resolution_summary` (what the human did about the alert) + `investigation_feedback` (why the investigation was good/bad).

### Memory scoping and retrieval

Memories are stored with scope metadata for context, but retrieval is **semantic-first** — driven by pgvector cosine similarity, not rigid scope filtering (see [Q6 decision](investigation-memory-questions.md)).

Stored scoping dimensions (metadata, not hard filters):

- **Alert type** — from `AlertSession.alert_type` (e.g., "Kubernetes - Multiple agents")
- **Chain ID** — from `AlertSession.chain_id` (which investigation pipeline was used)
- **Service / component** — extracted from alert data (the specific service under investigation)
- **Cluster / environment** — extracted from alert data or MCP server context

At retrieval time, the current alert context is embedded and the most semantically similar memories are returned, regardless of scope. Scope metadata provides a minor soft boost (same-service memories rank slightly higher) but never hard-filters. This lets cross-cutting knowledge ("Prometheus metrics lag 5 min on Mondays") surface for any alert where it's relevant, not just the service it was learned from.

The design philosophy: **memory is a light touch** — complementary hints for specific and repeating situations, not a playbook that constrains the LLM's natural investigative ability. Noise is controlled by the auto-inject cap (3-5 memories, Q3), not by complex filtering logic.

### The Reflector: extracting memories from completed investigations

Inspired by ACE's Reflector/Curator loop. After the scoring turns complete, the Reflector (third turn in the same scoring call, per Q5) analyzes the investigation and extracts discrete memory entries.

The Reflector has access to:
- The full investigation timeline (already loaded by the scoring stage)
- The scoring analysis and failure tags (from the preceding scoring turns)
- The tool improvement report
- Alert metadata (type, service, environment)
- Existing relevant memories found by pgvector similarity (for dedup, see [Q7 decision](investigation-memory-questions.md))

The Reflector produces structured memory entries, each with:
- **Content** — the actual knowledge (a sentence or short paragraph)
- **Category** — semantic / episodic / procedural
- **Valence** — positive (repeat this) / negative (avoid this) / neutral (informational)
- **Scope** — alert type, chain, service, environment
- **Confidence** — derived from investigation score and evidence quality
- **Source** — session ID and stage that produced this memory

Memory extraction is **embedded in the scoring stage** as a third LLM turn (see [Q5 decision](investigation-memory-questions.md)). The scorer already has the full investigation context and produces the critique that extraction needs — adding "what should be remembered?" is the most token-efficient approach with zero new infrastructure.

### Memory lifecycle

**Creation:** Reflector extracts memories after scoring completes.

**Reinforcement:** When a similar memory is extracted from a subsequent investigation, the existing entry is updated: `seen_count` incremented, `confidence` adjusted, `last_seen` updated.

**Decay:** Memories not reinforced within a configurable window lose confidence. Memories about decommissioned services or changed infrastructure should eventually be pruned.

**Human curation:** Admins can view memories in session detail pages and manage them via API (see [Q9 decision](investigation-memory-questions.md)). High-confidence memories can be "promoted" to permanent instructions via API (similar to "promote to agent_instructions.md" pattern, but promoting to skills or custom_instructions in TARSy's config).

**Deduplication:** Handled in-prompt by the Reflector itself (see [Q7 decision](investigation-memory-questions.md)). Existing memories (found by pgvector similarity, consistent with Q6) are included in the Reflector's context during extraction. The Reflector decides in one pass: reinforce existing memories if new evidence matches, add genuinely novel memories, and deprecate outdated ones. No separate dedup logic needed.

### Human feedback integration

The redesigned review workflow provides three fields at resolve time:

| Field | Purpose | Memory relevance |
|-------|---------|-----------------|
| `quality_rating` | `accurate` / `partially_accurate` / `inaccurate` | **Primary signal** — directly maps to memory confidence adjustment |
| `resolution_summary` | What the human did about the alert (free text) | Not used for memory — historical record only |
| `investigation_feedback` | Why the investigation was good/bad (free text) | **Richest signal** — can trigger targeted memory updates |

This separation is important: `resolution_summary` is about the **alert** ("I scaled the pod", "It's a false positive, I did nothing"), while `investigation_feedback` is about the **investigation** ("TARSy mistook X for Y", "Great root cause analysis").

## Rough Architecture

```
┌──────────────────────────────────────────────────────────┐
│                   Investigation Flow                     │
│                                                          │
│  Alert → Chain Stages → Final Analysis → Exec Summary    │
│                                                    │     │
│                                              ┌─────▼──┐  │
│                                              │Scoring │  │
│                                              └────┬───┘  │
│                                                   │      │
│                                      ┌────────────▼───┐  │
│                                      │  Reflector     │  │
│                                      │  (extract      │  │
│                                      │   memories)    │  │
│                                      └───────┬────────┘  │
└──────────────────────────────────────────────┼───────────┘
                                               │
                                               ▼
                                    ┌─────────────────────┐
                                    │   Memory Store      │
                                    │   (PostgreSQL)      │
                                    │                     │
                                    │  - content          │
                                    │  - category         │
                                    │  - valence          │
                                    │  - scope metadata   │
                                    │  - confidence       │
                                    │  - seen_count       │
                                    │  - embedding (vec)  │
                                    └─────────┬───────────┘
                                              │
                              ┌───────────────┼───────────────┐
                              │               │               │
                              ▼               ▼               ▼
                      ┌──────────┐    ┌───────────┐    ┌──────────┐
                      │ Prompt   │    │ Session   │    │ Human    │
                      │ Injection│    │ Detail +  │    │ Review   │
                      │ (Tier 4) │    │ API       │    │ Feedback │
                      └──────────┘    └───────────┘    └──────────┘
```

Storage uses **PostgreSQL with pgvector** — semantic-first retrieval driven by cosine similarity on embeddings, with scope metadata as a soft signal. No separate vector DB, no rigid scope hierarchy. See [Q1](investigation-memory-questions.md) and [Q6](investigation-memory-questions.md) decisions.

## What Is Out of Scope

- **Real-time memory formation during investigation** — this sketch covers post-investigation learning; the `recall_past_investigations` tool (Q3) reads existing memories during investigation but does not write new ones mid-investigation
- **Cross-deployment memory sharing** — memories are per-deployment (single PostgreSQL instance)
- **Automated remediation learning** — memory focuses on investigation quality, not action execution outcomes
- **Fine-tuning or model adaptation** — memory operates at the prompt/context level, not the model level
- **Memory extraction from chat sessions** — memory extraction only runs after investigations (via the Reflector in the scoring stage); chat sessions can read memories via the tool but don't produce new ones
