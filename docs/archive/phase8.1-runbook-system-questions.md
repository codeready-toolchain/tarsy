# Phase 8.1: Runbook System — Open Questions

Questions where the new design departs from old TARSy or involves non-obvious trade-offs. Please review and decide before implementation begins.

---

## Q1: Per-Chain Runbook Configuration (NEW — not in old TARSy)

**DECIDED: C — No per-chain runbook.**

Chain-specific investigation guidance is already handled by `custom_instructions` on agents. Runbooks provide general troubleshooting procedures selected per-alert (or defaulted globally). Adding a per-chain `runbook` field would create overlapping concerns with custom instructions. The resolution hierarchy is:

```
1. Per-alert runbook URL (from API submission)
2. defaults.runbook (inline content from YAML or builtin)
```

---

## Q2: GitHub Client Library

**DECIDED: A — `net/http` only (stdlib).**

Old TARSy used `httpx` for downloading and `PyGithub` for listing. We only need two HTTP calls: `GET /repos/{owner}/{repo}/contents/{path}` (listing) and `GET` on raw URLs (download). No need for `go-github` (50K+ lines for features we won't use). If pagination becomes an issue for very large runbook repos, `Link` header parsing is ~20 lines.

---

## Q3: Caching Strategy

**DECIDED: A — In-memory TTL cache, default 1 minute.**

Old TARSy had no caching. In-memory `map[string]*entry` with `sync.RWMutex`, content cached per normalized URL. 1-minute TTL keeps content fresh while preventing concurrent sessions from hammering GitHub for identical content. Cache is small (typically <100 entries), resets on restart.

---

## Q4: Runbook URL Domain Restriction

**DECIDED: A — Configurable domain allowlist.**

Default to GitHub domains (`github.com`, `raw.githubusercontent.com`). Organizations with internal repos (GitLab, Gitea) can extend via `runbooks.allowed_domains`. Balances security (SSRF protection out of the box) with flexibility.

---

## Q5: Failure Policy on Runbook Fetch

**DECIDED: A — Fail-open, use default content and log warning.**

Matches old TARSy behavior and TARSy's investigation-availability-first philosophy. An investigation with a generic runbook is better than no investigation. Runbook content is supplementary guidance, not critical data.

---

## Q6: Listing API Caching

**DECIDED: A — Shared TTL cache (same 1min TTL as content fetching).**

The listing is a single cached entry keyed by repo URL. Same cache, same TTL — simple and consistent.

---

## Q7: Runbook Listing — Recursive vs. Flat

**DECIDED: A — Recursive listing.**

Matches old TARSy behavior. Walk all subdirectories, return a flat list of full GitHub URLs. The cache means this only happens once per TTL period.

---

## Q8: `defaults.runbook` — Fix the Existing Config Gap

**DECIDED: A — Wire it in as part of Phase 8.1.**

Currently `defaults.runbook` is loaded from YAML but the executor ignores it (hardcoded to builtin). The executor should use `cfg.Defaults.Runbook` as the fallback, with the builtin only used if that is also empty. Straightforward fix — the field exists and is documented.

---

## Q9: GitHub Token Configuration Pattern

**DECIDED: A — `token_env` in YAML, defaulting to `GITHUB_TOKEN`.**

Config references the env var name (consistent with `LLMProviderConfig.APIKeyEnv` pattern). If `token_env` is not set, fall back to reading `GITHUB_TOKEN` directly — no config required for the common case.

GitHub config lives under a `system:` top-level section in `tarsy.yaml`, grouping it with other system-wide infrastructure settings (alongside `runbooks:`):

```yaml
system:
  github:
    token_env: "GITHUB_TOKEN"   # Optional — defaults to GITHUB_TOKEN if omitted
  runbooks:
    repo_url: "https://github.com/org/runbooks/tree/main/sre"
    cache_ttl: "1m"
    allowed_domains:
      - "github.com"
      - "raw.githubusercontent.com"
```

This keeps `system:` distinct from operational config (`defaults:`, `mcp_servers:`, `agents:`, `agent_chains:`).

---

## Summary

| Question | Recommendation | Impact |
|----------|---------------|--------|
| Q1: Per-chain runbook | **C — No per-chain** (decided) | Use custom_instructions instead |
| Q2: GitHub client | **A — stdlib `net/http`** (decided) | Zero new deps |
| Q3: Caching | **A — In-memory TTL, 1min** (decided) | Reduced GitHub API calls |
| Q4: Domain restriction | **A — Configurable allowlist** (decided) | Flexible SSRF protection |
| Q5: Failure policy | **A — Fail-open with default** (decided) | Matches TARSy philosophy |
| Q6: Listing cache | **A — Shared TTL cache** (decided) | Simple, consistent |
| Q7: Recursive listing | **A — Recursive** (decided) | Matches old TARSy |
| Q8: Fix defaults.runbook | **A — Wire it in** (decided) | Bug fix |
| Q9: Token config | **A — `token_env` in YAML, default `GITHUB_TOKEN`** (decided) | Consistent pattern |
