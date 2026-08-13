# ADR-0022: Tier 0 Calendar and Staffing Context

**Status:** Implemented  
**Date:** 2026-08-12  
**Related:** [ADR-0007: Automated Actions](0007-automated-actions.md) (action agents consume Tier 0; this ADR does not change the action type)

## Overview

Action and investigation agents sometimes need to know whether humans are likely available **when the decision is made** — for example so domain-specific `custom_instructions` can treat weekends or global holidays as reduced-review windows. Leaving weekday/holiday inference to the LLM is unreliable (models mis-map dates; alert timestamps are the wrong clock).

This decision extends existing Tier 0 prompt context (already injecting `Current time: <RFC3339> (<Weekday>)` via `ComposeInstructions` / `ComposeChatInstructions`) with an explicit **calendar classification** and **staffing hint**, computed server-side from wall-clock UTC at prompt-build time. Optional `system.holidays` in `tarsy.yaml` lets deployments own the holiday list.

## Design Principles

1. **Server-side classification** — TARSy labels `WEEKEND` / `GLOBAL_HOLIDAY` / `WEEKDAY`; agents read the block and must not re-derive dates from alert or session timestamps.
2. **Wall-clock “now”** — use `time.Now().UTC()` when the prompt is built (when remediation/investigation runs), not the alert fire time or session `created_at`. Staffing gaps are about human availability at decision time.
3. **Extend Tier 0, don’t add a tool** — calendar context is not an MCP tool and not a separate prompt tier; it stays beside the existing current-time line.
4. **Soft signal only** — reduced staffing informs agent judgment when operators are less available; it does not by itself authorize or execute actions.
5. **Deployments own product holidays** — small built-in defaults; non-empty `system.holidays` **replaces** defaults so a product can list a multi-day break without forking code.

## Prompt shape

```text
## Context

Current time: 2026-12-25T08:00:00Z (Friday)
Calendar context (UTC): 2026-12-25 — Friday — GLOBAL_HOLIDAY (Christmas)
Staffing: reduced (humans less likely to review promptly)
```

Weekend example:

```text
Calendar context (UTC): 2026-08-01 — Saturday — WEEKEND
Staffing: reduced (humans less likely to review promptly)
```

## Configuration

```yaml
system:
  holidays:
    - date: "12-24"
      name: "Christmas Eve"
    - date: "12-25"
      name: "Christmas"
    # ... through 01-01 as needed
```

Documented in `deploy/config/tarsy.yaml.example`. Holidays are resolved onto `Config` and passed into `NewPromptBuilder`.

## Consequences

- Action agent `custom_instructions` can reference Tier 0 staffing without embedding holiday calendars in every deployment’s agent text.
- Golden / e2e normalizers must stabilize `Calendar context` and `Staffing` lines the same way they stabilize `Current time`.
- Changing product holiday policy is a config change (replace list), not a TARSy code change.

## Follow-ups

- Locale-aware or region-specific calendars (explicitly out of v1).
- Optional per-agent opt-out of Tier 0 (not needed today; all Compose* consumers get the same context).
