#!/usr/bin/env bash
#
# cut-release.sh - cut one release: gate on its notes, the adoption walkthrough
# and the checks, build the archives and their checksums for the tag, then tag
# the commit they were built from.
#
#   make release VERSION=v0.3.0      what an operator runs
#   scripts/cut-release.sh v0.3.0    the same thing, without make
#
# This is a verb rather than a checklist because the cadence is daily. A
# checklist is a list of things somebody can skip on the day they are in a
# hurry, and the day somebody is in a hurry is exactly the day the walkthrough
# would have caught something. One invocation runs the gate every time, and a
# red gate refuses the cut and names what was red.
#
# What it does not do is publish. The tag push is the irreversible half and it
# is what .github/workflows/release.yml acts on, so it stays a separate act the
# operator takes deliberately; this prints the one command. Everything before
# that point is what this makes certain.
#
# The order is deliberate: every gate runs before anything is written, and the
# tag is created last, so a refusal at any point leaves the repository exactly
# as it was. There is no half-cut release to clean up.
#
# The one thing it writes into the repository is a release's notes, and it does
# that only when they are missing: a cut with no story to tell drafts one from
# the work items that landed and refuses, so the tag lands on a commit that
# carries its own notes rather than on one the notes are added after. That is
# the single exception to "a refusal leaves the repository exactly as it was",
# and the refusal says outright what it wrote.
#
# Requires git, make, go, and whatever scripts/walk-adoption.sh needs (bd and
# python3). Nothing outside the repository is written, and nothing is pushed.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
walkthrough="$repository/scripts/walk-adoption.sh"
notes_writer="$repository/scripts/release-notes.sh"
# Where a release's notes live, versioned beside the code they describe and
# named for the tag, so the release page and the repository tell one story.
notes_home="$repository/docs/releases"
# When this was reached through `make release`, use the same make.
make_program="${MAKE:-make}"
# Where `dist` writes, spelled the way the Makefile spells it: DIST ?= dist,
# and a caller who overrode it on the make command line has it in the
# environment here too.
dist_directory="$repository/${DIST:-dist}"
# What a release tag is: vMAJOR.MINOR.PATCH, and nothing else. The release
# workflow triggers on "v*" and the archives are named for this string, so a
# tag in another shape produces a release whose files nobody can predict. The
# narrowness is deliberate -- `git describe` output is a well-formed semver
# prerelease, and a looser pattern would let VERSION's build-time default be
# cut as if it were a release.
tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'

step()   { printf '\n=== %s\n' "$*"; }
# Every refusal names the tag it did not cut, because the operator's next
# question is always whether anything was left behind. Nothing was.
refuse() { printf '\ncut-release: %s\n' "$*" >&2; exit 1; }

walk_log=""
cleanup() { [ -z "$walk_log" ] || rm -f "$walk_log"; }
trap cleanup EXIT

git -C "$repository" rev-parse --git-dir >/dev/null 2>&1 ||
  refuse "$repository is not a git repository"

latest="$(git -C "$repository" tag --list 'v*' --sort=-v:refname | head -1)"
[ -n "$latest" ] || latest="(none yet)"

tag="${1:-${VERSION:-}}"
if [ -z "$tag" ]; then
  refuse "no version given. Pass the tag: make release VERSION=v0.3.0 (latest tag: $latest)"
fi
if ! [[ $tag =~ $tag_pattern ]]; then
  refuse "\"$tag\" is not a release tag; a release tag is vMAJOR.MINOR.PATCH. Pass one: make release VERSION=v0.3.0 (latest tag: $latest)"
fi
if git -C "$repository" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  refuse "$tag already exists. A release is cut once; pick the next tag (latest tag: $latest)"
fi

step "the commit $tag would name"

# The archives are built from the working tree and the tag is placed on HEAD,
# so the two agree only while the tree is clean. A dirty tree would ship a
# binary built from something no commit holds.
dirty="$(git -C "$repository" status --porcelain)"
if [ -n "$dirty" ]; then
  printf '%s\n' "$dirty" >&2
  refuse "the working tree has uncommitted changes, so the archives would not be the commit $tag names. Commit or stash them first"
fi

# Releases come off the branch integration lands on. A tag on a feature branch
# builds and publishes perfectly well and names work that is not in the
# product, which is the expensive mistake this cadence could make daily.
default_branch="$(git -C "$repository" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
default_branch="${default_branch#origin/}"
[ -n "$default_branch" ] || default_branch="main"
branch="$(git -C "$repository" branch --show-current)"
if [ "$branch" != "$default_branch" ]; then
  refuse "a release is cut from $default_branch; this checkout is on \"${branch:-a detached HEAD}\""
