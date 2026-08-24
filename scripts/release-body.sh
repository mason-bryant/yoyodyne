#!/usr/bin/env bash
#
# release-body.sh - compose one release page's body: the notes this repository
# carries for the tag, with the install preamble under them.
#
#   bash scripts/release-body.sh v0.3.1 /tmp/body.md
#
# This is the release workflow's own step, extracted so it is a thing that can
# be run and tested rather than eight lines of YAML whose first execution is a
# tag push against a real published release. .github/workflows/release.yml calls
# it and passes the result to `gh release create --notes-file`.
#
# The composition is deliberate in two places. The tag's notes go first and the
# preamble under them, because what changed is what a returning reader came for
# and installing is what a new one came for, and only one of those two can be at
# the top. And the notes file's own `# <tag>` heading is dropped, because the
# release page is already titled with the tag and the same words twice reads
# like a mistake.
#
# A tag with no notes file publishes the preamble alone rather than failing. A
# release with thin notes is worth more than a pushed tag with no release behind
# it, and the tags cut before the notes home existed are exactly the ones that
# would otherwise turn a rerun of this workflow into a red build.
#
# Requires bash. Nothing in the repository is written: the only thing written is
# the destination the caller named.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
preamble="$repository/.github/release-notes-preamble.md"
notes_home="$repository/docs/releases"

refuse() { printf '\nrelease-body: %s\n' "$*" >&2; exit 1; }

tag="${1:-}"
destination="${2:-}"
[ -n "$tag" ] ||
  refuse "no tag given. Usage: bash scripts/release-body.sh <tag> <destination>"
[ -n "$destination" ] ||
  refuse "no destination given. Usage: bash scripts/release-body.sh <tag> <destination>"
# The preamble is how a reader installs what they just downloaded, so a release
# published without it is worse than one this refused to compose.
[ -f "$preamble" ] || refuse "$preamble is missing, so there is no install section to publish"

notes="$notes_home/$tag.md"
: > "$destination"

if [ -f "$notes" ]; then
  # Drop a leading `# <anything>` and keep everything else. Done by reading the
  # first line rather than with sed's addressed-block syntax, which BSD and GNU
  # sed disagree about often enough that a release page is the wrong place to
  # find out which one the runner has.
  first=""
  IFS= read -r first < "$notes" || true
  case "$first" in
    ("# "*) tail -n +2 "$notes" >> "$destination" ;;
    (*)     cat "$notes" >> "$destination" ;;
  esac
  printf '\n' >> "$destination"
else
  printf 'no %s, so this release publishes the preamble alone\n' "$notes" >&2
fi

cat "$preamble" >> "$destination"
