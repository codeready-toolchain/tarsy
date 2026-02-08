# Phase 4: MCP Integration — Open Design Questions

Design questions extracted from Phase 3.2 deferred notes that need resolution before or during Phase 4 implementation.

**Status**: ⏸️ All Deferred (pending Phase 4 start)  
**Created**: 2026-02-08  
**Source**: Phase 3.2 deferred notes in `docs/project-plan.md`

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add answers inline under each question, then update the relevant design docs.

---

## Q1: Where Should ActionInput Parameter Parsing Live?

**Status**: ⏸️ Deferred

**Context:**

Old TARSy's `react_parser.py` parsed `action_input` into structured `Dict[str, Any]` parameters via a multi-format cascade: JSON → YAML → comma/newline-separated `key: value` → `key=value` → raw string fallback. It also had `_convert_parameter_value()` for type coercion (bool, int, float, None).

New TARSy's Go ReAct parser keeps `ActionInput` as a raw string. The `ToolExecutor` interface takes `Arguments string`, deferring parsing to the tool execution layer.

**Question:**

When Phase 4 implements the real `ToolExecutor` backed by MCP, where should the multi-format parameter parsing live?

**Options:**
- **A: In the MCP client itself** — parsing happens at the point of use, MCP client knows what format each server expects
- **B: In a shared utility package** — reusable parsing that any ToolExecutor implementation can call
- **C: In a ToolExecutor wrapper/middleware** — a decorator that sits between the controller and the real executor, parsing `Arguments string` into structured params before passing through

**Considerations:**
- Option C keeps the real ToolExecutor clean and testable
- Option B is more flexible but requires callers to know when to use it
- MCP servers may accept different parameter formats, so parsing might need to be server-aware

---

## Q2: Where Should Tool Name server.tool Validation Live?

**Status**: ⏸️ Deferred

**Context:**

Old TARSy split the action name into separate `server` and `tool` fields with validation that both parts are non-empty (e.g., `"server."` or `".tool"` → `ValueError` → malformed).

New TARSy's ReAct parser keeps `Action` as the full `"server.tool"` string and only validates that it contains a dot (intentionally loose — see comment in `react_parser.go` `ParseReActResponse`). Edge cases like `".tool"`, `"server."`, and `"a.b.c"` pass the parser's dot-check and are rejected later by the controller's tool-name-set lookup.

**Question:**

Should stricter validation (e.g., regex `^\w[\w-]*\.\w[\w-]*$`) be added at the parser level, or should it remain in the MCP routing layer?

**Options:**
- **A: Parser-level validation** — reject malformed names early with a clear error message, before the controller loop
- **B: MCP routing layer** — keep the parser loose, let the MCP client validate when it needs to split and route
- **C: Both** — parser does basic format validation, MCP client validates server existence

**Considerations:**
- Option A gives better error messages to the LLM (specific "invalid format" vs generic "unknown tool")
- Option B keeps the parser simpler and avoids duplicating validation logic
- The current two-tier approach (loose dot-check → tool-set lookup) works well in practice; the question is whether it's worth tightening for Phase 4
