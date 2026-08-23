#!/usr/bin/env bash
#
# bd-notes-guard-test.sh - exercise scripts/bd-notes-guard.sh against fabricated
# hook payloads and a stub tracker.
#
#   scripts/bd-notes-guard-test.sh
#
# The guard is a decision made from two inputs -- the hook payload on stdin and
# what `bd show` says about an item -- so both can be fabricated, and neither
# this repository's real tracker nor a real session is read. The stub bd lives
# in a temporary directory put at the front of PATH and removed on exit.
#
# What is under test is a refusal, and a refusal nobody exercises is a refusal
# nobody has. This one first fires on the day somebody is about to destroy a
# record, which is the worst possible day to find out it was wired up wrong --
# and it can be wrong in two directions, since a guard that refuses ordinary
# work is removed as surely as one that refuses nothing.
#
# Requires bash and python3. Without python3 the guard allows everything by
# design, so the suite names the claims it therefore did not exercise rather
# than passing them.

set -uo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
guard="$repository/scripts/bd-notes-guard.sh"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/bd-notes-guard-test.XXXXXX")"
failures=0
skips=0

trap 'rm -rf "$scratch"' EXIT

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

# The attributed item, the unattributed one, and an id the stub tracker does not
# know, which is how a failing read is produced without breaking bd itself.
attributed="yoyodyne-ifd.45"
unattributed="yoyodyne-ifd.83"
unknown="yoyodyne-ifd.9999"
goal="Run development nearly autonomously."

# A stub bd answering `show <id> --json` in the shape internal/beads decodes: a
# list of items, each carrying its id and its notes. Anything else exits
# non-zero, which is what an unknown id has to look like.
mkdir -p "$scratch/bin"
cat > "$scratch/bin/bd" <<STUB
#!/usr/bin/env bash
if [ "\$1" != "show" ]; then echo "stub bd: unexpected \$*" >&2; exit 64; fi
case "\$2" in
  ($attributed)
    printf '[{"id":"$attributed","notes":"Admitted by the product manager.\\\\nGoal served: $goal"}]\\n' ;;
  ($unattributed)
    printf '[{"id":"$unattributed","notes":"Filed directly, naming no goal."}]\\n' ;;
  (*)
    echo "stub bd: no issue \$2" >&2; exit 1 ;;
esac
STUB
chmod +x "$scratch/bin/bd"
export PATH="$scratch/bin:$PATH"

# A PATH with everything the guard's shell half needs and no python3 on it, for
# the two claims about what it does without an interpreter. Built by finding the
# externals it actually calls and linking those alone, so this stays a statement
# about python3 rather than about whatever else the directory happened to hold.
# `bash` is among them because a PATH-prefixed command is looked up on the PATH
# it sets, so leaving it out would fail to find the shell rather than the
# interpreter, and pass this for the wrong reason.
mkdir -p "$scratch/nopython"
for tool in bash cat dirname; do
  located="$(command -v "$tool" 2>/dev/null)"
  [ -n "$located" ] && ln -sf "$located" "$scratch/nopython/$tool"
done

have_python=0
command -v python3 >/dev/null 2>&1 && have_python=1

# One PreToolUse payload, built by python3 so the command is JSON-encoded the
# way Claude Code encodes it rather than the way this file guesses. The two
# claims that survive without python3 use commands needing no encoding, so a
# plain substitution covers them.
payload() {
  local tool="$1" command="$2"
  if [ "$have_python" = "1" ]; then
    TOOL="$tool" COMMAND="$command" python3 -c '
import json, os
print(json.dumps({
    "hook_event_name": "PreToolUse",
    "tool_name": os.environ["TOOL"],
    "tool_input": {"command": os.environ["COMMAND"]},
}))'
  else
    printf '{"hook_event_name":"PreToolUse","tool_name":"%s","tool_input":{"command":"%s"}}\n' "$tool" "$command"
  fi
}

# Run the guard over one payload, keeping its exit status and what it said. The
# status is the whole decision -- 2 blocks the call, anything else does not --
# so it is captured rather than left to end this suite.
verdict=""
status=0
decide() {
  verdict="$(payload "$1" "$2" | bash "$guard" 2>&1)"
  status=$?
}

allows() {
  decide "Bash" "$1"
  if [ "$status" = "0" ]; then pass "$2"; else fail "$2 -- exited $status: $verdict"; fi
}

refuses() {
  decide "Bash" "$1"
  if [ "$status" = "2" ]; then pass "$2"; else fail "$2 -- exited $status: $verdict"; fi
}

if [ "$have_python" = "0" ]; then
  step "the guard's decisions"
  skip "every claim about what the guard refuses: this environment has no python3, without which it allows everything by design"
