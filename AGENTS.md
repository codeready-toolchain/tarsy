# TARSy - agent guide

Default branch: `master`.

## Commands

- Lint: `make lint`
- Test: `make test`
- Full local gate: `make check-all`
- Never invent alternate package-manager commands if the above work.

Stack notes (do not invent substitutes):

- Go backend: `make test-unit`, `make test-go`, `make build`
- Python LLM service: `make test-llm`
- Dashboard: `make test-dashboard`
- Format: `make fmt`

Also read `CLAUDE.md` and skills under `.cursor/skills/` (symlinked for Claude Code) before substantive edits.

## Commits and PRs

- Conventional commits: `feat|fix|docs|chore|refactor|test(scope): summary`
- Prefer small, focused PRs
- Fixes should reference an issue: `Fixes #123`

## Style

- Match existing code; do not reformat unrelated files
- Prefer clear names over clever abstractions
- No AI walls of text in PR descriptions
- Avoid drive-by refactors

## Safety

- Do not commit secrets
- Do not weaken CI, auth, or permission checks without an explicit human request
- Ask before large architectural changes

## Learnings

When using `/learn`, append **non-obvious** discoveries to the nearest relevant `AGENTS.md` (package-level preferred over root).
