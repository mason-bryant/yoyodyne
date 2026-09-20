#!/usr/bin/env bash
#
# release-notes.sh - draft one release's notes from what actually landed: the
# work items closed between the previous release tag and this one, each with
# its title, its id, its description, who asked for it where that was a persona
# rather than a person, and the goal it served.
#
#   make release-notes VERSION=v0.3.1               write docs/releases/v0.3.1.md
#   bash scripts/release-notes.sh v0.3.1 --print    print it instead of writing
#   bash scripts/release-notes.sh v0.3.1 --force    overwrite notes that exist
#   bash scripts/release-notes.sh --help            print this
#
# The raw material is the tracker rather than the commit log. A commit message
# says what one change did; the work item behind it says what somebody wanted
# and which goal it served, which is the difference between a changelog and
# notes a newcomer can read. So the commits are used only to answer which items
# were touched in the range -- their subjects carry the ids -- and everything
# the notes actually say comes from the tracker's export, `.beads/issues.jsonl`,
# one JSON record per item, which the tracker keeps beside its store and which
# the harness treats as the current state of the work. Reading the export
# rather than asking `bd` for each item means the draft needs no tracker on the
# machine and can be made from any checkout that carries the export, a run's
# worktree included.
#
# Only what the tracker calls closed reaches the notes. An id in a commit
# message says work touched that item and not that the item is done: a parent
# epic is named by every child's commit, and a multi-part item is named by each
# part as it lands. Publishing either as shipped is the specific lie this
# filter exists to prevent, and what it puts aside is counted in the output
# rather than dropped quietly.
#
# Each entry has one fixed shape, and the shape is the one the operator asked
# for on 2026-09-19:
#
#   - **<title>** (`<id>`)
#     Serves: <the goal the item names>
#     Requested by the <persona>: <why>
#
#     <the item's description, paragraph by paragraph>
#
# The `Requested by` line is there only for an item that did not originate with
# a person. Which items those are is read off the admission the item's own
# notes record: an item admitted from a role's collected report names the
# report and the role that filed it, and an item the development manager carved
# out of a parent names the parent and the decomposition. An item the product
# manager admitted with neither is the ordinary case -- work the operator asked
# for through her -- and carries no such line, because the notes do not say
# which person asked and guessing would be worse than silence.
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
# Requires git and python3, and a checkout carrying the tracker's export.
# Nothing outside the repository is written, and nothing is pushed.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
notes_home="$repository/docs/releases"
# The tracker's own export of the work items: one JSON object per line, in the
# shape `bd show <id> --json` answers with for one item. It is a derived file
# the tracker rewrites, never edited by hand, and it is what a run reads to see
# the work around its own.
export_file="$repository/.beads/issues.jsonl"
# What a release tag is, spelled the same way scripts/cut-release.sh spells it.
# The file is named for the tag, so a tag in another shape would produce notes
# nothing looks for.
tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
# What a work item id can look like in a commit message. It is deliberately
# loose, because the tracker's prefix is the project's to choose: ordinary words
# match it too, and the export is what tells an id from a word -- a candidate
# with no record in it is not one.
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
    # The help is this file's own header, so the two cannot drift apart.
    --help|-h) sed -n '2,/^$/{s/^# \{0,1\}//;p;}' "${BASH_SOURCE[0]}"; exit 0 ;;
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
# Diagnosed here rather than as an empty range: an export nobody can read and
# nothing having landed produce the same silence, and they need opposite fixes.
[ -f "$export_file" ] ||
  refuse "$export_file is not here, and the notes are written from what the tracker exports rather than from commit messages. The tracker writes it beside its store in a checkout it is initialized in. See https://github.com/gastownhall/beads"
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

material="$(mktemp "${TMPDIR:-/tmp}/release-notes.XXXXXX")"
git -C "$repository" log --format='%s%n%b' "$range" |
  grep -oE "$id_pattern" | sort -u > "$material" || true

# The renderer joins the candidates against the export, filters, and renders,
# and reports what it kept and what it put aside, because only it has read the
# records. Three numbers on one line: the closed items it wrote, the items it
# dropped for not being closed, and the candidates the export has no record of.
tally="$(RELEASE_TAG="$tag" RELEASE_DATE="$released" RELEASE_COVERS="$covering" \
  python3 - "$material" "$export_file" "$material.md" <<'PY'
import json
import os
import re
import sys

KEY, ENHANCEMENT, FIX = 0, 1, 2
SECTIONS = ["Key functionality", "Enhancements", "Bug fixes"]
ATTRIBUTION = "Goal served:"
REASON = "Reason:"
# The two admissions that did not originate with a person, in the shape the
# harness writes them onto an item's notes at creation (internal/chat): a
# decomposition names its parent and the role that made it, and an admission
# from a collected report names the report, its severity, the role that filed
# it, and the reporter's own words. These are copies of that wording, so
# internal/chat's TestTheReleaseNotesReadTheAdmissionThisPackageWrites runs
# this script over notes the real writers produced: a change to the wording
# there fails that test rather than silently reading every item as the
# operator's.
DECOMPOSED = re.compile(r"^Created under (\S+), decomposing it by the ([a-z][a-z ]*?) in conversation ")
REPORTED = re.compile(r'^Admitted from report \S+, filed at "[^"]*" by the ([a-z][a-z ]*?): ?(.*)$')


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


