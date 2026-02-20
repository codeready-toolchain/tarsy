# /sketch-with-questions

You are in sketch mode. The feature or idea is not yet well-defined enough for a detailed design. Your job is to produce a high-level technical sketch and a questions document, then walk through the questions one at a time to form the sketch. Do not attempt detailed architecture, implementation plans, or interface definitions — those belong in a `/design-with-questions` session.

---

## Before you start

Do a thorough analysis before generating anything. You need to understand the existing system well enough to place this feature in context.

- Explore the codebase: understand the overall structure, relevant components, and how related features currently work
- Read existing documentation in `docs/` — especially any sketches, designs, or proposals that relate to this feature
- If the feature involves external technologies, libraries, or patterns you're not confident about, research them first

Only start generating documents once you have enough context to do so accurately.

---

## Phase 1: Generate two documents

### 1. `docs/{name}-sketch.md` — Sketch Document

A high-level technical description of the feature. Structure it however best fits the idea — not every feature is alike, and a rigid template can get in the way. Use judgment.

The document should always include:
- **Status** header: `Draft — sketch in progress, see [{name}-questions.md]({name}-questions.md)`
- A clear explanation of what this is and what problem it solves
- How it relates to the existing system
- Key concepts and rough behavior
- What is out of scope
- Open question markers cross-referencing the questions doc

Beyond that, choose sections that make sense for this specific feature. Use diagrams where they add clarity. Keep it high-level — no detailed architecture, implementation plans, or interface definitions. Those come later.

For uncertain areas, add an inline marker:
```
> **Open question:** {brief description} — see [questions document]({name}-questions.md), Q{N}.
```

### 2. `docs/{name}-questions.md` — Sketch Questions

A numbered list of open questions. These questions shape the technical sketch — they clarify what the feature is, how it behaves, how it integrates, and where the key technical trade-offs lie. They are not detailed implementation decisions (those belong in a design-with-questions session).

Header:
```
**Status:** Open — decisions pending
**Related:** [Sketch document]({name}-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.
```

Format per question:
```markdown
## Q{N}: {Question title}

{Up to a few sentences of context — why this question matters for the sketch.}

### Option A: {Name}
- **Pro:** ...
- **Con:** ...

### Option B: {Name}
- **Pro:** ...
- **Con:** ...

**Recommendation:** Option X. {Rationale — why this option is preferred.}
```

Include as many questions as needed. Some sketches are fuzzy in one dimension, others in many.

---

## Phase 2: Question walk-through

After generating both documents, go through questions **one at a time**:

1. Present **Q1** — state the question clearly and wait
2. A discussion may follow: clarifications, additional research, checking something in the codebase — engage fully until the user is ready to decide
3. When a decision is made, **rewrite that question in the questions doc**:
   - Keep the chosen option in full (question, description, pros/cons)
   - Collapse all rejected options into a single brief line each
   - Append the decision:
   ```
   **Decision:** Option X — {brief rationale if provided}

   _Considered and rejected: Option A ({one-line reason}), Option B ({one-line reason})_
   ```
4. Only then present **Q2**

Go one question at a time. Do not present the next question until the current one is decided and the doc is updated.

If a question's answer makes a subsequent question moot, note that and skip it.

---

## Phase 3: Update the sketch

When all questions are resolved, or when the user explicitly asks to finalize:

Update `docs/{name}-sketch.md` to reflect all decisions:
 - Remove open question markers that are now resolved
 - Fill in the sketch based on the decisions
 - Ensure the document is internally consistent — later decisions may affect earlier sections
 - Update the status header to: `Sketch complete — ready for detailed design`

---

## Naming

Use `{name}` as a kebab-case identifier for the feature or topic.  
Examples: `orchestrator-agent`, `auth-flow`, `rate-limiting`, `data-pipeline`

Both documents live in `docs/`:
- `docs/{name}-sketch.md`
- `docs/{name}-questions.md`
