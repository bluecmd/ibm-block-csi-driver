# Troubleshooting Hanging Unit Tests

## Problem
Unit tests sometimes hang during execution in Jenkins, specifically during the `nosetests` execution phase with no output or logs.

## Solutions Implemented

### 1. Enhanced Logging and Visibility
The main `controllers/scripts/unitests.sh` script now includes:
- **Verbose output** (`-v` flag): Shows which test is currently running
- **Stdout capture disabled** (`-s` flag): Allows print statements to appear immediately
- **No log capture** (`--nologcapture`): Ensures logging output is visible in real-time
- **Timestamps**: Shows when tests start and complete

### 2. Timeout Mechanisms (Multi-Layer)
- **Inner timeout** (30 minutes): `timeout 30m` in `unitests.sh` kills nosetests if it runs too long
- **Outer timeout** (35 minutes): `timeout 35m` in `run_unitests.sh` kills the entire container if needed
- This dual-layer approach ensures tests never hang indefinitely

### 3. Debug Mode
For troubleshooting specific hanging issues, use the debug scripts:

```bash
# Run tests in debug mode
./scripts/run_unitests_debug.sh build/reports
```

Debug mode provides:
- Extra verbose output (`-v -v`)
- Background monitoring that prints status every 30 seconds
- Test IDs for tracking which tests have run
- `--stop` flag to stop on first failure
- Full output logging to `/tmp/nosetests_monitor.log`

## Usage

### Normal Mode (Production)
```bash
# In Jenkins or locally
./scripts/run_unitests.sh build/reports
```

### Debug Mode (Troubleshooting)
```bash
# When tests are hanging
./scripts/run_unitests_debug.sh build/reports
```

## What to Check When Tests Hang

1. **Check the last test that ran**: The verbose output will show the last test name before hanging
2. **Review timeout messages**: Exit code 124 indicates a timeout occurred
3. **Check for deadlocks**: Look for tests that involve threading, locks, or external connections
4. **Resource issues**: Ensure the container has sufficient memory and CPU

## Common Causes of Hanging Tests

1. **Infinite loops** in test code or code under test
2. **Deadlocks** in multi-threaded code
3. **Blocking I/O** waiting for network/storage that never responds
4. **Mock issues** where mocks don't return as expected
5. **Resource exhaustion** (memory, file descriptors, etc.)

## Exit Codes

- `0`: Tests passed successfully
- `1`: Tests failed (but completed)
- `124`: Timeout occurred (tests hung)
- Other: Various error conditions

## Recommendations

1. **Always use timeouts** in production CI/CD pipelines
2. **Enable verbose mode** to identify problematic tests
3. **Use debug mode** when investigating specific hanging issues
4. **Review test isolation**: Ensure tests don't depend on each other or leave resources locked
5. **Add per-test timeouts** for tests that interact with external systems

## Files Modified

- `controllers/scripts/unitests.sh` - Main test script with timeouts and verbose output
- `scripts/run_unitests.sh` - Container runner with timeout
- `controllers/scripts/unitests_debug.sh` - Debug version with enhanced monitoring
- `scripts/run_unitests_debug.sh` - Debug container runner

## Future Improvements

Consider adding:
- Per-test timeout decorators in Python code
- Parallel test execution with `--processes` flag (requires test isolation)
- Test result caching to skip passing tests on subsequent runs
- Automatic retry of flaky tests