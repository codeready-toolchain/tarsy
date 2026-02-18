# Phase 8.3: Slack Notifications — Open Questions

Questions where the design departs from old TARSy or involves non-obvious trade-offs.

---

## Q1: Block Kit vs Legacy Attachments

**DECIDED: A — Block Kit only.**

Old TARSy used legacy Slack attachments with color bars (`good`/`danger`/`warning`). Slack has been deprecating legacy attachments in favor of Block Kit. Block Kit is Slack's long-term investment, and emoji indicators (`:white_check_mark:`, `:x:`, `:hourglass:`) are more accessible than color bars (color blindness). Dashboard link rendered as a button element.

---

## Q2: Notification Interface — Slack-specific vs Abstract Notifier

**DECIDED: A — Slack-specific (`*slack.Service`, nil-safe).**

No concrete plans for non-Slack backends. YAGNI — the Notifier interface is trivial to extract later if needed (method signatures are already clean). Worker takes `*slack.Service` directly; an interface is used only for testing (mock the Slack service in worker unit tests).

---

## Q3: Where to Trigger Notifications — Worker vs EventPublisher Subscriber

**DECIDED: A — Direct from Worker.**

The worker already has the session data and the execution result in scope. Slack notification is a natural part of the session completion lifecycle. An event-subscriber approach would require re-querying the database for executive summary and fingerprint (not in event payloads), adding complexity for no benefit at our scale.

---

## Q4: Thread TS Caching Strategy

**DECIDED: A — Cache in worker lifecycle, with short timeout on the start call.**

When a fingerprint is present, two notifications are sent (start + terminal). Resolve `thread_ts` during the start notification and pass the cached value to the terminal notification — one `conversations.history` call instead of two. No fingerprint → no start notification → no pre-execution Slack call at all. The start notification uses a short timeout (5s) to avoid delaying execution if Slack is slow or unreachable.

---

## Q5: Dashboard URL Configuration

**DECIDED: B — Top-level `system.dashboard_url`, default `http://localhost:8080`.**

Old TARSy derived the dashboard URL from `cors_origins[0]` (a hack). Placing it at `system.dashboard_url` gives it two concrete consumers now (Slack notification links, CORS origin) and a likely third in Phase 9 (OAuth redirects). Default to `http://localhost:8080` so local dev works OOTB without touching `tarsy.yaml` — Slack is disabled locally anyway (no token), and Vite dev proxy avoids CORS issues.

```yaml
system:
  dashboard_url: "https://tarsy.example.com"  # Default: http://localhost:8080
  slack:
    enabled: true
```

---

## Q6: Notification Content — Executive Summary vs Final Analysis

**DECIDED: A — Executive summary with final_analysis fallback.**

The executive summary is LLM-generated and designed for brevity — natural fit for Slack. If executive summary generation failed (fail-open), fall back to first ~2900 chars of final analysis. Full details available in the dashboard via the link.

---

## Q7: Start Notifications — When to Send

**DECIDED: A — Only with fingerprint (matches old TARSy).**

Start notifications are only useful when they appear in context (the original alert thread). Posting orphaned "processing started" messages to a channel without thread context is noise. No fingerprint → no start notification → only terminal notification sent.

---

## Q8: Slack Service Shutdown

**DECIDED: A — Fire-and-forget with timeout.**

Slack notifications run synchronously in the worker's lifecycle with a bounded timeout (10s for terminal, 5s for start). If Slack is slow or unreachable, we move on. No separate shutdown coordination needed. Matches the fail-open philosophy.

---

## Q9: Masking Sensitive Data in Slack Messages

**DECIDED: A — No additional masking.**

The executive summary is LLM-generated from already-masked tool results — it shouldn't contain raw secrets. Error messages are Go error strings, not raw data. Same trust level as the dashboard. If strict requirements arise later, applying `MaskingService` to Slack content is a one-line addition.

---

## Summary

| # | Question | Recommendation |
|---|----------|---------------|
| Q1 | Block Kit vs Legacy Attachments | **A: Block Kit only** (decided) |
| Q2 | Slack-specific vs Abstract Notifier | **A: Slack-specific** (decided) |
| Q3 | Trigger Location | **A: Direct from Worker** (decided) |
| Q4 | Thread TS Caching | **A: Cache in worker lifecycle, short timeout** (decided) |
| Q5 | Dashboard URL Config | **B: Top-level `system.dashboard_url`** (decided) |
| Q6 | Content (Summary vs Analysis) | **A: Executive summary with fallback** (decided) |
| Q7 | Start Notifications | **A: Only with fingerprint** (decided) |
| Q8 | Shutdown Behavior | **A: Fire-and-forget with timeout** (decided) |
| Q9 | Masking in Slack Messages | **A: No additional masking** (decided) |
