# Database Migrations

This directory contains SQL migration files for the database schema.

## Migration Files

Migration files follow the golang-migrate naming convention:
- `{version}_{name}.up.sql` - Apply the migration
- `{version}_{name}.down.sql` - Rollback the migration

Example:
- `000001_initial_schema.up.sql`
- `000001_initial_schema.down.sql`

## Creating Migrations

Use the makefile target to create new migrations:
```bash
make migrate-create NAME=add_feature
```

## How Migrations Work

1. Migration files are embedded into the binary at compile time using Go's `embed` directive
2. On application startup, pending migrations are automatically applied
3. If no migration files exist, the application falls back to Ent's auto-migration

This file (README.md) exists to satisfy Go's embed requirements, which needs at least one file in the directory.
