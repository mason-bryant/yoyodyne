#!/bin/sh
# Sources are indented with two spaces. A tab in one is a failure this project
# gates on, and it is deliberately the second check: a run that reaches it has
# already had one check pass, so a stopped run is stopped by the check that
# failed rather than by there being checks at all.
set -u

tabbed=$(grep -l "$(printf '\t')" src/*.ts 2>/dev/null || true)
if [ -n "$tabbed" ]; then
    echo "these sources are indented with tabs rather than spaces:" >&2
    echo "$tabbed" >&2
    exit 1
fi
exit 0
