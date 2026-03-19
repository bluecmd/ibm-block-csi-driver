#!/bin/bash
# Debug version of run_unitests.sh for troubleshooting hanging tests
set -x
set -e

echo "=========================================="
echo "Building test container (DEBUG MODE) at $(date)"
echo "=========================================="

[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"

# Build the test container
podman build -f Dockerfile-controllers.test -t csi-controller-tests .

echo "=========================================="
echo "Running unit tests in DEBUG MODE at $(date)"
echo "Timeout: 35 minutes (container level)"
echo "This will show which test is running when it hangs"
echo "=========================================="

# Run with debug script and timeout
timeout 35m podman run \
    --entrypoint ./controllers/scripts/unitests_debug.sh \
    --rm \
    -t \
    $coverage \
    csi-controller-tests

exit_code=$?

echo "=========================================="
echo "Unit tests completed at $(date)"
echo "Exit code: $exit_code"
if [ $exit_code -eq 124 ]; then
    echo "ERROR: Tests timed out after 35 minutes!"
    echo "Check the output above to see which test was running when timeout occurred"
fi
echo "=========================================="

exit $exit_code

# Made with Bob
