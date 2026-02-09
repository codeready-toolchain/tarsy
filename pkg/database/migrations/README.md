# Database Migrations

This directory contains SQL migration files managed by [Atlas CLI](https://atlasgo.io/) and applied at runtime by [golang-migrate](https://github.com/golang-migrate/migrate).

## How It Works

1. **Schema source of truth**: Ent schemas in `ent/schema/*.go` define the desired database state.
2. **Atlas generates diffs**: `atlas migrate diff` compares the current migration directory against the Ent schema and produces a new `.sql` file with the required DDL.
3. **Embedded at build time**: All `.sql` files in this directory are embedded into the Go binary via `go:embed`.
4. **Applied on startup**: When the application starts, `runMigrations()` in `pkg/database/client.go` uses golang-migrate to apply any pending migrations automatically.
5. **GIN indexes**: Full-text search GIN indexes on `alert_sessions` are created separately by `CreateGINIndexes()` (in `pkg/database/migrations.go`) after migrations run. These use `CREATE INDEX IF NOT EXISTS` and live in Go code rather than migration files because Atlas would otherwise try to drop them in subsequent diffs (Ent doesn't model custom SQL indexes).

## Migration File Format

Files follow the Atlas naming convention:

```text
{timestamp}_{name}.sql
```

Example:
- `20260209015211_initial_schema.sql`

The `atlas.sum` file is a checksum file that Atlas uses to verify migration integrity. It is regenerated automatically by `atlas migrate hash` whenever a migration is created or modified.

## Adding a New Migration

### Prerequisites

- **Atlas CLI** -- install from [atlasgo.io](https://atlasgo.io/getting-started#installation):
  ```bash
  curl -sSf https://atlasgo.sh | sh
  ```
- **PostgreSQL running** -- the dev database must be accessible for Atlas to compute diffs:
  ```bash
  make db-start
  ```

### Step-by-step

1. **Edit the Ent schema** -- make your changes in `ent/schema/*.go`.

2. **Regenerate Ent code** -- this updates the generated Go types and validators:
   ```bash
   make ent-generate
   ```

3. **Ensure the dev database is running and clean** -- Atlas needs a live database to compute the diff:
   ```bash
   make db-start
   # If the DB has stale state from manual testing:
   echo "y" | make db-reset
   ```

4. **Generate the migration** -- Atlas diffs the migration directory against the Ent schema and writes a new `.sql` file:
   ```bash
   make migrate-create NAME=describe_your_change
   ```

5. **Review the generated SQL** -- open the new file in `pkg/database/migrations/` and verify it does what you expect. Atlas is generally accurate but it's good practice to review.

6. **Verify the build and tests pass**:
   ```bash
   make build
   make test
   ```

7. **Commit** -- include the new `.sql` file and the updated `atlas.sum`.

### Important notes

- **No down migrations**: We use forward-only migrations. If you need to undo a change, create a new migration that reverses it.
- **Enum columns are VARCHAR**: Ent stores enum fields as `character varying`, not PostgreSQL ENUM types. Adding new enum values to an Ent schema does not require a database migration -- the validation happens at the application level. You still need `make ent-generate` to update the Go code.
- **Don't edit existing migrations**: Once a migration has been applied (in dev or production), treat it as immutable. Create a new migration instead.
- **atlas.sum must stay in sync**: If you manually edit a migration file, re-run `atlas migrate hash --dir "file://pkg/database/migrations"` to update the checksum.
