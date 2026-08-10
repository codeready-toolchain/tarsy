---
description: Remove AI code slop from the current branch vs default branch
---

Diff this branch against `master` and remove AI-generated slop introduced here:

- Comments a human would not add / inconsistent with the file
- Extra defensive try/catch abnormal for trusted call paths
- `any` casts to silence types
- Style inconsistent with surrounding code
- Unnecessary emoji

Keep behavior identical. End with a 1–3 sentence summary of edits only.

$ARGUMENTS
