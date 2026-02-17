# Writing Tests for Dashboard Functionality

## When to Use This Command

You'll typically use this after implementing new dashboard functionality. Create tests for the complex logic you just added.

**CRITICAL:** Tests for new functionality often reveal real bugs in the implementation. If a test fails:
1. **FIRST, analyze if it's a bug in the production code**
2. **FIX THE BUG in the implementation** - do NOT write tests that codify incorrect behavior
3. Only adjust tests if the implementation is correct and test expectations were wrong

**NEVER write tests that document/accept buggy behavior.** Tests must verify correct functionality, not enshrine bugs.

**If unsure - ASK!**

## Philosophy: Test What Matters

Write tests that prevent real bugs and give confidence during refactoring. Focus on complex logic prone to breaking.

**Test:**
- ✅ Complex business logic (parsing, transformations, filters)
- ✅ State management with intricate async flows
- ✅ API services with retry/error handling logic
- ✅ Components with complex conditional logic

**Skip:**
- ❌ Simple rendering/styling
- ❌ Trivial utilities
- ❌ Framework behavior
- ❌ Tests harder to maintain than the code itself

**Coverage is NOT a goal.** A few high-quality tests for critical logic beat dozens of brittle ones.

## Running Tests

**Always use `make` commands** - consistent for humans and CI:

```bash
make test-dashboard         # Full check: tests + TypeScript (CI)
make dashboard-test         # Vitest only (run once)
make dashboard-test-watch   # Development (auto-rerun)
make dashboard-test-build   # TypeScript check only
make dashboard-lint         # Lint dashboard code
make test                   # All project tests (Go + dashboard)
```

## Test Organization

Place tests in `web/dashboard/src/test/`:
- `utils/` - Utility function tests (parsers, formatters, persistence)
- `services/` - API and auth service tests
- `hooks/` - Custom hook tests
- `components/` - Component tests (sparingly!)

## Priority Guide

1. **Complex Business Logic** ⭐⭐⭐ - Most valuable
   - Parsers: `timelineParser.ts`, `contentParser.ts`, `traceHelpers.ts`
   - Filters and search logic: `search.ts`, `filterPersistence.ts`
   - Data formatting: `format.ts`, `yamlHighlighter.ts`

2. **State Management** ⭐⭐
   - Custom hooks with complex async logic (e.g., `useVersionMonitor.ts`)
   - Context providers with non-trivial state
   - Skip simple useState wrappers (e.g., `useAdvancedAutoScroll`)

3. **API Services** ⭐⭐
   - Error handling and retry logic in `api.ts`
   - Auth flow in `auth.ts`
   - Skip thin wrappers that just call endpoints

4. **Components** ⭐
   - Only if complex conditional rendering or intricate interactions
   - Skip presentational components

## Essential Standards

- Clear test names describing behavior
- Arrange-Act-Assert structure
- Use `it.each` for multiple similar cases
- Mock external dependencies (fetch, timers)
- Clean up after tests (restore mocks)
- **Enhance existing tests** when present - add cases to existing test files rather than creating duplicates

## Red Flags (Stop and Reconsider)

- Test longer than the code being tested
- Complex mock chains
- Testing implementation details (internal methods, prop passing)
- Tests break when implementation changes but behavior doesn't
- Testing that styling/framework works

## Checklist

- ✅ Complex logic tested
- ✅ All tests pass: `make dashboard-test`
- ✅ TypeScript builds: `make dashboard-test-build`
- ✅ No linter errors in test files
- ✅ Tests add real value
- ✅ No brittle/implementation tests

**Better 10 excellent tests than 100 mediocre ones.**

## Examples in Codebase

- `web/dashboard/src/test/utils/timelineParser.test.ts` - Complex event-to-FlowItem parsing
- `web/dashboard/src/test/utils/contentParser.test.ts` - Content type detection and parsing
- `web/dashboard/src/test/utils/traceHelpers.test.ts` - Trace data helpers and copy formatting
- `web/dashboard/src/test/services/api.test.ts` - API error handling and endpoint wiring
- `web/dashboard/src/test/services/auth.test.ts` - OAuth2 auth flow
- `web/dashboard/src/test/hooks/useVersionMonitor.test.ts` - Async hook with polling
- `web/dashboard/src/test/utils/filterPersistence.test.ts` - localStorage persistence
- `web/dashboard/src/test/utils/format.test.ts` - Timestamp/duration/token formatting
- `web/dashboard/src/test/utils/search.test.ts` - Search highlighting and filter detection
- `web/dashboard/src/test/utils/markdownComponents.test.ts` - Markdown syntax detection
- `web/dashboard/src/test/utils/yamlHighlighter.test.ts` - YAML highlighting with XSS prevention
