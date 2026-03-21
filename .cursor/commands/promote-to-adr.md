# Promote Proposal to ADR

Move an implemented proposal from `docs/proposals/` to `docs/adr/` as a numbered Architecture Decision Record.

The user will specify which proposal to promote (by name or file path). If unclear, ask.

---

## Steps

1. **Determine the next ADR number.** List `docs/adr/` and find the highest existing `NNNN-` prefix. The new ADR gets the next sequential number, zero-padded to 4 digits (e.g., `0001`, `0002`).

2. **Read the design document** (`docs/proposals/{name}-design.md`) in full.

3. **Check for a companion questions document** (`docs/proposals/{name}-questions.md`). If it exists, read it and extract any decision rationale not already present in the design doc.

4. **Create the ADR** at `docs/adr/{NNNN}-{name}.md`.

   **Do NOT copy the proposal verbatim.** Distill it into an architectural decision record. The ADR should capture *what was decided and why*, not *how it was implemented file-by-file*.

   **Header:**
   - Title: `# ADR-{NNNN}: {Title}` (use the existing title, drop suffixes like "— Design Document")
   - **Status:** `Implemented`
   - **Date:** today's date
   - Remove any references to the questions document

   **What to KEEP (the decision record):**
   - Overview / problem statement
   - Design principles
   - Decisions tables with rationale — this is the core of the ADR. If the design doc has a Decisions table, add a **Rationale** column with compact reasoning from the questions doc (if available)
   - Architecture: conceptual descriptions, ASCII diagrams, behavioral contracts, data flow
   - Configuration YAML examples (user-facing contract)
   - Conceptual tables (e.g., type mappings, enum values, API endpoints)
   - High-level migration strategy (e.g., "data-preserving migration with enum renames")
   - Future considerations
   - References to other ADRs

   **What to REMOVE (implementation detail):**
   - "Affected Files" tables and file-level change lists
   - "Implementation Plan" / "Implementation Phases" step-by-step checklists
   - Go/SQL/TypeScript/Python code snippets (struct definitions, function bodies, ent schemas)
   - File path references (e.g., `pkg/agent/controller/factory.go`) — use component names instead
   - Line number references
   - PR-level breakdowns
   - Risk assessments
   - Testing strategy sections

   **Other:**
   - Fix any relative links that break due to the directory change (e.g., links to files still in `proposals/` need a `../proposals/` prefix)
   - Drop the `-design` suffix from the filename — inside `adr/`, it's redundant

5. **Delete the old files:**
   - Delete `docs/proposals/{name}-design.md`
   - Delete `docs/proposals/{name}-questions.md` (if it exists)

6. **Check for broken references.** Search `docs/` for any links pointing to the deleted files. Fix them to point to the new ADR location.

7. **Show the final result** — print the new file path and the updated `docs/` directory tree.

---

## Naming Convention

- ADR files: `docs/adr/{NNNN}-{name}.md` (e.g., `0001-agent-type-refactor.md`)
- `{name}` is kebab-case, matching the original proposal name without the `-design` suffix
- Sequential numbering provides chronological ordering
