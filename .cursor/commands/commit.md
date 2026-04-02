# Commit Changes

Analyze uncommitted changes, create a branch if needed, and commit with a well-structured message.

---

## Step 1: Detect the default branch

Run `git remote show origin` (or fall back to checking which of `main`/`master` exists locally) to determine the default branch name.

## Step 2: Branch management

### If currently on the default branch

All uncommitted changes belong to a new feature/fix. **Create a new branch** before committing:

1. Analyze the changes (see Step 3) to understand their purpose.
2. Create a descriptive branch name: `<type>/<short-slug>` where type is `feat`, `fix`, `refactor`, `docs`, `chore`, `test`, etc.
3. Run `git checkout -b <branch-name>`.

### If currently on a non-default branch

Ask the user:

> You're on branch `<current-branch>`. Do you want to:
> 1. Commit to this branch
> 2. Create a new branch from `<default-branch>`

If the user picks option 2, follow the same branch creation steps as above.

## Step 3: Analyze changes

Run these commands in parallel to understand the full picture:

- `git status` — see all tracked/untracked changes
- `git diff` — unstaged changes
- `git diff --cached` — staged changes
- `git diff HEAD` — combined view of all uncommitted changes
Also run `git log --oneline -10` to glance at recent commit style. If the existing messages are well-structured and consistent, adopt their conventions (prefix style, tense, etc.) alongside the format rules below. If they are messy or inconsistent, ignore them and follow this command's format exclusively.

Review all changes carefully. Understand **what** changed and **why** before writing the commit message.

## Step 4: Stage and commit

1. **Pre-flight check** — Before staging, review every file in the changeset for suspicious content:
   - Secrets or credentials (API keys, tokens, passwords, `.env` files, private keys)
   - Debug leftovers (console.log dumps, TODO/HACK/FIXME hacks, hardcoded localhost URLs, test data)
   - Files that likely belong in `.gitignore` (build artifacts, editor configs, OS files like `.DS_Store`, vendor dirs, `node_modules`, `__pycache__`)
   - Accidentally included large or binary files
   - **Unrelated changes** — If some files appear thematically unrelated to the main body of changes (e.g., different feature, tooling config mixed with feature code, changes likely made by a parallel agent session), list them and ask the user whether to:
     (a) Include everything in this commit with an adjusted message
     (b) Exclude the unrelated files (leave unstaged for a separate commit)
     (c) Create a separate commit for the unrelated files first
     Do NOT silently include or exclude unrelated files — always ask.

   **If anything suspicious or unrelated is found, STOP and ask the user** before proceeding. List the flagged files/lines and suggest whether to exclude them, add them to `.gitignore`, or confirm they're intentional.

2. Stage all relevant files (`git add` — be selective based on the above review).
3. Commit using `--signoff` and a HEREDOC for proper formatting:

```bash
git commit --signoff -m "$(cat <<'EOF'
<title>

<body>
EOF
)"
```

### Commit message format

**Title** (first line):
- Brief, imperative mood ("Add X", "Fix Y", "Refactor Z")
- Describes **what the commit achieves** at a high level
- No implementation details — someone should understand the purpose without reading the diff
- Max ~60 characters
- If the repo already uses Conventional Commits prefixes (`feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`, etc.) based on the `git log` check, adopt the same convention. Otherwise, don't add them — a clear imperative verb already conveys the type.

**Body** (after blank line):
- Key changes as concise bullet points
- Focus on **important decisions and notable changes**, not a line-by-line changelog
- Mention new files/packages only if they represent significant additions
- Omit trivial or obvious details

**Good example:**
```
Add hybrid memory retrieval for investigations

- Combine semantic search (pgvector) with keyword matching (ts_query)
- Merge results via reciprocal rank fusion (RRF)
- Add configurable weight and limit parameters
- Index investigation summaries on insert/update
```

**Bad example:**
```
Update memory.go, add search.go, modify config.yaml, fix tests

- Changed MemoryService struct to add SearchEngine field
- Added NewSearchEngine() constructor in search.go
- Updated config.yaml to include memory.search section
- Fixed TestMemoryService to pass new required param
- Ran goimports on 3 files
```

## Step 5: Verify

Run `git status` after committing to confirm success. Show the user the commit hash and summary.
