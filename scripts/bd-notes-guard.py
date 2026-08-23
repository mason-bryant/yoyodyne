"""Decide whether one Claude Code Bash tool call would destroy a goal attribution.

The deciding half of bd-notes-guard.sh, which is where the reasoning for the
whole guard is written down. This reads a PreToolUse hook payload on stdin and
exits 0 to allow the call or 2 to block it, with the reason on stderr.

Reached only for a payload that mentions --notes at all, so everything here is
allowed to be thorough: it runs a handful of times a session rather than before
every command.
"""

import json
import os
import shlex
import subprocess
import sys

# The prefix opening the line a work item records its goal on. It is
# goal.AttributionPrefix in internal/goal/goal.go, and the two spellings have to
# agree: what is guarded here is exactly what is read back there.
ATTRIBUTION_PREFIX = "Goal served:"

# Where one command ends and the next begins. A hook sees whole shell commands,
# and `cd somewhere && bd update ...` is one of them, so the arguments of a
# `bd update` have to stop at the operator that ends it rather than running on
# into whatever follows.
SEPARATORS = {"&&", "||", ";", "|", "&"}

# The `bd update` flags that take a value, for the separate-token spelling where
# the value is its own word. Without this list `bd update <id> --status closed`
# reads `closed` as a second item id, and the tracker read for it fails -- which
# would turn ordinary work into a refusal, and a guard that refuses ordinary
# work is removed as surely as one that refuses nothing. The `--flag=value`
# spelling needs no list, because the value is inside the token.
#
# The list being incomplete is survivable rather than silent: main() only
# refuses on an unreadable id where *no* id in the same write resolved, so a
# valued flag missing from here is absorbed as long as the real item was found.
VALUED_FLAGS = {
    "--acceptance",
    "--acceptance-criteria",
    "--append-notes",
    "--assignee",
    "--description",
    "--design",
    "--estimate",
    "--label",
    "--labels",
    "--metadata",
    "--notes",
    "--parent",
    "--priority",
    "--reason",
    "--status",
    "--title",
    "--type",
}

DIAGNOSIS = "docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md"

# How long one tracker read may take before the guard treats it as unanswerable.
# Generous, because refusing costs the caller a retry; bounded at all because a
# hook that hangs holds up the session it was meant to protect.
SHOW_TIMEOUT_SECONDS = 30


def named_in(notes):
    """The goal these notes record, or "".

    Mirrors goal.NamedIn: the last attribution wins, because an item acquires
    one by having it appended to notes nothing rewrites, so the newest line is
    the current claim and the ones before it are how it got there.
    """
    named = ""
    for line in (notes or "").split("\n"):
        line = line.strip()
        if not line.startswith(ATTRIBUTION_PREFIX):
            continue
        statement = line[len(ATTRIBUTION_PREFIX):].strip()
        if statement:
            named = statement
    return named


def replacing_writes(command):
    """Every (items, replacement) this command names in a `bd update --notes`.

    The items of one write are kept together rather than flattened, because
    whether an id that will not resolve is a typo or a misread flag value is a
    question only its siblings can answer.

    `--append-notes` is deliberately not treated as a replacement. It is the
    safe spelling, the one the harness itself uses, and the one a refused caller
    is sent to -- but it is in VALUED_FLAGS, so its value is still skipped
    rather than mistaken for an id.
    """
    try:
        tokens = shlex.split(command)
    except ValueError:
        # An unbalanced quote is not a command any shell would run either, so
        # there is no write here to guard.
        return []
    writes = []
    index = 0
    while index < len(tokens) - 1:
        # The binary is matched by its base name, so an absolute path to bd, or
        # one reached through a wrapper directory, is still bd.
        if os.path.basename(tokens[index]) != "bd" or tokens[index + 1] != "update":
            index += 1
            continue
        cursor = index + 2
        items, replacement, replaces = [], "", False
        while cursor < len(tokens) and tokens[cursor] not in SEPARATORS:
            token = tokens[cursor]
            if token == "--notes":
                replaces = True
                if cursor + 1 < len(tokens):
                    replacement = tokens[cursor + 1]
                    cursor += 1
            elif token.startswith("--notes="):
                replaces, replacement = True, token[len("--notes="):]
            elif token in VALUED_FLAGS:
                # Its value is the next word, and a value is not an id.
                cursor += 1
            elif not token.startswith("-"):
                items.append(token)
            cursor += 1
        if replaces and items:
            writes.append((items, replacement))
        index = cursor
    return writes


