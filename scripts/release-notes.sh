#!/usr/bin/env bash
#
# release-notes.sh - draft one release's notes from what actually landed: the
# work items closed between the previous release tag and this one, with their
# titles, their types, and the goals they served.
#
#   make release-notes VERSION=v0.3.1               write docs/releases/v0.3.1.md
#   bash scripts/release-notes.sh v0.3.1 --print    print it instead of writing
#   bash scripts/release-notes.sh v0.3.1 --force    overwrite notes that exist
#
# The raw material is the tracker rather than the commit log. A commit message
# says what one change did; the work item behind it says what somebody wanted
# and which goal it served, which is the difference between a changelog and
# notes a newcomer can read. So the commits are used only to answer which items
# were touched in the range -- their subjects carry the ids -- and everything
# the notes actually say comes from `bd show`.
#
# Only what the tracker calls closed reaches the notes. An id in a commit
# message says work touched that item and not that the item is done: a parent
# epic is named by every child's commit, and a multi-part item is named by each
# part as it lands. Publishing either as shipped is the specific lie this
# filter exists to prevent, and what it puts aside is counted in the output
# rather than dropped quietly.
#
# The sections are the operator's shape and are not negotiable here: key
# functionality first, enhancements under it, bug fixes last. Where an item goes
# is placed mechanically from its type, with the operator's one exception --
# a critical bug fix may go up with key functionality, and priority 0 is what
# this tracker spells "critical". That placement is a starting point rather than
# a judgement: which work is key and which is an enhancement belongs to the
# product manager, who edits this file before it is committed. Generating it is
# what makes that judgement cheap enough to make daily.
#
# Requires git, bd, and python3. Nothing outside the repository is written, and
# nothing is pushed. One `bd show` runs per item that landed, so a release with
# a hundred items behind it spends a hundred tracker reads; that is affordable
# inside a cut that already walks the adoption path and cross-compiles.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
notes_home="$repository/docs/releases"
# What a release tag is, spelled the same way scripts/cut-release.sh spells it.
# The file is named for the tag, so a tag in another shape would produce notes
# nothing looks for.
tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
# What a work item id can look like in a commit message. It is deliberately
# loose, because the tracker's prefix is the project's to choose: ordinary words
# match it too, and the tracker is what tells an id from a word -- a candidate
# `bd show` does not answer for is not one.
id_pattern='[A-Za-z][A-Za-z0-9_]*-[A-Za-z0-9]+(\.[0-9]+)*'

refuse() { printf '\nrelease-notes: %s\n' "$*" >&2; exit 1; }

material=""
cleanup() { [ -z "$material" ] || rm -f "$material" "$material.md"; }
trap cleanup EXIT

tag=""
print_only=0
force=0
for argument in "$@"; do
  case "$argument" in
    --print) print_only=1 ;;
    --force) force=1 ;;
    -*)      refuse "unknown option \"$argument\". Usage: scripts/release-notes.sh <tag> [--print] [--force]" ;;
    *)
      [ -z "$tag" ] || refuse "one tag at a time: \"$tag\" was already given, and then \"$argument\""
      tag="$argument"
      ;;
  esac
done

# Read from the environment when no argument was given, the way cut-release.sh
# does, so `make release-notes VERSION=<tag>` and the bare script agree.
[ -n "$tag" ] || tag="${VERSION:-}"
[ -n "$tag" ] || refuse "no version given. Pass the tag: make release-notes VERSION=v0.3.1"
if ! [[ $tag =~ $tag_pattern ]]; then
  refuse "\"$tag\" is not a release tag; a release tag is vMAJOR.MINOR.PATCH, and the notes are named for it"
fi

git -C "$repository" rev-parse --git-dir >/dev/null 2>&1 ||
  refuse "$repository is not a git repository"
# Diagnosed here rather than as an empty range: every tracker read failing and
# nothing having landed produce the same silence, and they need opposite fixes.
command -v bd >/dev/null 2>&1 ||
  refuse "bd is not installed, and the notes are written from what the tracker holds rather than from commit messages. See https://github.com/gastownhall/beads"
