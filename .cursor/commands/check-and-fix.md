# Check and Fix

Run `make check` and fix all issues including failed tests.

1. Run `make check` to see all problems (runs fmt, build, lint-fix, test)
2. Fix all compilation errors, lint issues, and test failures
3. For test failures, read the failing test and the code under test to understand the root cause
4. Apply fixes to the source code (not the tests) unless the test itself is wrong
5. Re-run the specific failing target (`make build`, `make lint`, or `make test`) to verify each fix
6. Repeat until `make check` passes cleanly
