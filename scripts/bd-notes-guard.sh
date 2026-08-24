#!/usr/bin/env bash
# Refuses the one tracker command that destroys a work item's attribution:
# `bd update <id> --notes=…`, which replaces an item's notes wholesale rather
# than appending to them. An attribution is a single `Goal served:` line in
# those notes and is read from there and nowhere else, so a writer that replaces
# them takes the attribution with it. That is not hypothetical: twelve items
# lost a recorded goal exactly this way, each matched to the command that did it
# in docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md.
#
# The harness's own writer never does this -- beads.Client.Update passes
# `--append-notes` (internal/beads/client.go) -- so every loss came from a raw
# `bd update` typed in an agent session. This guard is what stands where that
# discipline was being asked for rather than enforced.
#
# It runs as a Claude Code PreToolUse hook on Bash. Two populations get a shell
# and only one is wired to it today: harness developer runs, through
# internal/backend/claudecode (developerSandboxSettings). Interactive sessions in
# a checkout are wired in .claude/settings.json, which carries no such stanza, so
# that population -- the one every recorded loss actually came from -- is still
# unguarded. docs/configuration.md says the same; keep the two in step rather
# than letting this comment claim coverage that does not exist.
#
# Exit 2 is what blocks a tool call and puts this script's stderr in front of the
# model; any other non-zero exit would be reported and then ignored, which is the
# failure mode this guard exists to avoid, so nothing here exits non-zero for any
# other reason.
set -uo pipefail

payload=$(cat)

# Matched against the raw hook payload rather than a JSON-decoded command,
# deliberately: this has no jq or python dependency to be missing, and the way
# it is wrong is to refuse a command that merely mentions the flag -- a `grep`
# for it in these very docs -- rather than to let a real one through. Refusing
# too much is recoverable in a sentence; letting one through loses a record
# nobody can tell was lost.
#
# `--append-notes` does not contain the substring `--notes`, so the safe writer
# needs no exception here, and `bd create --notes` is safe too -- it names an
# item that does not exist yet -- which is why `update` is required to match.
if printf '%s' "$payload" | grep -Eq '(^|[^[:alnum:]_-])bd[[:space:]]+update[[:space:]][^;&|]*--notes([^[:alnum:]]|$)'; then
	cat >&2 <<'REFUSAL'
Refused: `bd update … --notes` replaces a work item's notes wholesale.

An item's attribution is one `Goal served:` line inside those notes, so
replacing them destroys it. This has already cost twelve items their recorded
goal; each loss is matched to the command that caused it in
docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md.

Use `--append-notes` instead, which adds to the notes and destroys nothing:

    bd update <id> --append-notes="…"

If you genuinely must replace an item's notes, read the current value back
first and re-send everything you intend to keep -- and say in your summary that
you did it, so the record shows who rewrote it and why.
REFUSAL
	exit 2
fi

exit 0
