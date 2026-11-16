---
name: db-migration-review
description: Review and clean up auto-generated database migration files after running make migrate-create. Identifies and removes unrelated Atlas schema drift such as dropped partial indexes, no-op index recreations, and other operations not related to the intended migration. Use when creating database migrations, running make migrate-create, or reviewing .up.sql files.
---

# DB Migration Review

Atlas diffs the Ent schema against a dev database and often emits operations unrelated to the change, because it can't model some indexes. Review every generated `.up.sql` in `pkg/database/migrations/` before it is committed.

## Indexes Atlas can't model (never drop)

| Index | Table | Created in |
|-------|-------|------------|
| `agentexecution_stage_id_agent_index_top_level` | `agent_executions` | `20260225235224_add_orchestrator_sub_agent_fields.up.sql` |
| `agentexecution_parent_execution_id_agent_index_sub_agent` | `agent_executions` | `20260225235224_add_orchestrator_sub_agent_fields.up.sql` |
| `idx_alert_sessions_alert_data_gin` | `alert_sessions` | `CreateGINIndexes` in `pkg/database/migrations.go` |
| `idx_alert_sessions_final_analysis_gin` | `alert_sessions` | `CreateGINIndexes` in `pkg/database/migrations.go` |
| `idx_timeline_events_content_gin` | `timeline_events` | `CreateGINIndexes` in `pkg/database/migrations.go` |
| `idx_alert_sessions_labels_gin` | `alert_sessions` | `CreateGINIndexes` in `pkg/database/migrations.go` |

The first two are partial unique indexes (`WHERE parent_execution_id IS [NOT] NULL`) documented in `ent/schema/agentexecution.go`. The GIN indexes are created at startup.

## Checklist

1. Wrap the file in `BEGIN;` / `COMMIT;` if Atlas didn't. A failed migration then rolls back cleanly and `recoverDirtyMigration` (`pkg/database/client.go`) can retry on the next deploy. Exception: statements that can't run in a transaction (e.g. `CREATE INDEX CONCURRENTLY`) go in their own migration file without `BEGIN`/`COMMIT`.
2. Remove any `DROP INDEX` for an index in the table above, or any other index created by hand or with a `WHERE` clause (search `pkg/database/migrations/` for where it was created).
3. Remove `DROP INDEX` + `CREATE INDEX` pairs whose `CREATE` matches the existing definition in an earlier migration (common with `sessionscore_session_id`).
4. Remove `ALTER TABLE` statements on tables unrelated to the migration's purpose. If unsure, find the Ent schema change that caused it.
5. Run `make migrate-hash` to update `atlas.sum`.

## Adding a new index Atlas can't model

Write it as raw SQL in the migration, add a comment naming it and its migration file in the Ent schema's `Indexes()`, and add a row to the table above.
