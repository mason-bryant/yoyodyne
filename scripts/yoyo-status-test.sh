#!/usr/bin/env bash
#
# yoyo-status-test.sh - check that bin/yoyo-status delegates to `yoyo status`
# and derives nothing itself.
#
#   scripts/yoyo-status-test.sh
#
# The tool used to read the state directory and derive run, conversation,
# branch-review, and exchange state out of it, and this suite checked those
# derivations against a fabricated state directory. The derivations are gone:
# the tool is bound by surfaces-project-one-read-model like any other operator
# surface, so it is a wrapper for the binary now. What is left to check is that
# it is one, and that no derivation has crept back into it.
#
# It needs bash and nothing else. `yoyo` is stubbed rather than built, so this
# never invokes the real binary, never reads an operator's state, and says what
# the wrapper passed on rather than what the binary made of it.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
status="$repository/bin/yoyo-status"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyo-status-test.XXXXXX")"
failures=0
skips=0

trap 'rm -rf "$scratch"' EXIT

# Run from a directory that is not a checkout, so nothing here can reach the
# repository's own configuration or state by accident.
cd "$scratch"

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }
skip()   { printf '  SKIPPED: %s\n' "$*"; skips=$((skips + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

# A `yoyo` that reports what it was asked rather than answering it. The wrapper
# execs whatever `yoyo` is on PATH, so this is what tells the difference between
# delegating and doing the work.
mkdir -p "$scratch/stub"
cat > "$scratch/stub/yoyo" <<'STUB'
#!/usr/bin/env bash
printf 'stub yoyo called with:'
printf ' %s' "$@"
printf '\n'
STUB
chmod +x "$scratch/stub/yoyo"

step "the wrapper delegates to yoyo status"
delegated="$(PATH="$scratch/stub:$PATH" "$status" 2>&1 || true)"
contains "$delegated" "stub yoyo called with: status" \
  "no arguments reaches \`yoyo status\`"
delegated="$(PATH="$scratch/stub:$PATH" "$status" yoyodyne-ifd.90 --json 2>&1 || true)"
contains "$delegated" "stub yoyo called with: status yoyodyne-ifd.90 --json" \
  "arguments are passed through in the order they were given"

step "a retired flag says where the capability went"
for flag in -l -c -L --runs --chats --reviews --raw --no-follow; do
  retired="$(PATH="$scratch/stub:$PATH" "$status" "$flag" 2>&1 || true)"
  contains "$retired" "yoyo-status is retired: $flag" "$flag is named as retired"
  missing "$retired" "stub yoyo called with" "$flag reaches the binary as nothing"
done
retired="$(PATH="$scratch/stub:$PATH" "$status" -c 2>&1 || true)"
contains "$retired" "yoyo cost" "the cost flag names the verb that prices work now"
contains "$retired" "yoyodyne-ifd.63" "following a stream live is said to be covered by nothing"
# The status the wrapper exits with is the one a misused command exits with, so
# a script that ran the old flag fails rather than reading an empty report as an
# empty state directory.
set +e
PATH="$scratch/stub:$PATH" "$status" -c >/dev/null 2>&1
retired_status=$?
set -e
if [ "$retired_status" = "2" ]; then
  pass "a retired flag exits 2, the way a misused command does"
else
  fail "a retired flag exits 2, the way a misused command does -- got status $retired_status"
fi

step "yoyo missing is said rather than left to the shell"
# A path with a shell on it and no yoyo. An empty one would fail at the
# interpreter instead, which tests the shebang rather than the wrapper.
bare="/usr/bin:/bin"
if PATH="$bare" command -v yoyo >/dev/null 2>&1; then
  skip "a missing binary is named: this machine has yoyo on $bare"
else
  absent="$(PATH="$bare" "$status" 2>&1 || true)"
  contains "$absent" "yoyo is not on PATH" "a missing binary is named"
  contains "$absent" "go install" "and the way to install it is named with it"
fi

step "nothing in the wrapper derives state for itself"
# This is the claim the architect's ruling turns on, so it is checked rather
# than trusted: a derivation that crept back in would be a second surface
# computing what the read model already computes, which is what
# surfaces-project-one-read-model forbids. The tools are what any derivation
# here would have to be written with, and the state root is what it would have
# to read.
# The wrapper's own comments say what it used to do with each of these, so only
# the lines that would run are looked at.
running="$(grep -v '^[[:space:]]*#' "$status" || true)"
for tool in jq awk sed tail; do
  missing "$running" "$tool " "the wrapper runs no $tool"
done
missing "$running" "YOYODYNE_STATE_HOME" "the wrapper reads no state directory"
missing "$running" "products/" "the wrapper reads nothing under a product"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'bin/yoyo-status delegates to yoyo status and derives nothing\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
if [ "$skips" != "0" ]; then
  printf '%d claim(s) this environment could not exercise, named above\n' "$skips"
fi
exit "$failures"
