# Referenced Stage ID — Design Questions

**Status:** All decisions made
**Related:** [Design document](referenced-stage-id-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Ent schema approach

The `referenced_stage_id` column points from one stage to another stage in the same table. Ent supports self-referential edges, but they add complexity. The alternative is a plain optional string field with manual FK management.

### Option A: Self-referential ent edge (chosen)

Define `referenced_stage_id` as a field backed by an ent self-referential edge (`referenced_stage` / `referencing_stages`). This follows the same pattern as `session`, `chat`, and `chat_user_message` edges on Stage.

```go
field.String("referenced_stage_id").Optional().Nillable()

edge.To("referencing_stages", Stage.Type)
edge.From("referenced_stage", Stage.Type).
    Ref("referencing_stages").
    Field("referenced_stage_id").
    Unique()
```

- **Pro:** Generated query helpers — `stage.QueryReferencedStage()`, eager loading via `.WithReferencedStage()`.
- **Pro:** Consistent with how every other FK in the Stage schema works (session, chat, chat_user_message all use edges).
- **Pro:** DB-level FK constraint generated automatically.
- **Con:** Self-referential edges in ent require ON DELETE behavior consideration (CASCADE would be dangerous — deleting an investigation stage would cascade-delete its synthesis stage; SET NULL is the safe choice and matches the optional semantics).
- **Con:** Ent codegen produces `referencing_stages` edge methods we'll likely never query from the "parent" direction.

**Decision:** Option A — Self-referential ent edge. Consistent with how every other FK in the Stage schema is modeled. ON DELETE SET NULL.

_Considered and rejected: Option B — plain string field with no edge (simpler but breaks FK-has-edge convention, requires manual FK constraint in migration SQL)._

---

## Q2: Backfill strategy for existing synthesis stages

Existing synthesis stages have no `referenced_stage_id`. Their relationship to the parent investigation stage is encoded only in the naming convention (`"{ParentName} - Synthesis"`). Should we backfill this data?

### Option A: Backfill in migration SQL (chosen)

Add a SQL UPDATE in the migration that pairs synthesis stages to their parent investigation stages using the existing name convention: trim `" - Synthesis"`, find the nearest preceding investigation stage with that name in the same session.

```sql
UPDATE stages s_synth
SET referenced_stage_id = (
    SELECT s_inv.stage_id
    FROM stages s_inv
    WHERE s_inv.session_id = s_synth.session_id
      AND s_inv.stage_type = 'investigation'
      AND s_inv.stage_name = REPLACE(s_synth.stage_name, ' - Synthesis', '')
      AND s_inv.stage_index < s_synth.stage_index
    ORDER BY s_inv.stage_index DESC
    LIMIT 1
)
WHERE s_synth.stage_type = 'synthesis';
```

- **Pro:** All existing data gets the FK — consumers can use a single code path immediately.
- **Pro:** Name-based fallback can be removed entirely from `buildChatContext`.
- **Pro:** SQL migration is a well-established pattern in this project (used for stage_type backfill in ADR-0004).
- **Con:** Migration SQL is more complex than a simple ALTER TABLE. Needs testing against real data.

**Decision:** Option A — Backfill in migration SQL. Same pattern as ADR-0004 stage_type backfill. Enables consumers to drop name-based pairing entirely.

_Considered and rejected: Option B — no backfill (requires indefinite dual code paths), Option C — application startup code (persistent startup code for one-time operation, rejected for same reason in ADR-0004 Q5)._

---

## Q3: Name-based fallback after migration

After adding the FK, should `buildChatContext` keep the name-based backward scan as a fallback for stages where `referenced_stage_id` is NULL?

### Option A: FK-only, no fallback (chosen)

After migration (with backfill), trust that all synthesis stages have the FK set. Remove the name-based pairing code entirely.

- **Pro:** Simpler code — single code path.
- **Pro:** Removes the string convention dependency from runtime code.
- **Con:** If any synthesis stages were missed by backfill, they silently lose their pairing.

**Decision:** Option A — FK-only, no fallback. Backfill (Q2) covers all existing data, so the name-based code path is dead code. Removing it keeps the consumer simple and eliminates the string convention from runtime.

_Considered and rejected: Option B — FK-first with name fallback (defensive but maintains dead code, masks backfill bugs rather than surfacing them)._

---

## Q4: API/WS exposure of `referenced_stage_id`

Should the `referenced_stage_id` field be included in REST API responses and WebSocket events?

### Option A: Expose in API responses (chosen)

Add `referenced_stage_id` to `StageOverview` (session detail API) and `StageStatusPayload` (WS events).

- **Pro:** Frontend could show stage relationships (e.g. link synthesis to its parent in the timeline).
- **Pro:** Consistent — other stage fields like `stage_type` are already exposed.
- **Con:** No current frontend consumer. Adding it to the API is easy; removing it later is harder.

**Decision:** Option A — Expose in API responses. Single additive field, near-zero cost, avoids a future API change. Same reasoning as exposing `stage_type` in ADR-0004 Q3.

_Considered and rejected: Option B — internal only (smaller API surface but requires future API change if frontend ever wants to show relationships)._

---

## Q5: Chat stage scoping — include or defer?

The original proposal mentions that chat stages could optionally set `referenced_stage_id` to scope a follow-up question to a specific investigation stage. This would enable "ask about this stage" UX in the dashboard. Should this be included in this proposal?

### Option B: Not applicable — chat stages are session-scoped (chosen)

Chat stages operate on the entire session context, not individual stages. There is no current use case for scoping a chat to a specific stage. `referenced_stage_id` will be NULL for chat stages.

- **Pro:** Keeps this proposal tightly scoped to the cleanup it was designed for.
- **Pro:** Matches the existing chat model — chats are session-level follow-ups.

**Decision:** Option B — Chat stages remain session-scoped. `referenced_stage_id` is only used for synthesis→investigation pairing. No chat scoping in this proposal.

_Considered and rejected: Option A — stage-scoped chat (doesn't match the current chat model; chats are session-level, not stage-level)._
