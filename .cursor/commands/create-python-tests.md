# Writing Tests for Python LLM Service

## Running Tests

### From Project Root

**All tests:**
- `make test` - Run all tests (Go + Python + Dashboard)
- `make test-go` - Run all Go tests
- `make test-python` - Run all Python tests

**Python-specific:**
- `make test-llm` - Run LLM service tests
- `make test-llm-unit` - Run unit tests only
- `make test-llm-integration` - Run integration tests only
- `make test-llm-coverage` - Run with coverage report

**Go-specific:**
- `make test-go-coverage` - Run Go tests with coverage report

### From `llm-service/` Directory

- **All tests**: `uv run pytest tests/ -v`
- **Unit tests only**: `uv run pytest tests/ -m unit -v`
- **Integration tests only**: `uv run pytest tests/ -m integration -v`
- **Coverage**: `uv run pytest tests/ --cov=llm --cov-report=term-missing`
- **Specific file**: `uv run pytest tests/test_servicer.py -v`
- **Specific test**: `uv run pytest tests/test_servicer.py::TestLLMServicer::test_generate_success -v`
- **Show print statements**: `uv run pytest -s`
- **Stop on first failure**: `uv run pytest -x`

## Critical Rules

### 1. Be Pragmatic
Write tests that add real value. If a test requires overly complex mocking or doesn't validate meaningful behavior, skip it. It's better to have no test than a brittle one that breaks with every minor change. If fighting with mocks, consider an integration test instead.

### 2. Test Behavior, Not Implementation
Focus on what the code does, not how. Don't test that specific internal methods were called or verify exact sequences of internal function calls.

### 3. No Historical Comments in Code
No `# Added in TASK-0025` or `# Testing new implementation`. Historical context belongs in git commits. Tests should be self-explanatory.

### 4. Extend Existing Tests Before Creating New Ones
When code already has test coverage, prefer modifying or extending existing tests to cover new behavior rather than creating new test functions that largely duplicate what's already there. Add cases to existing parameterized tests when the scenario fits.

### 5. All Tests Must Pass
New tests are done ONLY when they ALL pass 100%. Don't leave failing tests. If a test is too complex to get right, don't write it.

### 6. No Documentation Files Unless Explicitly Requested
DO NOT create summary documents, README files, or any markdown documentation files (like test coverage summaries, test reports, etc.) unless the user explicitly asks for them. Focus exclusively on creating the actual test code.


## Pytest Markers

Tests are organized using pytest markers:
- `@pytest.mark.unit` - Unit tests (fast, no external dependencies, mocked)
- `@pytest.mark.integration` - Integration tests (may require real services, slower)

Apply markers at the module level using `pytestmark = pytest.mark.unit` at the top of the test file.

## Project Conventions

- **Test framework**: Use `pytest` with standard assertions
- **Mocking**: Use `unittest.mock` (patch, MagicMock, Mock) or `pytest-mock` if available
- **Parameterized tests**: Use `@pytest.mark.parametrize` for multiple similar cases
- **Fixtures**: Use `@pytest.fixture` for shared setup/teardown
- **Async tests**: Use `@pytest.mark.asyncio` for async functions (if pytest-asyncio is installed)
- **Test organization**: `tests/` directory mirroring the `llm/` structure
  - Unit tests: `tests/test_servicer.py`, `tests/providers/test_registry.py`
  - Integration tests: `tests/integration/test_grpc_service.py`
- **Test naming**: `test_*` for functions, `Test*` for classes

### LLM Service Specific

- **gRPC testing**: Use `grpc.aio` test utilities or mock the gRPC context
- **Provider mocking**: Mock external LLM API calls (google-genai SDK)
- **Stream testing**: Test async streaming responses properly
- **Configuration**: Use fixtures for test configurations and environment variables

Look at similar patterns in existing tests:
- Parameterized tests: Check other Python projects for `@pytest.mark.parametrize` examples
- Async streaming: Test gRPC streaming with async iterators
- Provider registry: Test provider registration and lookup

## Approach

1. **Read the implementation** - understand the public API, expected behavior, and error handling
2. **Check existing tests** for similar patterns in the same or related modules
3. **Determine what to test**: happy path, edge cases, error handling, async behavior if applicable
4. **Choose the right level**: unit tests for logic, integration tests for gRPC/service interactions
5. **Write mostly unit tests, some integration tests** - each test independent, no execution order dependencies

## Pitfalls

- Don't over-mock - keep it simple or test at a different level
- Don't test the framework (pytest, grpcio, pydantic)
- Don't hardcode time values or depend on execution order
- Don't assert unrelated things in one test - split them
- Don't forget to test error cases and edge conditions
- Don't make real API calls in unit tests - always mock external services
- Don't leave `print()` statements - use logging or remove them

## Setup Requirements

If tests don't exist yet, you may need to:
1. Create `tests/` directory structure
2. Add test dependencies to `pyproject.toml`:
   ```toml
   [project.optional-dependencies]
   test = [
       "pytest>=8.0.0",
       "pytest-asyncio>=0.23.0",
       "pytest-cov>=4.1.0",
       "pytest-mock>=3.12.0",
   ]
   ```
3. Create `pytest.ini` or `pyproject.toml` pytest configuration if needed

---

**BE PRACTICAL. CREATE ONLY TESTS THAT BRING REAL VALUE. DO NOT DUPLICATE TESTS.**

**DO NOT CREATE DOCUMENTATION OR SUMMARY FILES UNLESS EXPLICITLY REQUESTED.**

**Now create tests for the LLM service following the above guidelines.**
