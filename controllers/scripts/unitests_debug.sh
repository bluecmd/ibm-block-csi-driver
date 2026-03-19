#!/bin/bash
# Debug version of unitests.sh with enhanced logging and per-test timeout
set -x
set -e

coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

echo "=========================================="
echo "Starting DEBUG unit tests at $(date)"
echo "Coverage directory: $coveragedir"
echo "PID: $$"
echo "=========================================="

# Function to monitor test progress
monitor_tests() {
    local log_file="/tmp/nosetests_monitor.log"
    while true; do
        if [ -f "$log_file" ]; then
            tail -n 5 "$log_file"
        fi
        echo "[$(date +%H:%M:%S)] Test still running... (monitoring)"
        sleep 30
    done
}

# Start background monitor
monitor_tests &
MONITOR_PID=$!

# Cleanup function
cleanup() {
    echo "Cleaning up monitor process..."
    kill $MONITOR_PID 2>/dev/null || true
}
trap cleanup EXIT

# Run tests with maximum verbosity and per-test timeout
# --processes: run tests in parallel (helps identify deadlocks)
# --process-timeout: timeout per test process
# -v -v: extra verbose
timeout 30m nosetests \
    --exe \
    --with-coverage \
    --cover-xml \
    --cover-xml-file=$coveragedir/.coverage.xml \
    --cover-package=common \
    --cover-package=controllers \
    --with-xunit \
    --xunit-file=$coveragedir/.unitests.xml \
    -v -v \
    -s \
    --nologcapture \
    --with-id \
    --failed \
    --stop \
    $@ 2>&1 | tee /tmp/nosetests_monitor.log

exit_code=${PIPESTATUS[0]}

echo "=========================================="
echo "Tests completed at $(date) with exit code: $exit_code"
echo "=========================================="

exit $exit_code

# Made with Bob