else
  step "a write that would destroy an attribution is refused"
  refuses "bd update $attributed --notes=\"Fresh evidence 2026-08-18: a chat turn failed.\"" \
    "replacing the notes of an attributed item is refused"
  contains "$verdict" "$goal" "the refusal quotes the goal statement that would have been lost"
  contains "$verdict" "--append-notes" "the refusal names the flag to use instead"
  contains "$verdict" "$attributed" "the refusal names the item"

  step "the flag is recognised however it is spelled and wherever it sits"
  refuses "bd update $attributed --notes \"separate token\"" \
    "--notes as its own token is the same write"
  refuses "cd /tmp && bd update $attributed --notes=\"after a cd\"" \
    "a write later in a compound command is still found"
  refuses "/opt/homebrew/bin/bd update $attributed --notes=\"absolute path\"" \
    "bd reached by an absolute path is still bd"
  refuses "bd update --json $attributed --notes=\"flags first\"" \
    "the item is found among the flags rather than by position"

  step "a flag's value is not mistaken for an item"
  # The way this guard would get itself removed: reading `closed` as a second id
  # sends `bd show closed --json`, which fails, and a refusal built on that would
  # be refusing ordinary work over the guard's own misparse.
  refuses "bd update $attributed --status closed --notes=\"valued flag before\"" \
    "a valued flag in separate-token form does not become a second id"
  contains "$verdict" "$goal" "and the refusal is still about the item, not about the flag value"
  refuses "bd update $attributed --notes=\"valued flag after\" --assignee somebody" \
    "a valued flag after the write is skipped too"
  # A valued flag the list does not know still leaves its value looking like an
  # id. That is absorbed rather than fatal, because a sibling resolved.
  refuses "bd update $attributed --not-a-known-flag closed --notes=\"unknown valued flag\"" \
    "an unreadable id is ignored where another id in the same write resolved"
  contains "$verdict" "$goal" "and that write is still judged on the item that did resolve"
  allows "bd update $unattributed --status closed --notes=\"no goal to lose\"" \
    "the same shape against an unattributed item is still allowed"

  step "the safe spelling is not caught"
  allows "bd update $attributed --append-notes=\"checks passed\"" \
    "--append-notes on an attributed item is allowed"
  allows "bd update $attributed --append-notes \"checks passed\"" \
    "--append-notes as its own token is allowed too"

  step "a write that carries the attribution across is allowed"
  allows "bd update $attributed --notes=\"Rewritten record.
Goal served: $goal\"" \
    "replacing notes that end with the same attribution is allowed"
  allows "bd update $attributed --notes=\"Goal served: $goal
A later line that is not an attribution.\"" \
    "the attribution need not be last, only present and unchanged"
  refuses "bd update $attributed --notes=\"Goal served: Something nobody agreed to.\"" \
    "swapping in a different goal is refused rather than counted as preserving"

  step "an item with no attribution has nothing to protect"
  allows "bd update $unattributed --notes=\"Filed directly, still naming no goal.\"" \
    "replacing the notes of an unattributed item is allowed"

  step "a tracker read that cannot be completed refuses rather than assumes"
  refuses "bd update $unknown --notes=\"an item the tracker does not know\"" \
    "a failing bd show on a replacing write is refused"
  contains "$verdict" "could not read" "the refusal says the guard could not establish what was there"

  step "everything that is not this is left alone"
  allows "git commit -m \"--notes is only a word here\"" \
    "a command that is not a bd update is allowed"
  allows "bd show $attributed --json" \
    "reading an item is allowed"
  allows "bd close $attributed --reason=\"done\"" \
    "another bd verb is allowed"
  decide "Read" "bd update $attributed --notes=\"not a Bash call\""
  if [ "$status" = "0" ]; then
    pass "a payload for another tool is allowed"
  else
    fail "a payload for another tool is allowed -- exited $status: $verdict"
  fi

  step "a payload the guard cannot read is allowed rather than blocking the session"
  # A hook that refuses what it failed to parse takes the session down with it,
  # and the thing it would be protecting is not in a payload it cannot read.
  verdict="$(printf 'not json at all --notes\n' | bash "$guard" 2>&1)"; status=$?
  if [ "$status" = "0" ]; then
    pass "an unparseable payload mentioning --notes is allowed"
  else
    fail "an unparseable payload mentioning --notes is allowed -- exited $status: $verdict"
  fi
  verdict="$(printf '{"tool_name":"Bash","tool_input":{"command":"bd update x --notes=\\"cut off}\n' | bash "$guard" 2>&1)"; status=$?
  if [ "$status" = "0" ]; then
    pass "a truncated payload is allowed"
  else
    fail "a truncated payload is allowed -- exited $status: $verdict"
  fi
fi

step "a command with no --notes in it never reaches the interpreter"
# The fast path, which is what keeps a hook running before every Bash call from
# spawning python3 before every Bash call. Checked by taking python3 away: with
# none on PATH this still has to be allowed silently, where a command carrying
# --notes says out loud that it went unchecked.
quiet="$(payload "Bash" "ls -la" | PATH="$scratch/nopython" bash "$guard" 2>&1)"; status=$?
if [ "$status" = "0" ] && [ -z "$quiet" ]; then
  pass "a command with no --notes is allowed silently, with no interpreter"
else
  fail "a command with no --notes is allowed silently -- exited $status, said: $quiet"
fi

step "without python3 the guard allows the write and says it went unchecked"
# The direction this half fails in. Blocking every Bash call over a missing
# interpreter would get the hook removed, which loses the guard rather than
# tightening it -- so it allows, and is loud about what it did not check.
unchecked="$(payload "Bash" "bd update $attributed --notes=x" | PATH="$scratch/nopython" bash "$guard" 2>&1)"; status=$?
if [ "$status" = "0" ]; then
  pass "a replacing write is allowed where the guard cannot examine it"
else
  fail "a replacing write is allowed where the guard cannot examine it -- exited $status"
fi
contains "$unchecked" "went unchecked" "and the caller is told the write was not examined"
missing "$unchecked" "refused" "an unexamined write is not reported as a refusal"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'bd-notes-guard refuses the writes that destroyed twelve attributions, and allows the rest\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
if [ "$skips" != "0" ]; then
  printf '%d claim(s) this environment could not exercise, named above\n' "$skips"
fi
exit "$failures"