command -v python3 >/dev/null 2>&1 ||
  refuse "python3 is not installed, and the draft is rendered with it"

notes_file="$notes_home/$tag.md"
# The tag is checked against $tag_pattern above, so it carries no separator and
# cannot name anything but a file directly in the notes home. What a pattern
# cannot rule out is the path resolving somewhere else, because a symlink is
# legal repository content: refuse rather than write through one.
for link in "$notes_home" "$notes_file"; do
  [ ! -L "$link" ] ||
    refuse "$link is a symbolic link, so writing there would put $tag's notes outside the repository"
done
if [ "$print_only" = "0" ] && [ "$force" = "0" ] && [ -f "$notes_file" ]; then
  refuse "$notes_file already exists. Notes are written once and then edited; pass --force to draft over them, or --print to see what a fresh draft would say"
fi

# A tag that already exists bounds the range at itself, which is what makes
# notes for a release that was cut before this existed possible at all. A tag
# that does not yet exist bounds it at HEAD, which is the commit the cut is
# about to tag.
if git -C "$repository" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  end="$tag"
  released="$(git -C "$repository" log -1 --format=%cs "$tag")"
else
  end="HEAD"
  released="$(date +%Y-%m-%d)"
fi

# The previous release is the newest release tag the range's end can reach,
# which is not the same as the newest tag in the repository: a tag cut on
# something this commit does not carry would name a range git cannot walk.
previous="$(git -C "$repository" tag --list 'v*' --sort=-v:refname --merged "$end" |
  grep -vxF "$tag" | head -1 || true)"
if [ -n "$previous" ]; then
  range="$previous..$end"
  covering="$previous to $tag"
else
  range="$end"
  covering="everything up to $tag"
fi

candidates="$(git -C "$repository" log --format='%s%n%b' "$range" |
  grep -oE "$id_pattern" | sort -u || true)"

material="$(mktemp "${TMPDIR:-/tmp}/release-notes.XXXXXX")"
landed=0
strangers=0
while IFS= read -r candidate; do
  [ -n "$candidate" ] || continue
  if item="$(bd show "$candidate" --json 2>/dev/null)" && [ -n "$item" ]; then
    # bd answers with a JSON array. Flattening it onto one line is safe --
    # a newline inside a JSON string is escaped, so the only ones here are
    # formatting -- and gives the renderer one record per line.
    printf '%s\n' "$item" | tr -d '\n' >> "$material"
    printf '\n' >> "$material"
    landed=$((landed + 1))
  else
    strangers=$((strangers + 1))
  fi
done <<EOF
$candidates
EOF

if [ "$landed" = "0" ]; then
  refuse "no work item the tracker knows was found in $range, so there is nothing to write $tag's notes from. Write $notes_file by hand if this release really is only commits"
fi

# The renderer both filters and renders, and reports what it kept and what it
# put aside, because only it has read the status. Two numbers on one line: the
# closed items it wrote, and the items it dropped for not being closed.
tally="$(RELEASE_TAG="$tag" RELEASE_DATE="$released" RELEASE_COVERS="$covering" \
  python3 - "$material" "$material.md" <<'PY'
import json
import os
import sys

KEY, ENHANCEMENT, FIX = 0, 1, 2
SECTIONS = ["Key functionality", "Enhancements", "Bug fixes"]
ATTRIBUTION = "Goal served:"


def placement(item):
    """Where one item starts out, from its type alone.

    The operator's shape has one exception in it: a critical bug fix may go up
    with key functionality. Priority 0 is what this tracker spells "critical",
    so that is the exception applied mechanically. Everything here is the
    product manager's to move afterwards.
    """
    kind = (item.get("issue_type") or "").strip().lower()
    if kind == "bug":
        return KEY if item.get("priority") == 0 else FIX
    if kind == "feature":
        return KEY
    return ENHANCEMENT


