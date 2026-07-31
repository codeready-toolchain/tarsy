# Pull Request Review Comment - Context

**The user's message contains a Pull Request review comment that needs to be
addressed.**

Treat the user's input as the reviewer's feedback. Your job is to **decide**
whether to **Implement**, **Skip**, **Clarify**, or **Alternative** — not to
make the review green by default.

---

## How to Address the PR Comment

Follow this systematic approach:

## 1. Identify the Context

**First, determine what code the comment refers to:**

- Read the files mentioned or implied by the comment
- Use recently viewed files, or file context to identify relevant files
- Understand the current implementation before proceeding
- Check how **sibling code** handles the same concern (other tools, callers,
  established patterns in this repo). Inconsistency is a signal the suggestion
  may be over-scoped.

## 2. Analysis Phase

**Do not blindly implement the suggestion.** Analyze before writing code.

### Soundness vs necessity

A comment can be **technically correct** and still **not worth doing**. Judge
both:

| Question | Ask |
|----------|-----|
| Sound? | Is the finding still true against current code? Is the reviewer mistaken or missing context? |
| Necessary? | What concrete scenario does this prevent or improve? How likely is it here? |
| Proportionate? | Does the added complexity/cost match the realistic risk or benefit? |
| Consistent? | Do peer paths (similar tools/APIs) already accept the same tradeoff? |

**"Still valid" means still true in the code — not "must implement."** Valid but
unnecessary nits (theoretical TOCTOU, speculative caching, style-only refactors
with large blast radius) should use **Skip** (disagreement is a common
rationale) unless the user explicitly wants them.

### Before implementing security, concurrency, or perf nits

State in your reasoning (and to the user if you will change code):

1. **Scenario** — the relevant concrete case: failure mode, abuse path, or
   performance impact (actors, timing/preconditions, and what goes wrong)
2. **Likelihood** — why it would or would not happen in this product/path
3. **Decision** — exactly one of: Implement / Skip / Clarify / Alternative

If you cannot name a plausible scenario for the concern, **do not implement**;
use Skip or Clarify.

### Critical: Understand Full Implications

**A suggestion may be correct but incomplete.** Always consider:

- **Ripple effects:** Changes in one area often require updates elsewhere
- **Cross-component impact:** Backend changes may require frontend updates, and
  vice versa
- **API contracts:** Changing interfaces affects all consumers
- **Database schema:** Model changes require migration scripts and all affected
  queries
- **Tests:** Implementation changes may require corresponding test updates
- **Documentation:** Code changes may need doc updates

**If you choose Implement and the suggestion requires changes beyond what's
explicitly mentioned, make those changes too.** A partial implementation is
worse than no implementation. If the full fix is large, prefer **Alternative**
or **Clarify** and confirm before proceeding.

## 3. Decision Gate (required before coding)

Pick **exactly one** named outcome and tell the user that name before coding:

1. **Implement** — sound, necessary, and proportionate; then follow §4 and §5
2. **Skip** — do not change code unless the user overrides. Common rationales:
   disagreement (scenario unlikely, inconsistent with siblings, cost outweighs
   benefit), reviewer mistaken, or finding already fixed. Explain why.
3. **Clarify** — ask before changing anything
4. **Alternative** — propose a smaller fix or different approach; do not
   implement a non-trivial delta until the user agrees

Do **not** use other labels (e.g. "Disagree", "Partial") as the decision name.
Disagreement is a **rationale for Skip**. A smaller/different approach is
**Alternative**, not a vague "partial".

Do **not** optimize for "address every bullet in the review." Optimize for a
correct product decision.

## 4. Response Strategy

### If Implement

- Acknowledge the useful part of the feedback
- Implement the fix
- Explain briefly if your change differs from the suggestion in small ways
- Update tests only when required by §5
- Update documentation if relevant

### If Clarify

- **Ask for clarification** before making changes
- Explain your current understanding and why it might be ambiguous
- Suggest options if you have ideas about what they meant

### If Skip

- Respectfully explain with technical justification (scenario + likelihood +
  cost, or why the finding is already addressed / mistaken)
- Provide context the reviewer might have missed (including sibling patterns)
- Suggest an Alternative or middle ground when useful
- Stay open to discussion — you might be missing something too

### If Alternative

- Name the outcome **Alternative** explicitly
- State what you would change instead of (or instead of fully following) the
  reviewer's suggestion, and why it is better or more proportionate
- Ask the user to accept or reject before coding if the delta is non-trivial
- If they accept, then follow §5; if they reject, stop or **Clarify** further

## 5. Implementation Guidelines

Only after choosing **Implement**, or after the user accepts an **Alternative**:

- **Make comprehensive changes:** Modify everything necessary to fully address
  the comment, including related components
- **Think across boundaries:** If backend changes, check if frontend needs
  updates; if models change, update all consumers
- **Maintain consistency:** Follow existing code style and patterns across all
  modified files
- **Preserve functionality:** Don't introduce new bugs while fixing issues
- **Consider edge cases:** Think beyond the immediate change
- **Test coverage:** When making changes, prioritize test updates:
   - **First choice:** Adjust existing tests to cover the change (better than
    adding new tests)
   - **Second choice:** Add new tests if existing ones don't cover the changed
    behavior
   - **Always:** Ensure all tests pass after your changes
   - **When to skip:** Only skip test updates if it's too tricky or doesn't make
    sense for the specific change
- **Check for linter errors:** Fix any new warnings or errors introduced in all
  modified files

## 6. Final Checklist

Before considering the comment addressed:

- [ ] Have I distinguished soundness from necessity?
- [ ] For nits: did I name a concrete scenario (failure mode, abuse path, or
      performance impact) — or Skip / Clarify?
- [ ] Did I compare with sibling/existing patterns?
- [ ] Did I name exactly one decision — **Implement**, **Skip**, **Clarify**,
      or **Alternative** — before coding?
- [ ] If **Skip**: did I explain clearly enough for the user to reply to the
      reviewer (including disagreement rationale when applicable)?
- [ ] If **Alternative**: did I propose the approach and get agreement before a
      non-trivial change?
- [ ] If **Implement** (or accepted Alternative): is the change proportionate
      and complete; did I run the repo's canonical full validation (including
      build) and rerun any failing target after fixes?
- [ ] Have I communicated the rationale to the user?
