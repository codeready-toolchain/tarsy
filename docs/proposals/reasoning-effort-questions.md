# Reasoning effort — design questions

**Status:** All decisions made
**Related:** [Design document](reasoning-effort-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where does `reasoning_effort` live?

Effort is a property of a model call. TARSy already selects models by naming an LLM provider (`defaults`, chain, stage, summarization, scoring). The new field can sit on that provider entry, or it can follow the same override stack as `llm_provider` so one provider ID can run at different efforts in different stages.

### Option A: On the LLM provider entry only

- **Pro:** Matches how `model` and API keys already work. Two efforts are two entries (`gemini-3.8-flash-high`, `gemini-3.8-flash-medium`), and every existing selector keeps working.
- **Pro:** One struct field, one proto field, no resolver changes.
- **Con:** A chain that wants the same model at two efforts needs two provider names.

**Decision:** Option A — the field lives on the LLM provider entry. Two efforts are two provider names.

_Considered and rejected: Option B (override hierarchy beside `llm_provider` — every pairing site would grow a field and merge rules), Option C (global `defaults.reasoning_effort` — a single value cannot be legal on every family, and the per-provider field would still be required)._

---

## Q2: Which effort levels are in the config vocabulary?

The vendors do not share a set, and TARSy already has custom OpenAI-compatible endpoints (`openai-gemini-proxy`: `type: openai`, an arbitrary model ID, and a `base_url`). A closed catalog goes stale (LiteLLM has shipped wrong GPT-5.5/5.6 flags and has silently dropped `max`). The provider's own 400 is the authority.

### Pass the string through; document a well-known set

The YAML field is a free string. Constants `low`, `medium`, `high`, `xhigh`, and `max` exist for docs, the config viewer, and tests. Any other non-empty token (`none`, `minimal`, a private gateway's level) is forwarded unchanged.

- **Pro:** A custom `base_url` or a new level does not wait on a TARSy release.
- **Pro:** The well-known constants still tell operators what the common models accept.
- **Con:** A typo or a level the model rejects boots successfully and fails on the first call, unless the warning below is noticed.

**Decision:** Pass the string through. Well-known constants are `low`, `medium`, `high`, `xhigh`, `max`. An empty or whitespace-only value fails config load. TARSy does not rewrite a level to a nearby one.

_Considered and rejected: Option A (intersection `low`/`medium`/`high` only — drops `xhigh` and `max`), Option B (closed five-value enum rejected at load — catalog rots, and custom providers get blocked), Option C (per-vendor native enums as the only legal values — same catalog problem)._

---

## Q3: Where is the Claude floor?

GPT 5.6+, Grok 4.6+, and Gemini 3.8+ are fixed. Claude is the awkward family.

The floor does not gate the field. An explicit `reasoning_effort` is forwarded on any Claude ID (Q2, Q5). The floor decides which Claude models, when the field is omitted, switch to adaptive thinking plus effort `high`. Below the floor, an omitted field keeps today's payload.

Today an omitted field sends `thinking.budget_tokens: 32000` for Claude 4.x, including Opus 4.6 and Sonnet 4.6 (`deploy/config` still points Vertex at those IDs). Generation 5 (`claude-sonnet-5`, `claude-opus-5`, `claude-opus-5-5`, `claude-fable-5`) already sends adaptive thinking and no effort. The API default effort is `high`.

Generation 5 and 5.5 must be supported. 4.8 should be included when it falls out of the same check. 4.6 and 4.7 must keep the current omitted-field payload so existing providers do not move.

### Claude 4.8 and newer

One compare: parsed `(major, minor) >= (4, 8)`. Missing minor is 0, so `claude-sonnet-5` is 5.0. A trailing `YYYYMMDD` is ignored. `4-6` and `4.6` both parse as 4.6.

That includes `claude-opus-4-8`, `claude-sonnet-5`, `claude-sonnet-5-5`, `claude-opus-5`, and `claude-opus-5-5`. It excludes `claude-sonnet-4-6`, `claude-opus-4.6`, and `claude-opus-4-7`.

- **Pro:** 5 and 5.5 are the same branch the code already uses for adaptive thinking. Adding `output_config.effort` (default `high`) is the whole change for them.
- **Pro:** 4.8 uses that same adaptive-only mode, so it is the same compare, not a special case.
- **Pro:** Omitted-field 4.6 and 4.7 stay on `budget_tokens: 32000`. The Vertex providers in `deploy/config` do not change until someone sets the field.
- **Con:** An omitted field on Opus 4.7 still sends `budget_tokens`, which that model rejects. That is the current behavior, left in place on purpose.

**Decision:** Claude 4.8 and newer. Omitted field sends adaptive thinking plus effort `high`. 4.6 and 4.7 keep the 32k budget unless `reasoning_effort` is set.

_Considered and rejected: Option A (4.6+ — moves the Vertex 4.6 providers off the 32k budget), Option B (4.7+ — moves 4.7 off its current payload), Option C (generation 5 only — skips 4.8, which the same `>= 4.8` compare covers for free)._

---

## Q4: What is sent when `reasoning_effort` is omitted?

Every eligible family's API default is `high`. TARSy already forces `high` for OpenAI reasoning models and for Gemini 3. Grok receives nothing today, and Grok's own default is `high`. Claude generation 5 receives adaptive thinking and no effort, which the API treats as `high`.

### Option A: Eligible models are sent `high`

- **Pro:** Builtins keep today's depth on OpenAI and Gemini without YAML edits. The config viewer can show the value that will actually be sent.
- **Pro:** A later vendor default change does not move TARSy investigations.
- **Con:** For Claude models at or above the floor, "send high" still changes the thinking mechanism (see Q3). The effort value matches the API default; the thinking mode does not match today's 32k budget on 4.x.

**Decision:** Option A — an omitted field on an eligible model is sent as `high`. An omitted field below the floor keeps the legacy payload. An explicit value is sent as written, on any model.

_Considered and rejected: Option B (omit the parameter and take the vendor default — the effective level is invisible, and a vendor default change moves production), Option C (per-family defaults — Sonnet `medium` or Opus `xhigh` would change cost with no YAML saying so)._

---

## Q5: What happens when the value cannot be honored?

Cases: a token outside the well-known set, `reasoning_effort` set on a legacy or custom model, `max` on Grok, `xhigh` on Gemini.

### Warn and forward

Config load logs a warning (provider name, model ID, the value, and the levels we know) and still sends that exact string. The provider 400 is the failure if the model rejects it. TARSy does not drop the field and does not clamp to a nearby level.

An empty or whitespace-only value fails config load. That is a broken field, not a custom effort.

- **Pro:** Custom endpoints and new levels keep working. A stale documented set cannot block boot.
- **Pro:** The operator sees the mismatch at startup instead of only as a quiet change in reasoning depth.
- **Con:** A warning in the log is easier to miss than a failed boot. The first investigation then fails with the provider's error.

**Decision:** Warn and forward. Empty or whitespace fails load. No clamping, no silent drop.

_Considered and rejected: Option A (config load fails — blocks custom providers and rots as vendors add levels), Option B (warn and ignore — the YAML says `medium` while the call stays on the legacy payload), Option C (clamp to the nearest level — OpenRouter does this; the investigation runs at a different depth and looks successful)._

---

## Q6: How fine-grained is the documented-level warning?

The warning table is advice. It is not an allowlist. Q2 and Q5 already decide that every non-empty value is forwarded.

### Family table, plus a Claude `xhigh` note

Warn when either is true:

1. The value is outside `low` / `medium` / `high` / `xhigh` / `max` (any provider, including a custom `base_url`).
2. There is no custom `base_url`, the model ID is one we recognize, and the value is outside that model's documented set.

Documented sets used only for warning (2):

| Recognized models | Documented levels |
|---|---|
| GPT ≥ 5.6, excluding IDs containing `-chat` or `-main` | `low`, `medium`, `high`, `xhigh`, `max` |
| Grok ≥ 4.6 | `low`, `medium`, `high`, `xhigh` |
| Gemini ≥ 3.8, excluding IDs containing `image` | `low`, `medium`, `high` |
| Claude ≥ 4.8 | `low`, `medium`, `high`, `max`. `xhigh` is documented for Opus ≥ 4.8 and for generation 5, including 5.5 (`claude-sonnet-5`, `claude-sonnet-5-5`, `claude-opus-5`, `claude-opus-5-5`, Fable 5) |

A custom `base_url` skips warning (2). `openai-gemini-proxy` can send any token the proxy accepts; it still gets warning (1) for `extra-high`. A recognized model below the floor, or a recognized non-reasoning ID (`gpt-5.6-chat`, a Gemini image model), gets warning (2) and the value is still sent.

- **Pro:** The common mismatch (`xhigh` on Gemini 3.8, `max` on Grok, `xhigh` on Sonnet 4.6) shows up at startup.
- **Pro:** A proxy with a weird model ID is not lectured about a family it may not be.
- **Con:** The table will drift. A warning can be wrong in either direction; the call still uses the configured string.

**Decision:** Advisory family table, used only for startup warnings. Claude `xhigh` is documented for Opus ≥ 4.8 and generation 5. A custom `base_url` only warns when the token is outside the well-known five.

_Considered and rejected: Option A (hard family allowlist), Option B (hard family allowlist plus a hard Claude `xhigh` rule), Option C (no warning beyond a closed enum — typos and known mismatches stay silent until the provider 400)._

---

## Q7: What effort do builtin providers use?

Eligible builtins would otherwise inherit the omitted-field default of `high`. That is the top of Gemini's set and below the top of GPT (`max`), Grok (`xhigh`), and Claude (`max`).

### Highest documented level on eligible builtins

Set the field in `initBuiltinLLMProviders` for models at or above the family floor. GPT 5.6+ and Claude 4.8+ (including Sonnet 5, Sonnet 5.5, and Opus 5.5) use `max`. Grok 4.6+ uses `xhigh`. Gemini 3.8 uses `high`. Builtins below the floor leave the field unset. A user provider that omits the field still gets `high` when eligible. Replacing a builtin by name does not inherit the builtin value. `claude-sonnet-5-5` is added as its own builtin. `anthropic-default` stays on Sonnet 5.

- **Pro:** The providers TARSy ships for current models run at the deepest effort those models document.
- **Pro:** Gemini stays at `high`, which is already what the code sends, because that family has nothing above `high`.
- **Con:** GPT, Grok, and Claude builtins spend more reasoning tokens than today's `high` (or, for Grok, more than the vendor default of `high`).

**Decision:** Eligible builtins set the highest documented effort for their family. User providers that omit the field stay at `high`.

_Considered and rejected: leaving builtins unset so they inherit `high` (GPT, Grok, and Claude would not run at the top of their sets)._
