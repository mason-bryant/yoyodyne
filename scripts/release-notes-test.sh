#!/usr/bin/env bash
#
# release-notes-test.sh - exercise scripts/release-notes.sh against a fabricated
# repository and a stub tracker, so what the notes say about a range is executed
# rather than asserted.
#
#   bash scripts/release-notes-test.sh
#
# The script's whole job is a join: which work items landed between two tags,
# and what the tracker says about each. Running it against this repository would
# mean its real history and its real tracker, so the answer would change every
# day and the assertions could only be about shape. Each case builds its own
# instead -- a scratch repository holding a copy of the script, commits whose
# subjects name work items the way this project's do, and a stub `bd` on PATH
# that answers `show <id> --json` from fixtures.
#
# What is worth testing here is the placement, because it is the part with a
# rule in it: a feature opens, a task sits under it, a bug sinks, and a critical
# bug rises back to the top. Everything else the file says is the tracker's
# words carried through.
#
# scripts/release-body.sh is covered here too, against the notes these cases
# just wrote. It is what the release workflow runs on a tag push, so the only
# other place it would ever execute is a real publication.
#
# Everything lives under one temporary root that is removed on exit. The real
# tracker is never read and nothing outside that root is written.
#
# Requires bash, git, and python3.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/release-notes-test.XXXXXX")"
scratch="$(cd "$scratch" && pwd)"
project="$scratch/product"
fixtures="$scratch/fixtures"
failures=0

trap 'rm -rf "$scratch"' EXIT

# The scratch repository is committed and tagged into, and a machine with no git
# identity configured would fail at that rather than at the claim.
export GIT_AUTHOR_NAME="release-notes-test" GIT_AUTHOR_EMAIL="test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
# The script reads VERSION from the environment when no argument is given, so a
# caller's own VERSION would silently supply the tag one case withholds.
unset VERSION

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

# item writes one tracker record, in the shape `bd show <id> --json` answers
# with: a JSON array holding the one item. The goal is written where the harness
# writes it, onto the notes, because that is where the script reads it from.
# Status defaults to closed, which is what a release's notes are made of; a case
# that wants unfinished work passes its own.
item() {
  mkdir -p "$fixtures"
  python3 - "$fixtures/$1.json" "$1" "$2" "$3" "$4" "${5:-}" "${6:-closed}" <<'PY'
import json
import sys

path, identifier, title, kind, priority, served, status = sys.argv[1:8]
notes = "Admitted to the backlog by the product manager."
if served:
    notes += "\n\nGoal served: %s" % served
with open(path, "w", encoding="utf-8") as fixture:
    json.dump([{
        "id": identifier,
        "title": title,
        "issue_type": kind,
        "priority": int(priority),
        "status": status,
        "notes": notes,
        "metadata": {},
    }], fixture)
PY
}

mkdir -p "$scratch/bin"
cat > "$scratch/bin/bd" <<'SH'
#!/usr/bin/env bash
# The tracker, reduced to the one question the notes writer asks it. An id with
# no fixture is one bd does not know -- which is what an ordinary hyphenated
# word in a commit message looks like from here.
[ "$1" = "show" ] || exit 1
[ -f "$BD_FIXTURES/$2.json" ] || exit 1
cat "$BD_FIXTURES/$2.json"
SH
chmod +x "$scratch/bin/bd"
export BD_FIXTURES="$fixtures"
export PATH="$scratch/bin:$PATH"

item scratch-ifd.1 "Watch mode: the harness runs until told to stop" feature 1 \
  "Let configurable agents run unattended."
item scratch-ifd.2 "Format the goals listing for reading" task 2
item scratch-ifd.3 "A completed run is discarded after its terminal result" bug 0 \
  "Never lose finished work."
item scratch-ifd.4 "Shift-return inserts a newline in the conversation input" bug 2
item scratch-ifd.5 "The thing that shipped in the last release" feature 1
# The item a commit names without the item being done: a parent epic is named by
# every child's commit, and a multi-part item by each part as it lands.
item scratch-ifd.6 "The epic every child's commit names" task 1 "" in_progress

mkdir -p "$project/scripts"
cp "$repository/scripts/release-notes.sh" "$project/scripts/release-notes.sh"
chmod +x "$project/scripts/release-notes.sh"
git init -q "$project"
# git init's default branch name varies by version; name it once so nothing here
# depends on which git ran.
git -C "$project" symbolic-ref HEAD refs/heads/main
git -C "$project" add -A
git -C "$project" commit -qm "the first commit"

# land writes one commit whose subject names a work item, the way this project's
# integration commits do.
land() { git -C "$project" commit -q --allow-empty -m "product: $1 $2"; }

land scratch-ifd.5 "The thing that shipped in the last release"
git -C "$project" tag -a v0.1.0 -m v0.1.0

land scratch-ifd.1 "Watch mode: the harness runs until told to stop"
# A hyphenated ordinary word in a subject looks exactly like an id from a
# regular expression's side, and the tracker is what tells them apart.
land scratch-ifd.2 "Format the goals listing, including the well-known ordering"
land scratch-ifd.3 "A completed run is discarded after its terminal result"
land scratch-ifd.4 "Shift-return inserts a newline in the conversation input"
land scratch-ifd.6 "The epic every child's commit names"

draft() { ( cd "$project" && ./scripts/release-notes.sh "$@" 2>&1 ) || true; }

# section_of reports the heading one line sits under, which is the whole of what
# "the sections follow the operator's shape" means for a single item.
section_of() {
  awk -v needle="$2" '
    /^## / { heading = substr($0, 4); next }
    index($0, needle) { print heading; exit }
  ' "$1"
}

