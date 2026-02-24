# Promote Proposal to ADR

Move an implemented proposal from `docs/proposals/` to `docs/adr/` as a numbered Architecture Decision Record.

The user will specify which proposal to promote (by name or file path). If unclear, ask.

---

## Steps

1. **Determine the next ADR number.** List `docs/adr/` and find the highest existing `NNNN-` prefix. The new ADR gets the next sequential number, zero-padded to 4 digits (e.g., `0001`, `0002`).

2. **Read the design document** (`docs/proposals/{name}-design.md`) in full.

3. **Check for a companion questions document** (`docs/proposals/{name}-questions.md`). If it exists, read it and extract any decision rationale not already present in the design doc.

4. **Create the ADR** at `docs/adr/{NNNN}-{name}.md`:
   - Copy the design document content
   - Update the title to `# ADR-{NNNN}: {Title}` (use the existing title, drop suffixes like "— Design Document")
   - Update the header:
     - **Status:** `Implemented`
     - **Date:** today's date
     - Remove any references to the questions document
   - If the design doc has a Decisions table, add a **Rationale** column with compact reasoning from the questions doc (if available)
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
