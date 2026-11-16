---
description: Apply an llm-service Dependabot upgrade locally so uv.lock and requirements.txt stay in sync. Use when given a Dependabot PR URL for llm-service. Do not commit.
---

Reproduce a Dependabot upgrade for `llm-service` in the working tree so both `uv.lock` and `requirements.txt` stay in sync. Stop for review. Do not branch, commit, push, or open a PR.

The Dependabot PR URL is the argument passed to this command:

$ARGUMENTS

If that is missing, empty, or still the literal `$ARGUMENTS`, use a PR URL from the user message. If there is no URL, ask for one and stop.

Dependabot updates `llm-service/uv.lock` (and sometimes `pyproject.toml`) and skips `llm-service/requirements.txt`. The image installs that hash-pinned export, and `make llm-requirements-check` fails when it drifts from the lockfile.

1. If the working tree is dirty, or the current branch is not the default branch, stop and ask. Do not check out the Dependabot branch.
2. Read the PR (`gh pr view`, `gh pr diff`). List every package and the exact version it bumps in `llm-service/uv.lock`. Note any `pyproject.toml` constraint edits. Ignore Dependabot's lockfile diff as something to copy.
3. Apply only those `pyproject.toml` constraint edits. Do not add a transitive package to `pyproject.toml`.
4. From `llm-service/`, lock each bumped package to the PR's version (one `--upgrade-package` per package, not a full `--upgrade`):

```bash
uv lock --upgrade-package '<name>==<version>'
```

5. From the repo root, regenerate and verify the image requirements:

```bash
make llm-requirements
make llm-requirements-check
```

6. Show `git diff --stat` and the package bumps, including any extra transitive bumps uv pulled in. Leave the changes uncommitted. Repeat the Dependabot PR number so it is available for the commit.

## When asked to commit

Commit only after the user has reviewed and explicitly asks. In the commit message body, reference the Dependabot PR this change replaces:

```
Replaces #<number>
```

Use the PR number from the URL given at the start. Do not close or comment on that PR.
