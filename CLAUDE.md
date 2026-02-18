# TARSy Development Guidelines

## 🎯 General Coding Standards (All Languages)

**MANDATORY**: For ALL coding tasks regardless of programming language, apply:

- `.cursor/skills/karpathy-guidelines/SKILL.md`

These emphasize: think before coding, simplicity over complexity, surgical changes, goal-driven execution.

## 🔍 Language-Specific Skills (Auto-Discovery)

When working with code in a specific programming language:

### Before writing, editing, or reviewing code

1. **Scan** `.cursor/skills/` directory for skills matching `<language>-*` pattern
2. **Read and apply** ALL matching skill files
3. **Apply patterns** from those skills automatically

### Go (Golang)

Pattern: `golang-*`

Example current skills (scan directory for latest):

- `golang-context-patterns` - Context handling (background ctx for writes, etc.)
- `golang-error-handling` - Error wrapping, sentinel errors, custom errors
- `golang-testing-patterns` - Table-driven tests, subtests

**Future skills like `golang-performance`, `golang-concurrency` will be automatically discovered and applied.**

### Other Languages

Follow the same pattern:

- Python: scan for `python-*`
- TypeScript: scan for `typescript-*`
- Rust: scan for `rust-*`
- etc.

## 📋 Available Commands

Commands in `.cursor/commands/` are available for explicit invocation:

- `/analyze-only` - Analysis without code changes
- `/create-backend-tests` - Generate Go backend tests
- `/create-dashboard-tests` - Generate dashboard tests
- `/create-python-tests` - Generate Python tests
- `/lint-and-fix` - Run linter and fix issues
- `/pr-comment` - PR review context
- `/research` - Internet research

**Note**: Commands are only executed when explicitly requested by the user.

## 🚀 How This Works

This configuration is **future-proof**:

- Add `golang-concurrency/SKILL.md` → automatically applied to Go code
- Add `python-testing/SKILL.md` → automatically applied to Python code
- No need to update CLAUDE.md when adding new skills

**Naming convention**: `<language>-<topic>/SKILL.md`

## Application Order

1. **Always first**: karpathy-guidelines
2. **Then**: All `<language>-*` skills for the current language (discovered automatically)
3. **On request**: Specific commands via `/command-name`

Apply skills automatically without being asked.
