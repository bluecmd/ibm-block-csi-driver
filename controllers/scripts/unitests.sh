#!/bin/bash
set -x
set -e

coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

echo "=========================================="
echo "Starting unit tests at $(date)"
echo "Coverage directory: $coveragedir"
echo "=========================================="

# Add verbose and progress output to nosetests
# -v: verbose output showing test names
# -s: don't capture stdout (allows print statements to show immediately)
# --nologcapture: don't capture logging output
# --nocapture: alias for -s
exec timeout 30m nosetests \
    --exe \
    --with-coverage \
    --cover-xml \
    --cover-xml-file=$coveragedir/.coverage.xml \
    --cover-package=common \
    --cover-package=controllers \
    --with-xunit \
    --xunit-file=$coveragedir/.unitests.xml \
    -v \
    -s \
    --nologcapture \
    $@
