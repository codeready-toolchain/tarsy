# Writing Tests for Go Backend

## Running Tests

### From Project Root

**All tests:**
- `make test` - Run all tests (Go + Python + Dashboard)
- `make test-go` - Run all Go tests only
- `make test-go-coverage` - Run Go tests and open HTML coverage report

### Direct Go Commands

- **Specific package**: `go test ./pkg/queue/... -v`
- **Specific test**: `go test ./pkg/api -run TestExtractAuthor -v`
- **With coverage**: `go test -v -race -coverprofile=coverage.out ./...`
- **Race detector**: `go test -race ./...` (always use for concurrent code)
- **Show coverage**: `go tool cover -html=coverage.out`

## Critical Rules

### 1. Be Pragmatic
Write tests that add real value. If a test requires overly complex mocking or doesn't validate meaningful behavior, skip it. It's better to have no test than a brittle one that breaks with every minor change. If fighting with mocks, consider an integration test instead.

### 2. Test Behavior, Not Implementation
Focus on what the code does, not how. Don't test that specific internal methods were called or verify exact sequences of internal function calls.

### 3. No Historical Comments in Code
No `// Added in TASK-0025` or `// Testing new implementation`. Historical context belongs in git commits. Tests should be self-explanatory.

### 4. Extend Existing Tests Before Creating New Ones
When code already has test coverage, prefer modifying or extending existing tests to cover new behavior rather than creating new test functions that largely duplicate what's already there. Add cases to existing table-driven tests when the scenario fits.

### 5. All Tests Must Pass
New tests are done ONLY when they ALL pass 100%. Don't leave failing tests. If a test is too complex to get right, don't write it.

### 6. No Documentation Files Unless Explicitly Requested
DO NOT create summary documents, README files, or any markdown documentation files (like test coverage summaries, test reports, etc.) unless the user explicitly asks for them. Focus exclusively on creating the actual test code.


## Test Organization

- **Unit tests**: `*_test.go` - Fast, mocked, no external dependencies
- **Integration tests**: `*_integration_test.go` - Real database, slower, comprehensive

## Project Conventions

- **Test assertions**: Use `testify/assert` (non-fatal) and `testify/require` (fatal)
- **Table-driven tests**: Preferred for multiple similar cases with `t.Run()` subtests
- **Test helpers**: Always mark with `t.Helper()`
- **Cleanup**: Use `t.Cleanup()` for resource cleanup
- **Test database**: Uses `DATABASE_URL_TEST` env var (via `testdb.NewTestClient(t)`)
- **Race detection**: Always use `-race` flag when testing concurrent code

### Existing Test Patterns

- **Table-driven unit tests**: `pkg/config/validator_test.go`, `pkg/api/auth_test.go`
- **Worker unit tests**: `pkg/queue/worker_test.go`
- **Service integration tests**: `pkg/services/integration_test.go`
- **Database integration tests**: `pkg/queue/integration_test.go`
- **Concurrent behavior tests**: `pkg/queue/integration_test.go`

## Approach

1. **Read the implementation** - understand the public API, expected behavior, and error handling
2. **Check existing tests** for similar patterns in the same or related packages
3. **Determine what to test**: happy path, edge cases, error handling, concurrency if applicable
4. **Choose the right level**: unit tests for logic, integration tests for database/service interactions
5. **Write mostly unit tests, some integration tests** - each test independent, no execution order dependencies

## Pitfalls

- Don't over-mock - keep it simple or test at a different level
- Don't test the framework (Go stdlib, Echo, testify)
- Don't forget `-race` flag for concurrent code
- Don't hardcode time values or depend on execution order
- Don't assert unrelated things in one test - split them

---

**BE PRACTICAL. CREATE ONLY TESTS THAT BRING REAL VALUE. DO NOT DUPLICATE TESTS.**

**DO NOT CREATE DOCUMENTATION OR SUMMARY FILES UNLESS EXPLICITLY REQUESTED.**

**Now create tests for the new functionality following the above guidelines.**
