# Update Documentation

Update relevant documentation to reflect recent code changes.

1. Identify which docs are affected by the code changes
2. Update **only** the relevant docs — don't touch unrelated files
3. Preserve each document's existing format and style
4. **Never modify** historical records (`docs/adr/`)

## Documentation structure

- `README.md` — project overview, setup, usage
- `deploy/README.md` — deployment instructions
- `deploy/config/README.md` — configuration reference
- `deploy/config/tarsy.yaml.example` — example configuration file
- `pkg/database/migrations/README.md` — migration guide
- `docs/architecture-overview.md` — system architecture
- `docs/functional-areas-design.md` — functional area designs
- `docs/slack-integration.md` — Slack integration details
- `docs/proposals/` — design proposals (update if the proposal is being implemented)
- `docs/adr/` — architecture decision records (**read-only, do not modify**)
