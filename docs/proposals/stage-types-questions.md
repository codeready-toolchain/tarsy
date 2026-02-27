# Stage Types — Design Questions

**Status:** Open — decisions pending
**Related:** [Design document](stage-types-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where should `StageType` be defined?

`AgentType` and `LLMBackend` live in `pkg/config/enums.go` because they are config-layer concepts — they flow from YAML config through resolution into the DB. `StageType` is different: it is never user-configured. It is assigned internally by executors at stage creation time.

### Option C: Use ent-generated constants only (no separate Go type)

- **Pro:** Zero duplication. Ent already generates `stage.StageType*` constants when you add an enum field to the schema.
- **Pro:** No new file or package needed.
- **Pro:** Consistent with how `stage.Status`, `stage.ParallelType`, and `stage.SuccessPolicy` already work.
- **Con:** Ent-generated types use `stage.StageType` (the ent package), which ties referencing packages to the ent schema package.
- **Con:** Harder to add methods (like `IsValid()`) to ent-generated types.

**Decision:** Option C — use ent-generated constants only. No separate Go type needed.

_Considered and rejected: Option A (`pkg/config/enums.go` — `StageType` is not a config concept, would imply configurability), Option B (new `pkg/stage/types.go` — introduces a new package pattern with no precedent, duplicates ent-generated constants)._

---

## Q2: Should we add a DB index on `stage_type`?

### Option B: No index

- **Pro:** Avoids unnecessary index maintenance overhead on writes.
- **Pro:** Current query patterns don't benefit from it — stages are always loaded per-session, typically 1-5 per session.
- **Con:** If future queries need `WHERE stage_type = X` across sessions, they'd need a migration to add the index.

**Decision:** Option B — no index. Can be added later if a use case arises.

_Considered and rejected: Option A (single-column index — low cardinality makes B-tree indexes ineffective; no current query filters by `stage_type` alone)._

---

## Q3: Should `stage_type` be added to `StageStatusPayload`?

### Option A: Add `stage_type` to `StageStatusPayload`

- **Pro:** Frontend receives stage type without an additional REST call.
- **Pro:** Consistent — if the REST API includes it, the WS event should too.
- **Pro:** WS payload is self-describing.
- **Con:** Requires changing `publishStageStatus` signature to accept a `stageType` parameter, touching 6 call sites. Change is mechanical.

**Decision:** Option A — add `stage_type` to `StageStatusPayload` and update `publishStageStatus` accordingly.

_Considered and rejected: Option B (omit from WS payload — creates REST/WS inconsistency; frontend re-fetch is an implementation detail that could change)._

---

## Q4: Should synthesis stage pairing also use `stage_type`?

The `buildChatContext` method in `chat_executor.go:452-464` pairs synthesis stages with their parent investigation stages using suffix stripping + backward name scanning. With `stage_type`, the _identification_ can use the type field, but the _pairing_ (which investigation stage does this synthesis belong to) still needs the name convention.

### Option A: Replace only the identification, keep name-based pairing

- **Pro:** Minimal change. Replace `strings.HasSuffix(stg.StageName, " - Synthesis")` with `stg.StageType == stage.StageTypeSynthesis`. Pairing logic unchanged.
- **Pro:** Pairing by name is correct — synthesis stage name is always derived from the parent in `executor_synthesis.go`.
- **Con:** Still relies on the naming convention for pairing.

**Decision:** Option A — replace identification with type check, keep name-based pairing for now.

_Considered and rejected: Option B (`parent_stage_id` FK — valid future improvement but scope creep, orthogonal to stage types), Option C (pair by adjacency — fragile, not safer than name-based)._

---

## Q5: How should the backfill migration be handled?

Ent generates migrations automatically when the schema changes. The `DEFAULT 'investigation'` covers new rows and existing investigation stages. But existing synthesis and chat stages need explicit backfill.

### Option C: Embed backfill SQL in ent migration

- **Pro:** Single migration step — ent migration includes both DDL and DML.
- **Pro:** Standard approach — ent/Atlas supports custom SQL in generated migrations.
- **Con:** Requires editing the generated migration file (or adding a custom migration).

**Decision:** Option C — embed backfill `UPDATE` statements in the same ent migration that adds the column.

_Considered and rejected: Option A (separate migration file — requires two steps or manual embedding), Option B (Go code at startup — one-time logic that lives forever in the codebase)._

---

## Q6: Should this be a single PR or phased?

### Option A: Single PR

- **Pro:** Less overhead — no intermediate states, no partial feature.
- **Pro:** The total change is small: ~15 files touched, mostly adding one field to structs and one parameter to function calls.
- **Pro:** Easier to review as a coherent whole — tightly coupled changes form one feature.
- **Con:** Larger diff (but still manageable).

**Decision:** Option A — single PR. The change is small and tightly coupled.

_Considered and rejected: Option B (two PRs — unnecessary overhead for this size), Option C (five PRs — trivially small individual PRs, confusing intermediate states)._
