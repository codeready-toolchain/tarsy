---
name: db-migration-review
description: Review and clean up auto-generated database migration files after running make migrate-create. Identifies and removes unrelated Atlas schema drift such as dropped partial indexes, no-op index recreations, and other operations not related to the intended migration. Use when creating database migrations, running make migrate-create, or reviewing .up.sql files.
---

# DB Migration Review

## When to Apply

After every `make migrate-create` run, review the generated `.up.sql` file before committing. Atlas diffs the Ent schema against a dev database and frequently emits unrelated operations due to schema drift it cannot model.

## Known Atlas Drift Patterns

### 1. Dropping manually-managed partial indexes

Atlas cannot express `WHERE` clauses in index definitions. Several indexes are created manually in migrations and documented in Ent schema comments. Atlas sees them in the database but not in the Ent schema, so it generates `DROP INDEX` statements.

**Known partial indexes (must never be dropped):**

| Index | Table | Created in |
|-------|-------|------------|
| `agentexecution_stage_id_agent_index_top_level` | `agent_executions` | `20260225235224_add_orchestrator_sub_agent_fields.up.sql` |
| `agentexecution_parent_execution_id_agent_index_sub_agent` | `agent_executions` | `20260225235224_add_orchestrator_sub_agent_fields.up.sql` |

These enforce sub-agent ordering uniqueness with `WHERE parent_execution_id IS NULL / IS NOT NULL` clauses. The Ent schema at `ent/schema/agentexecution.go` documents this explicitly.

**Action:** Remove any `DROP INDEX` targeting these indexes.

### 2. No-op index drop + recreate

Atlas sometimes re-emits a `DROP INDEX` + `CREATE INDEX` pair for indexes that already exist with identical definitions. This commonly affects partial unique indexes like `sessionscore_session_id`.

**How to identify:** The `CREATE INDEX` statement after the `DROP` is identical to what already exists in a prior migration.

**Action:** Remove the entire drop + recreate pair.

### 3. Unrelated table modifications

Occasionally Atlas includes `ALTER TABLE` statements for tables unrelated to the migration's purpose (e.g., adding default values or constraints that were pending in the schema diff).

**Action:** If the operation is clearly unrelated to the migration's named purpose, remove it. If uncertain, investigate the Ent schema change that caused it.

## Review Checklist

After `make migrate-create NAME=<name>`:

1. **Read the generated `.up.sql` file end to end**
2. **For every `DROP INDEX` statement:** verify the index name relates to the migration's purpose. Cross-reference with `pkg/database/migrations/` to find where it was created. If it was created manually (not by Atlas) or with a `WHERE` clause, remove the `DROP`.
3. **For every `DROP INDEX` + `CREATE INDEX` pair on the same index:** compare the `CREATE` definition with the prior migration. If identical, remove both.
4. **For every `ALTER TABLE` statement:** verify it targets a table related to the migration's purpose.
5. **After edits:** run `make migrate-hash` to update `atlas.sum`.

## Adding New Manually-Managed Indexes

When creating a partial index (with `WHERE` clause) or any index Atlas cannot model:

1. Add it as raw SQL in the migration file.
2. Add a comment in the corresponding Ent schema `Indexes()` method documenting the index name and the migration file that creates it.
3. Add the index to the "Known partial indexes" table above so future reviews catch drift.
