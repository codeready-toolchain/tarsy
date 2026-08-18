# TARSy - agent guide

TARSy is a hybrid Go + Python SRE system: Go orchestrator (alerts, sessions, MCP tools), a stateless Python LLM service over gRPC, and a React dashboard.

Default branch: `master`. Architecture and package map: `CLAUDE.md`.

## Commands

Never invent alternate package-manager commands if these work.

- Full local gate: `make check-all`
- Lint: `make lint` (`make lint-fix` to auto-fix)
- Test: `make test`
- Format: `make fmt`
- Dev: `make doctor`, `make setup`, `make dev`, `make dev-stop`
- Go: `make test-unit`, `make test-go`, `make build`
- Python LLM service: `make test-llm`
- Dashboard: `make test-dashboard`
- Codegen: `make ent-generate`, `make proto-generate`
- Migrations: `make migrate-create NAME=add_feature` — then apply the `db-migration-review` skill

## Skills

Project skills live in `.cursor/skills/` (symlinked as `.claude/skills/`). Load a skill when its description matches the task. Do not preload all of them.

## How we work

- Don't assume; surface tradeoffs and ask when unclear
- Minimum code that solves the problem; no speculative features or abstractions
- Touch only what you must; no drive-by refactors
- Define success criteria and verify before finishing

## Commits and PRs

- Never commit, amend, or push unless the user explicitly asks in this turn
- “Make the change”, “fix the tests”, or finishing a task is not permission to commit
- If it is unclear, leave changes uncommitted and ask
- Conventional commits: `feat|fix|docs|chore|refactor|test(scope): summary`
- Prefer small, focused PRs
- Fixes should reference an issue: `Fixes #123`

## Style

- Match existing code; do not reformat unrelated files
- Prefer clear names over clever abstractions
- No AI walls of text in PR descriptions

## Safety

- Do not commit secrets
- Do not weaken CI, auth, or permission checks without an explicit human request
- Ask before large architectural changes

## Learnings

When using `/learn`, append **non-obvious** discoveries to the nearest relevant `AGENTS.md` (package-level preferred over root). Do not create empty nested files.