def recorded_notes(item):
    """What the tracker currently holds in this item's notes.

    Raises LookupError where that cannot be established, which the caller
    treats as a refusal rather than as an absence.
    """
    try:
        answer = subprocess.run(
            ["bd", "show", item, "--json"],
            capture_output=True,
            text=True,
            timeout=SHOW_TIMEOUT_SECONDS,
        )
    except OSError as failure:
        raise LookupError(str(failure))
    except subprocess.TimeoutExpired:
        raise LookupError("bd show did not answer within %ds" % SHOW_TIMEOUT_SECONDS)
    if answer.returncode != 0:
        raise LookupError(
            (answer.stderr or answer.stdout).strip()
            or "bd show exited %d" % answer.returncode
        )
    try:
        payload = json.loads(answer.stdout)
    except ValueError as failure:
        raise LookupError("bd show did not answer with JSON: %s" % failure)
    # bd answers a show with a list of items, the shape internal/beads decodes.
    # A bare object is accepted too, so a tracker that tightens to the singular
    # form does not silently turn every guarded write into a refusal.
    found = payload if isinstance(payload, list) else [payload]
    for candidate in found:
        if isinstance(candidate, dict) and candidate.get("id") == item:
            return candidate.get("notes") or ""
    if len(found) == 1 and isinstance(found[0], dict):
        return found[0].get("notes") or ""
    raise LookupError("bd show answered with no item called %s" % item)


def refuse(reason):
    sys.stderr.write("bd-notes-guard: refused.\n\n" + reason + "\n")
    sys.exit(2)


def unreadable(item, failure):
    refuse(
        "This replaces %s's notes wholesale, and the guard could not read what\n"
        "is in them now to see whether an attribution would go with them:\n\n"
        "  bd show %s --json -- %s\n\n"
        "Refusing costs one retry with --append-notes. Allowing costs a goal\n"
        "attribution nobody notices is gone, which is how twelve of them were\n"
        "lost (%s).\n\n"
        "Use --append-notes, or fix the tracker read and try again."
        % (item, item, failure, DIAGNOSIS)
    )


def would_destroy(item, recorded):
    refuse(
        "`bd update <id> --notes=` replaces the notes field wholesale, and %s's\n"
        "notes are where its goal attribution lives:\n\n"
        "  %s %s\n\n"
        "This write does not carry that line, so it would destroy the record.\n"
        "Twelve attributions were lost exactly this way (%s).\n\n"
        "Add to the notes instead:\n\n"
        '  bd update %s --append-notes="<your text>"\n\n'
        "If the notes really must be replaced, end your text with the line above,\n"
        "verbatim, and this will allow it."
        % (item, ATTRIBUTION_PREFIX, recorded, DIAGNOSIS, item)
    )


def main():
    try:
        payload = json.load(sys.stdin)
    except ValueError:
        # Not a payload this hook understands. Allowing is the same answer it
        # gives for any command it cannot read, and for the same reason.
        return 0
    if not isinstance(payload, dict) or payload.get("tool_name") != "Bash":
        return 0
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        return 0
    command = tool_input.get("command") or ""
    for items, replacement in replacing_writes(command):
        resolved, unresolved = [], []
        for item in items:
            try:
                resolved.append((item, recorded_notes(item)))
            except LookupError as failure:
                unresolved.append((item, failure))
        if unresolved and not resolved:
            # Nothing in this write could be read, so the guard cannot tell an
            # attributed item from an unattributed one and refuses. Where some
            # sibling *did* resolve, the one that did not is far likelier to be
            # a flag value this parse misread than an item, and refusing on it
            # would be refusing ordinary work over the guard's own blind spot.
            unreadable(*unresolved[0])
        for item, notes in resolved:
            recorded = named_in(notes)
            if not recorded:
                # Nothing to destroy. An item that never named a goal is not
                # made worse by a replacing write, and refusing it would be the
                # guard protecting a record that does not exist.
                continue
            if named_in(replacement) == recorded:
                # The attribution is carried across verbatim, so the record
                # survives the replacement and there is nothing to protect.
                continue
            would_destroy(item, recorded)
    return 0


if __name__ == "__main__":
    sys.exit(main())
