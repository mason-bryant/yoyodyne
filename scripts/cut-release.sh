#!/usr/bin/env bash
#
# cut-release.sh - cut one release: gate on the adoption walkthrough and the
# checks, build the archives and their checksums for the tag, then tag the
# commit they were built from.
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
# as it was. There is no half-cut release to clean up. The one thing written
# before the tag is the housekeeping commit for the tracker's derived exports,
# and it is written after the last gate has passed, for the reason given there.
# It is held to the same claim: it is made or it is not, and a failure partway
# through puts the index back rather than leaving the exports staged.
#
# Requires git 2.9 or newer. Two things here have a floor: the `:(exclude)`
# pathspec the cleanliness check uses needs 1.9, and `core.hooksPath`, which is
# how the housekeeping commit keeps the tracker's export hook from rewriting the
# very files that commit exists to clean, is only honoured from 2.9. Older git
# does not refuse `core.hooksPath` -- it ignores it, the hook runs, and the tag
# names a tree that was dirty again a millisecond after it was cleaned, with
# nothing saying so. The `git push --atomic` this prints on a day it housekept
# needs 2.4 of whoever runs it, which the same floor covers. Also make, go, and
# whatever scripts/walk-adoption.sh needs (bd and python3). Nothing outside the
# repository is written, and nothing is pushed.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
walkthrough="$repository/scripts/walk-adoption.sh"
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
# The tracker's derived exports. Beads keeps the issues in a local database and
# writes these out as a passive dump, so they change whenever anything touches
# the tracker -- including the adoption walkthrough this gate runs itself, which
# is why they cannot simply be committed beforehand. Nothing a release ships is
# built from them. Each one is also a change a run declares the primary checkout
# may acquire while it works, in internal/cli/run.go.
derived_exports=(".beads/interactions.jsonl" ".beads/issues.jsonl")

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
#
# The tracker's derived exports are not that: nothing is built from them, and
# under a daily cadence they are dirty nearly every day, so refusing on them
# stalls the cut on a day nobody is standing there to stash. They are committed
# rather than excepted -- see the housekeeping step -- so what this asks is that
# the tree is clean of everything else, and it still names what it found.
exclude_exports=()
for export_path in "${derived_exports[@]}"; do
  exclude_exports+=(":(exclude)$export_path")
done
dirty="$(git -C "$repository" status --porcelain -- . "${exclude_exports[@]}")"
if [ -n "$dirty" ]; then
  printf '%s\n' "$dirty" >&2
  refuse "the working tree has uncommitted changes, so the archives would not be the commit $tag names. Commit or stash them first"
fi
if [ -n "$(git -C "$repository" status --porcelain -- "${derived_exports[@]}")" ]; then
  printf "the tracker's derived exports have changed; they are committed as housekeeping below\n"
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

step "housekeeping: the tracker's derived exports"
# Here rather than at the top, for two reasons. The walkthrough and the checks
# above touch the tracker themselves, so an earlier commit would be dirty again
# behind them; and every gate has now passed, so this is the first thing written
# and every refusal above it left the repository exactly as it found it.
# Committing rather than excepting these paths is what keeps the
# tag naming a tree with nothing uncommitted in it, so "the archives are the
# commit the tag names" stays a property rather than a property with a footnote.
housekept=()
for export_path in "${derived_exports[@]}"; do
  if [ -n "$(git -C "$repository" status --porcelain -- "$export_path")" ]; then
    housekept+=("$export_path")
  fi
done
if [ "${#housekept[@]}" -gt 0 ]; then
  # Hooks are off for this one commit: the tracker installs commit hooks that
  # export, which would dirty the tree this commit exists to clean. This is the
  # git 2.9 in the prerequisites above -- older git ignores the option rather
  # than refusing it, and the hook runs.
  if ! git -C "$repository" add -- "${housekept[@]}" ||
     ! git -C "$repository" -c core.hooksPath=/dev/null \
         commit -q -m "record the tracker's derived exports for $tag"; then
    # Put the index back, so this refusal leaves the repository exactly as it
    # was like every refusal above it rather than leaving the exports staged.
    # Its own stderr stands if it cannot, which is the only way this leaves
    # anything behind and is louder than the refusal that follows.
    git -C "$repository" reset -q -- "${housekept[@]}" || true
    refuse "the tracker's derived exports could not be committed, so $tag was not cut and the index was put back"
  fi
  head="$(git -C "$repository" rev-parse HEAD)"
  printf 'committed %s\n' "${housekept[*]}"
  printf 'HEAD: %s\n' "$head"
else
  printf 'nothing to commit\n'
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
if [ "${#housekept[@]}" -gt 0 ]; then
  # The tag names the housekeeping commit, which origin does not have, so the
  # branch goes with it -- atomically, because a tag published without its
  # commit on the branch is the divergence the gate above refuses.
  printf '  git push --atomic origin %s %s\n' "$default_branch" "$tag"
else
  printf '  git push origin %s\n' "$tag"
fi
