# TARSy

Go orchestrator (`cmd/tarsy`, `pkg/`) + stateless Python LLM service over gRPC (`llm-service/`) + React dashboard (`web/dashboard/`). Default branch: `master`. Design docs: `docs/architecture-overview.md`, `docs/functional-areas-design.md`, `docs/adr/`.

## Commands

- Full gate: `make check-all` (fmt, build, lint-fix, tests). Lint only: `make lint`
- Go: `make test-unit` (pkg), `make test-e2e`; one test: `go test -race ./pkg/<pkg> -run TestName`
- Python: `make test-llm`; one test: `cd llm-service && uv run pytest tests/<file> -k <name>`
- Dashboard: `make test-dashboard` (vitest + `tsc -b`); one file: `cd web/dashboard && npx vitest run <file>`
- Dev stack: `make doctor`, `make dev`, `make dev-stop`
- Go DB and e2e tests start a shared PostgreSQL testcontainer, so they need a running container runtime (podman/docker)

## Generated code

- `ent/` is generated from `ent/schema/` → `make ent-generate`
- `proto/*.pb.go` and `llm-service/llm_proto/` are generated from `proto/llm_service.proto` → `make proto-generate`
- After changing `llm-service/uv.lock`: `make llm-requirements`
- Schema change: edit `ent/schema/`, `make ent-generate`, `make migrate-create NAME=<name>`, then use the `db-migration-review` skill. Atlas output always needs manual cleanup

## Conventions

- Service-layer writes that must survive a client disconnect use `context.WithTimeout(context.Background(), …)` instead of the request context. This is deliberate (see `pkg/services/session_service.go`)
- In tests, assert exact values for strings we own; use `Contains` only for third-party/runtime text

## Commits and PRs

- Don't commit, amend, or push unless asked in the current turn. Finishing a task is not permission
- Conventional commit titles: `type(scope): summary`. Bug fixes reference an issue (`Fixes #123`)
- PR descriptions: short and human, say how the change was verified, no AI essays (see `CONTRIBUTING.md`)
- Net-new product features need a design discussion in an issue first. Ask before large architectural changes
- Don't weaken CI, auth, or permission checks without an explicit request

## Learnings

`/learn` appends non-obvious discoveries to the nearest relevant `AGENTS.md` (package-level preferred over root). Don't create empty nested files.