def admission_reason(lines):
    """The reason the admission recorded, which follows the creation note."""
    for line in lines:
        if line.startswith(REASON):
            return line[len(REASON):].strip()
    return ""


def requested_by(item):
    """Which persona asked for this item and why, or nothing for a person.

    Read off the admission the notes record rather than guessed from the
    description. An admission from a report is the role that filed the report,
    in the reporter's own words; a decomposition is the role that carved the
    item out, with the reason it recorded. An admission naming neither is the
    ordinary one -- work a person asked for through the product manager -- and
    gets no line, because the notes do not say which person.
    """
    lines = [line.strip() for line in (item.get("notes") or "").splitlines()]
    for line in lines:
        reported = REPORTED.match(line)
        if reported:
            role, words = reported.group(1), reported.group(2).strip()
            return "Requested by the %s: %s" % (role, words or admission_reason(lines) or "no reason was recorded")
    for line in lines:
        decomposed = DECOMPOSED.match(line)
        if decomposed:
            parent, role = decomposed.group(1), decomposed.group(2)
            reason = admission_reason(lines)
            if reason:
                return "Requested by the %s, decomposing `%s`: %s" % (role, parent, reason)
            return "Requested by the %s, decomposing `%s`; no reason was recorded" % (role, parent)
    return ""


def paragraphs(text):
    """The description as the paragraphs its author wrote, each on one line.

    A newline inside a paragraph is folded to a space so the entry's shape is
    the same however the description was typed; a blank line is kept as the
    break it was. A description with nothing in it says so rather than leaving
    the reader to wonder whether the writer dropped it.
    """
    found = []
    for block in re.split(r"\n\s*\n", (text or "").strip()):
        block = " ".join(block.split())
        if block:
            found.append(block)
    return found or ["(this item has no description)"]


records = {}
with open(sys.argv[2], encoding="utf-8") as export:
    for number, line in enumerate(export, 1):
        line = line.strip()
        if not line:
            continue
        try:
            item = json.loads(line)
        except ValueError as failure:
            # A line the export writer left torn is refused rather than skipped:
            # the item on it may be one that landed, and notes short by an item
            # nobody was told about are worse than a draft that stopped.
            sys.exit("%s line %d cannot be read as a work item: %s" % (sys.argv[2], number, failure))
        if isinstance(item, dict) and item.get("id"):
            # The last record for an id wins, which is how an export that was
            # appended to rather than rewritten would read.
            records[item["id"]] = item

placed = [[] for _ in SECTIONS]
unfinished = 0
strangers = 0
with open(sys.argv[1], encoding="utf-8") as candidates:
    for candidate in candidates:
        candidate = candidate.strip()
        if not candidate:
            continue
        item = records.get(candidate)
        if item is None:
            strangers += 1
            continue
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
    out.extend(["", "## %s" % heading])
    for item in sorted(items, key=ordering):
        out.append("")
        out.append("- **%s** (`%s`)" % ((item.get("title") or "").strip(), item["id"]))
        served = goal(item)
        if served:
            out.append("  Serves: %s" % served)
        requested = requested_by(item)
        if requested:
            out.append("  %s" % requested)
        for paragraph in paragraphs(item.get("description")):
            out.append("")
            out.append("  %s" % paragraph)
out.append("")
with open(sys.argv[3], "w", encoding="utf-8") as rendered:
    rendered.write("\n".join(out))
sys.stdout.write("%d %d %d\n" % (sum(len(items) for items in placed), unfinished, strangers))
PY
)"

read -r closed unfinished strangers <<EOF
$tally
EOF

if [ "$closed" = "0" ] && [ "$unfinished" = "0" ]; then
  refuse "no work item the tracker knows was found in $range, so there is nothing to write $tag's notes from. Write $notes_file by hand if this release really is only commits"
fi
if [ "$closed" = "0" ]; then
  refuse "$unfinished work item(s) landed in $range and none of them is closed, so there is nothing $tag's notes can claim shipped. Close them, or write $notes_file by hand"
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
  # Named rather than passed over: an export missing an item that really did
  # land looks exactly like a commit message using a hyphen.
  printf '%d token(s) in those commit messages looked like work item ids and are not in the tracker export; they are not in the notes\n' "$strangers"
fi
printf 'The sections are placed from each item type. Which work is key, which is an\n'
printf 'enhancement, and which critical fix belongs at the top is the product\n'
printf "manager's call: edit the file, then commit it.\n"
