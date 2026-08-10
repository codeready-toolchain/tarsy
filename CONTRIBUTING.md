# Contributing to TARSy

## What merges easily

- Bug fixes with a clear reproduction
- Docs improvements
- Small, focused changes with tests when behavior changes

## Design review first

Net-new product features (especially UI or core behavior) need a short design discussion in an issue **before** a PR. Wait for maintainer approval.

Look for labels: `help wanted`, `good first issue`, `bug`.

## Issue-first for fixes

Open or link an issue for bugfix PRs. Use `Fixes #123` / `Closes #123` in the PR body.

## PR expectations

- Keep PRs small and focused
- Fill the PR template; empty or placeholder-only descriptions may be closed
- Do not paste large AI-generated essays - short, human explanations only
- Say how you verified the change
- Titles: conventional commits (`feat:`, `fix:`, `docs:`, …)

## Local checks

```bash
make lint
make test
# or the full gate:
make check-all
```

## Questions

If unsure whether a change would be accepted, ask in an issue before writing a large PR.
