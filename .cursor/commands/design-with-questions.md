# /design-with-questions

You are in design mode. Generate **two documents** and then walk through the questions together.

---

## Before you start

Do a thorough analysis before generating anything. A good design requires deep understanding of the existing system.

- Explore the codebase: understand the structure, relevant packages, existing interfaces, patterns, and conventions
- Read existing documentation in `docs/` — sketches, prior designs, proposals, anything related to this feature
- Look at existing tests to understand expected behavior and boundaries
- If the feature involves external technologies, libraries, or patterns you're not confident about, research them first

Only start generating documents once you have enough context to produce an accurate, well-grounded design.

---

## Phase 1: Generate two documents

### 1. `docs/proposals/{name}-design.md` — Design Document

A best-guess draft of the full design. Complete enough to reason about, but with open question markers wherever a decision is still needed.

Required sections:
- **Status** header: `Draft — pending decisions from [{name}-questions.md]({name}-questions.md)`
- **Overview** — what this is and why it exists
- **Design Principles** — key constraints and guiding goals
- **Architecture / How It Works** — components, relationships, data flows (use diagrams where helpful)
- **Core Concepts** — key abstractions and their roles
- **Implementation Plan** — PR-sized steps (see below)
- **Open Questions** — summary list cross-referencing the questions doc

#### Implementation Plan

Write it for an AI implementer, not a human project plan. Split **code** into PRs; non-code steps (research, configuration-only, docs-only) are fine when they are not a PR.

Each PR:
- Leaves the product fully working. New behavior may land incrementally and only be “complete” in a later PR. Do **not** break existing functionality unless the payoff is clearly worth it — call that out for discussion.
- Is one logical chunk: not a dump, not a drive-by. Split or merge only when it makes implementation easier.
- Ships tests with the change. Deferring tests (e.g. e2e until the feature is whole) is OK if the plan says **what** is deferred, **to which PR**, and **why**.

For each PR/step: what lands, where, tests in this PR vs later, any temporary gap vs current behavior.

For each unresolved decision, add an inline marker in the relevant section:
```
> **Open question:** {brief description} — see [questions document]({name}-questions.md), Q{N}.
```

### 2. `docs/proposals/{name}-questions.md` — Design Questions

A numbered list of open questions. Each question must have options with trade-offs and a recommendation.

Header:
```
**Status:** Open — decisions pending
**Related:** [Design document]({name}-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.
```

Format per question:
```markdown
## Q{N}: {Question title}

{Up to a few sentences of context — why this decision matters.}

### Option A: {Name}
- **Pro:** ...
- **Con:** ...

### Option B: {Name}
- **Pro:** ...
- **Con:** ...

**Recommendation:** Option X. {Rationale — why this option is preferred.}
```

Include as many questions as the scope warrants — a small task may have 3, a large feature may have 20. Cover all decisions that meaningfully affect the design. Skip trivial implementation details.

---

## Phase 2: Question walk-through

After generating both documents, go through questions **one at a time**:

1. Present **Q1** — state the question clearly and wait
2. A discussion may follow: clarifications, additional research, checking something in the codebase — engage fully until the user is ready to decide
3. When a decision is made, **rewrite that question in the questions doc**:
   - Keep the chosen option in full (question, description, pros/cons)
   - Collapse all rejected options into a single brief line each — no need to preserve their full detail
   - Append the decision:
   ```
   **Decision:** Option X — {brief rationale if provided}

   _Considered and rejected: Option A ({one-line reason}), Option B ({one-line reason})_
   ```
4. Only then present **Q2**

Go one question at a time. Do not present the next question until the current one is decided and the doc is updated.

If a question's answer makes a subsequent question moot, note that and skip it.

---

## Phase 3: Update the design

When all questions are resolved, or when the user explicitly asks to finalize:

Update `docs/proposals/{name}-design.md` to reflect all decisions:
 - Remove open question markers that are now resolved
 - Fill in concrete design choices where placeholders existed
 - Ensure the document is internally consistent — decisions made later in the walk-through may affect sections written earlier
 - Rewrite the Implementation Plan as PR-sized steps (rules above)
 - Update the status header to `Final` (or `In Review`, as appropriate)

---

## Naming

Use `{name}` as a kebab-case identifier for the feature or topic.  
Examples: `orchestrator-agent`, `auth-flow`, `rate-limiting`, `data-pipeline`

Both documents live in `docs/proposals/`:
- `docs/proposals/{name}-design.md`
- `docs/proposals/{name}-questions.md`