def goal(item):
    """The goal an item names, read the way the harness records it.

    The notes are the authoritative home and the last attribution in them wins,
    because an item acquires one by having it appended. The tracker's witness is
    the fallback, for an item whose notes a careless writer replaced.
    """
    for line in reversed((item.get("notes") or "").splitlines()):
        line = line.strip()
        if line.startswith(ATTRIBUTION):
            statement = line[len(ATTRIBUTION):].strip()
            if statement:
                return statement
    witness = (item.get("metadata") or {}).get("yoyodyne_goal_recorded")
    if isinstance(witness, str) and witness.strip() not in ("", "0", "1"):
        return witness.strip()
    return ""


placed = [[] for _ in SECTIONS]
seen = set()
unfinished = 0
with open(sys.argv[1], encoding="utf-8") as material:
    for line in material:
        line = line.strip()
        if not line:
            continue
        answer = json.loads(line)
        # bd answers with an array. A lone object is accepted too, so a tracker
        # that spells one item differently is a shape to read rather than a
        # release with items silently missing from its notes.
        for item in answer if isinstance(answer, list) else [answer]:
            if not isinstance(item, dict):
                continue
            identifier = item.get("id") or ""
            if not identifier or identifier in seen:
                continue
            seen.add(identifier)
            # An id in a commit message says work touched this item, not that
            # the item is done: a parent epic is named by every child's commit,
            # and a multi-part item is named by each part as it lands. Only what
            # the tracker calls closed goes into a release's notes, because that
            # is what the notes claim about it.
            if (item.get("status") or "").strip().lower() != "closed":
                unfinished += 1
                continue
            placed[placement(item)].append(item)

def ordering(item):
    """Within a section, what the backlog owner ranked first comes first.

    A priority the tracker did not give sorts last rather than raising a
    TypeError: one item with an odd record must not cost the whole release its
    notes.
    """
    priority = item.get("priority")
    return (priority if isinstance(priority, int) else 9, item.get("id") or "")


out = ["# %s" % os.environ["RELEASE_TAG"], ""]
out.append("Released %s, covering %s." % (os.environ["RELEASE_DATE"], os.environ["RELEASE_COVERS"]))
for heading, items in zip(SECTIONS, placed):
    if not items:
        continue
    out.extend(["", "## %s" % heading, ""])
    for item in sorted(items, key=ordering):
        out.append("- **%s** (`%s`)" % ((item.get("title") or "").strip(), item["id"]))
        served = goal(item)
        if served:
            out.append("  Serves: %s" % served)
out.append("")
with open(sys.argv[2], "w", encoding="utf-8") as rendered:
    rendered.write("\n".join(out))
sys.stdout.write("%d %d\n" % (sum(len(items) for items in placed), unfinished))
PY
)"

closed="${tally%% *}"
unfinished="${tally##* }"

if [ "$closed" = "0" ]; then
  refuse "$landed work item(s) landed in $range and none of them is closed, so there is nothing $tag's notes can claim shipped. Close them, or write $notes_file by hand"
fi

if [ "$print_only" = "1" ]; then
  cat "$material.md"
  exit 0
fi

mkdir -p "$notes_home"
cp "$material.md" "$notes_file"

printf 'wrote %s from %d closed work item(s) across %s\n' "$notes_file" "$closed" "$range"
if [ "$unfinished" != "0" ]; then
  # An item a commit named and the tracker has not closed is the one exclusion
  # somebody has to be able to argue with: it is either work that genuinely has
  # not landed, or an item somebody forgot to close before the cut.
  printf '%d work item(s) the commits named are not closed and are not in the notes; close one and draft again if it shipped\n' "$unfinished"
fi
if [ "$strangers" != "0" ]; then
  # Named rather than passed over: a tracker that could not answer for an item
  # that really did land looks exactly like a commit message using a hyphen.
  printf '%d token(s) in those commit messages looked like work item ids and are not in the tracker; they are not in the notes\n' "$strangers"
fi
printf 'The sections are placed from each item type. Which work is key, which is an\n'
printf 'enhancement, and which critical fix belongs at the top is the product\n'
printf "manager's call: edit the file, then commit it.\n"
