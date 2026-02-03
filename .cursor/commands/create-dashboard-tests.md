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
make test-dashboard         # CI mode (run once)
make test-dashboard-watch   # Development (auto-rerun)
make test-dashboard-ui      # Interactive debugging
make test-dashboard-build   # TypeScript check
make test-dashboard-all     # Build + tests
make test                   # All project tests
```

## Test Organization

Place tests in `dashboard/src/test/`:
- `utils/` - Utility function tests
- `services/` - API service tests
- `components/` - Component tests (sparingly!)
- `hooks/` - Custom hook tests

## Priority Guide

1. **Complex Business Logic** ⭐⭐⭐ - Most valuable
   - Parsers: `conversationParser.ts`, `chatFlowParser.ts`
   - Filters and search logic
   - Data transformations

2. **State Management** ⭐⭐
   - Custom hooks with complex async logic (e.g., `useVersionMonitor.ts`)
   - Context providers with non-trivial state
   - Skip simple useState wrappers

3. **API Services** ⭐⭐
   - Only if logic beyond fetch (retry, transformation, error handling)
   - Skip thin wrappers

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
- ✅ All tests pass: `make test-dashboard`
- ✅ TypeScript builds: `make test-dashboard-build`
- ✅ No linter errors in test files
- ✅ Tests add real value
- ✅ No brittle/implementation tests

**Better 10 excellent tests than 100 mediocre ones.**

## Examples in Codebase

- `dashboard/src/test/utils/conversationParser.test.ts` - Complex parsing
- `dashboard/src/test/services/api.test.ts` - API services
- `dashboard/src/test/components/` - Selective component testing
