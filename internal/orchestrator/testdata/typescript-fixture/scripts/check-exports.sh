#!/bin/sh
# Every module under src/ has to export something: a module nothing can import
# is dead weight in this project. Plain POSIX shell, so verifying it needs no
# compiler, no package manager, and no Node.js.
set -u

status=0
found=0
for source in src/*.ts; do
    [ -f "$source" ] || continue
    found=1
    if ! grep -q '^export ' "$source"; then
        echo "$source exports nothing" >&2
        status=1
    fi
done

if [ "$found" -eq 0 ]; then
    echo "no TypeScript sources found under src/" >&2
    exit 1
fi
exit "$status"
