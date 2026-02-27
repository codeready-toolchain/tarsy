# Verify Design Document

Carefully and systematically review the provided design document to verify it is **consistent**, **aligned with the current codebase**, and **free of meaningful gaps**.

---

## Before you start

Build a thorough understanding of the relevant parts of the system:

- Read the design document fully — understand its goals, architecture, concepts, and implementation plan
- If a companion questions doc exists (e.g. `{name}-questions.md`), read it too — check that resolved decisions are reflected accurately in the design
- Explore the codebase: look at the packages, types, interfaces, and patterns that the design touches or depends on
- Read existing documentation in `docs/` that relates to the same area

---

## What to verify

### 1. Internal consistency
- Do all sections of the document agree with each other?
- Are terms, names, and concepts used consistently throughout?
- Do examples and diagrams match the described architecture?
- If there are phases or steps, do they form a coherent sequence?

### 2. Alignment with the codebase
- Do referenced types, interfaces, packages, and files actually exist?
- Does the design correctly describe current behavior where it references it?
- Are proposed changes compatible with existing patterns and conventions?
- Are there recent code changes that the design doesn't account for?

### 3. Gaps and omissions
- Are there edge cases or failure modes not addressed?
- Are migration or backward-compatibility concerns covered where needed?
- Does the implementation plan have enough detail to act on?
- Are there implicit assumptions that should be stated explicitly?
- Are there dependencies between components that aren't acknowledged?

### 4. Open questions
- Are all open questions clearly marked?
- Are there unmarked decisions that should be questions?
- Have any "resolved" questions been left inconsistent with the design text?

---

## Output

Present your findings as a structured review:

1. **Summary** — one paragraph overall assessment
2. **Issues** — numbered list, each with severity (high / medium / low), location in the document, and a clear description of the problem
3. **Suggestions** — optional improvements that aren't strictly issues

Do **not** modify the design document. Report findings only.
