# Phase 4.2: Data Masking — Questions (All Resolved)

All questions resolved. Decisions are reflected in `docs/phase4.2-data-masking-design.md`.

---

## Q1: KubernetesSecretMasker — Implement Now or Defer?

**Decision: A — Implement in Phase 4.2.**

Build `KubernetesSecretMasker` as part of this phase. The project plan explicitly calls for it. The `kubernetes` pattern group already references `kubernetes_secret`, so without the masker, that group member silently does nothing.

---

## Q2: Fail-Safe Behavior for MCP Tool Results

**Decision: C (modified) — Fail-closed with warning, but no retry suggestion.**

Return `[REDACTED: data masking failure — tool result could not be safely processed]`. The LLM gets context about why the result is missing but is NOT told to re-attempt (which would risk a retry loop if the same content triggers the same masking failure).

---

## Q3: MaskingService Lifetime — Singleton vs Per-Session

**Decision: A — Singleton, created once at startup.**

All patterns compiled once. Thread-safe, zero per-session overhead. Config is static after startup so there's no reason to recompile per session.

---

## Q4: Alert Masking Config Location

**Decision: A — Add to `defaults` in tarsy.yaml.**

Consistent with how all other config works. Env vars can still be used via `{{.VAR}}` interpolation if needed.

---

## Q5: Custom Masking Pattern Compilation — Eager vs Lazy

**Decision: A — Eager, compile all at startup.**

Fail-fast philosophy matches the rest of TARSy. Invalid patterns surface at startup rather than mid-investigation.

---

## Q6: Should Pattern Groups Support Code Masker References?

**Decision: A — Unified namespace.**

Pattern group members can reference either regex patterns or code maskers by name. Already how `builtin.go` is structured — no config changes needed.

---

## Q7: Custom Patterns on Chains (Not Just MCP Servers)

**Decision: A — MCP server-level only.**

Masking is about the data source, not the consumer. A kubernetes-server masks the same way regardless of which chain invokes it. Can be revisited later if a concrete need arises.

---

## Q8: Regex Safety — ReDoS Protection

**Decision: A — Rely on Go's RE2-safe regexp.**

Go's `regexp` package uses RE2 which guarantees linear-time matching — inherently immune to catastrophic backtracking. Document this as an intentional design choice. The 15 built-in patterns are already written for RE2.

---

## Summary

| # | Question | Recommendation | Impact |
|---|----------|---------------|--------|
| Q1 | K8s Secret masker timing | **A: Phase 4.2** | DECIDED |
| Q2 | MCP fail-safe | **C: Fail-closed + warning (no retry)** | DECIDED |
| Q3 | Service lifetime | **A: Singleton** | DECIDED |
| Q4 | Alert masking config | **A: In defaults** | DECIDED |
| Q5 | Pattern compilation | **A: Eager** | DECIDED |
| Q6 | Pattern group namespace | **A: Unified** | DECIDED |
| Q7 | Custom patterns scope | **A: Server-level only** | DECIDED |
| Q8 | ReDoS protection | **A: Rely on RE2** | DECIDED |