fi

head="$(git -C "$repository" rev-parse HEAD)"
printf 'HEAD: %s (%s)\n' "$head" "$default_branch"

# Whether HEAD is what the rest of the world has needs the network. Where it is
# reachable this is settled; where it is not, it is named as unchecked rather
# than passed over, the same way the walkthrough treats a claim it cannot
# exercise. A silent skip here reads as "today's work" and can be yesterday's.
if git -C "$repository" fetch --quiet origin "$default_branch" 2>/dev/null; then
  remote="$(git -C "$repository" rev-parse FETCH_HEAD)"
  if [ "$head" != "$remote" ]; then
    refuse "HEAD is not where origin/$default_branch is ($remote), so $tag would name a commit the product does not have. Pull or push first"
  fi
  printf 'origin/%s agrees\n' "$default_branch"
else
  printf 'SKIPPED: origin is unreachable, so whether HEAD is current was not checked\n'
fi

step "gate: this release's notes"
# A release nobody can read is a release nobody adopts, so the notes are a gate
# rather than a courtesy -- and they are gated here, before the walkthrough and
# the cross-compile, because drafting them costs seconds and those cost minutes.
#
# The notes are the one thing a cut cannot finish on its own: which work is key
# functionality, which is an enhancement, and which critical fix belongs at the
# top is the product manager's judgement. So a missing file is drafted from the
# items that landed and the cut refuses, leaving the operator a file to read,
# place, and commit. The tag then names a commit that carries its own notes.
notes_file="$notes_home/$tag.md"
if [ ! -f "$notes_file" ]; then
  printf '%s has no notes yet, so they are drafted here from what landed since\n' "$tag"
  printf 'the last tag. Nothing else has been written and no tag exists.\n\n'
  if ! bash "$notes_writer" "$tag"; then
    refuse "$tag's notes could not be drafted, so $tag was not cut"
  fi
  refuse "drafted docs/releases/$tag.md and stopped there. Read it, move each item into the section its work belongs in, commit it, then cut $tag again -- that is the only thing this left behind"
fi
printf 'docs/releases/%s.md is present and committed, so %s will name a commit\n' "$tag" "$tag"
printf 'that carries its own notes\n'

step "gate: the adoption walkthrough"
printf 'A release is what the install path consumes, so the documented first hour\n'
printf 'is walked before the tag exists rather than after it is published.\n\n'
walk_log="$(mktemp "${TMPDIR:-/tmp}/cut-release-walk.XXXXXX")"
if ! bash "$walkthrough" 2>&1 | tee "$walk_log"; then
  named="$(grep 'FAIL:' "$walk_log" || true)"
  if [ -n "$named" ]; then
    printf '\nwhat the walkthrough found:\n%s\n' "$named" >&2
  else
    printf '\nthe walkthrough did not finish:\n%s\n' "$(tail -5 "$walk_log")" >&2
  fi
  refuse "the adoption walkthrough is red, so $tag was not cut and nothing was written"
fi

step "gate: make check"
# The release workflow runs this after the tag push. Running it here means a
# red check costs a rerun rather than a pushed tag with no release behind it.
if ! "$make_program" -C "$repository" check; then
  refuse "make check is red, so $tag was not cut and nothing was written"
fi

step "build: the archives and their checksums for $tag"
if ! "$make_program" -C "$repository" dist-verify VERSION="$tag"; then
  refuse "the release build for $tag failed, so no tag was written"
fi

step "tag"
# Last, and only now: everything above passed, so this tag never needs undoing.
git -C "$repository" tag -a "$tag" -m "$tag"
printf '%s tagged at %s\n' "$tag" "$head"

printf '\n=== cut\n'
# The cut is finished from here: the tag exists and the archives are on disk.
# Nothing below is allowed to fail it. A completed release that exits non-zero
# because a report could not be printed is the worst of both -- the tag is
# real, `make release` says it failed, and the push it needs never appears.
cat "$dist_directory/checksums.txt" 2>/dev/null ||
  printf 'the cut succeeded, but %s/checksums.txt is not where it was expected\n' "$dist_directory"
printf '\nPublishing is the tag push, which the release workflow acts on:\n'
printf '  git push origin %s\n' "$tag"