notes="$project/docs/releases/v0.2.0.md"

step "the notes are written from the items that landed in the range"
output="$(draft v0.2.0)"
contains "$output" "wrote $notes" "says where it wrote"
contains "$output" "from 4 closed work item(s)" "counts what landed"
contains "$output" "across v0.1.0..HEAD" "names the range it walked"
contains "$output" "looked like work item ids and are not in the tracker" \
  "a hyphenated word the tracker does not know is named rather than silently dropped"
if [ -f "$notes" ]; then
  pass "the file is named for the tag, in the notes home"
else
  fail "no $notes was written"
fi

body="$(cat "$notes" 2>/dev/null || true)"
contains "$body" "# v0.2.0" "the file is titled with the tag"
contains "$body" "covering v0.1.0 to v0.2.0" "and says what it covers"
contains "$body" '(`scratch-ifd.1`)' "each item carries its id"
contains "$body" "Serves: Let configurable agents run unattended." "and the goal it served"
missing "$body" "scratch-ifd.5" "an item that landed before the previous tag is not in it"

step "only what the tracker calls closed is published as shipped"
contains "$output" "1 work item(s) the commits named are not closed" \
  "an item a commit named and the tracker has not closed is counted"
missing "$body" "The epic every child's commit names" "and is not in the notes"
missing "$body" "scratch-ifd.6" "not even as an id"

step "each item starts in the section its type puts it in"
for probe in "Watch mode|Key functionality" \
             "Format the goals listing|Enhancements" \
             "A completed run is discarded|Key functionality" \
             "Shift-return inserts|Bug fixes"; do
  needle="${probe%%|*}"
  want="${probe##*|}"
  got="$(section_of "$notes" "$needle")"
  if [ "$got" = "$want" ]; then
    pass "\"$needle\" is under $want"
  else
    fail "\"$needle\" is under \"${got:-no heading}\", expected $want"
  fi
done
printf '  (the third is the one exception in the shape: a critical bug fix goes up\n'
printf '   with key functionality rather than to the bottom)\n'

step "the sections are in the operator's order"
order="$(grep '^## ' "$notes" | tr '\n' '|')"
if [ "$order" = "## Key functionality|## Enhancements|## Bug fixes|" ]; then
  pass "key functionality, then enhancements, then bug fixes"
else
  fail "the sections came out as: $order"
fi

step "the release page's body is the tag's own notes with the preamble under them"
# scripts/release-body.sh is what the release workflow runs on a tag push. It is
# exercised here because the alternative is finding out what it does during a
# real publication, which is the one run where being wrong is expensive.
mkdir -p "$project/.github"
printf '## Install\n\nDownload the archive for your platform below.\n' \
  > "$project/.github/release-notes-preamble.md"
cp "$repository/scripts/release-body.sh" "$project/scripts/release-body.sh"
composed="$scratch/body.md"
if output="$( ( cd "$project" && bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )"; then
  pass "composing a body for a tag that has notes succeeds"
else
  fail "release-body.sh failed for a tag with notes -- got: $output"
fi
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "Watch mode" "the tag's own notes are the body"
contains "$body" "## Key functionality" "with their sections intact"
contains "$body" "## Install" "and the install preamble under them"
missing "$body" "# v0.2.0" "the file's title is dropped, because the page already carries it"

step "a tag with no notes publishes the preamble rather than failing the workflow"
if output="$( ( cd "$project" && bash scripts/release-body.sh v9.9.9 "$composed" ) 2>&1 )"; then
  pass "composing a body for a tag with no notes file still succeeds"
else
  fail "release-body.sh failed for a tag with no notes -- got: $output"
fi
contains "$output" "publishes the preamble alone" "and says so rather than doing it quietly"
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "## Install" "the preamble is the whole body"
missing "$body" "Watch mode" "and no other release's notes leaked into it"

step "notes are written once, and then edited"
output="$(draft v0.2.0)"
contains "$output" "already exists" "refuses to overwrite notes that are there"
contains "$output" "--force" "and names the way to mean it"

step "--print writes nothing"
output="$(draft v0.3.0 --print)"
contains "$output" "# v0.3.0" "prints the draft"
if [ ! -f "$project/docs/releases/v0.3.0.md" ]; then
  pass "and left no file behind"
else
  fail "--print wrote docs/releases/v0.3.0.md"
fi

step "the tag has to be a release tag, and one has to be given"
output="$(draft 0.2.0)"
contains "$output" "is not a release tag" "refuses a tag with no leading v"
output="$(draft v0.2.0-143-gf5e427a)"
contains "$output" "is not a release tag" "refuses \`git describe\` output"
output="$(draft)"
contains "$output" "no version given" "refuses with no tag at all"

step "a range with nothing in it is refused rather than published as empty notes"
# A release nobody can say anything about is worth a person's attention, and
# empty notes on a release page are worse than notes somebody had to write.
git -C "$project" tag -a v0.2.0 -m v0.2.0
output="$(draft v0.3.0)"
contains "$output" "nothing to write" "says there is nothing to write from"
contains "$output" "v0.2.0..HEAD" "and names the range it found empty"

step "a range holding only unfinished work is refused too"
# Different from the range above and worth telling apart: work did land here,
# and none of it is something a release page may claim shipped.
land scratch-ifd.6 "The epic every child's commit names"
output="$(draft v0.3.0)"
contains "$output" "none of them is closed" "says the work is not closed rather than that nothing landed"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'release-notes.sh writes what landed, in the shape the operator asked for\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
exit "$failures"
