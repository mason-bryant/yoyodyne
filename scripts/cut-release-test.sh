#!/usr/bin/env bash
#
# cut-release-test.sh - exercise scripts/cut-release.sh against fabricated
# repositories, so every refusal it can make is executed rather than asserted.
#
#   scripts/cut-release-test.sh
#
# The verb's whole value is that it refuses, so the refusals are what needs
# testing, and testing them against this repository would mean a real
# walkthrough and a real cross-compile for each one. What cut-release.sh needs
# is a git repository with a walkthrough and a Makefile beside it, so each case
# builds one: a scratch repository holding a copy of the script, a stub
# walkthrough that is green or red on request, and a stub Makefile whose
# `check` and `dist-verify` targets do the same. Everything the script does to
# a repository -- reading it, tagging it -- then happens to the scratch one.
#
# Everything lives under one temporary root that is removed on exit. No tag is
# written anywhere but there, and nothing is pushed.
#
# Requires bash, git, and make.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/cut-release-test.XXXXXX")"
failures=0

trap 'rm -rf "$scratch"' EXIT

# The scratch repositories are committed and tagged into, and a machine with no
# git identity configured would fail at that rather than at the claim.
export GIT_AUTHOR_NAME="cut-release-test" GIT_AUTHOR_EMAIL="test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
# The script reads VERSION from the environment when no argument is given, so
# a caller's own VERSION would silently supply the tag these cases withhold.
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

# fabricate builds one scratch repository: $1 names it, $2 is "green" or "red"
# for the walkthrough, $3 is "green", "check-red", or "build-red" for make.
fabricate() {
  local name="$1" walk="$2" mk="$3"
  local project="$scratch/$name"

  mkdir -p "$project/scripts"
  cp "$repository/scripts/cut-release.sh" "$project/scripts/cut-release.sh"

  if [ "$walk" = "green" ]; then
    cat > "$project/scripts/walk-adoption.sh" <<'SH'
#!/usr/bin/env bash
echo "  ok: the documented adoption path works as written"
SH
  else
    cat > "$project/scripts/walk-adoption.sh" <<'SH'
#!/usr/bin/env bash
echo "  FAIL: README says Go 1.24 or newer, go.mod declares 1.9"
exit 1
SH
  fi
  chmod +x "$project/scripts/walk-adoption.sh" "$project/scripts/cut-release.sh"

  # Tabs matter here, so the recipe lines are written with printf rather than a
  # heredoc an editor could helpfully reformat.
  {
    printf 'check:\n'
    if [ "$mk" = "check-red" ]; then
      printf '\t@echo "stub check is red"; exit 1\n'
    else
      printf '\t@echo "stub check passed"\n'
    fi
    printf 'dist-verify:\n'
    if [ "$mk" = "build-red" ]; then
      printf '\t@echo "stub dist-verify is red"; exit 1\n'
    else
      printf '\t@mkdir -p dist\n'
      printf '\t@echo "abc123  yoyo_$(VERSION)_stub.tar.gz" > dist/checksums.txt\n'
      printf '\t@echo "stub built $(VERSION)"\n'
    fi
  } > "$project/Makefile"

  printf '/dist/\n' > "$project/.gitignore"

  git init -q "$project"
  # git init's default branch name varies by version; name it the way the
  # script expects to find a release being cut from.
  git -C "$project" symbolic-ref HEAD refs/heads/main
  git -C "$project" add -A
  git -C "$project" commit -qm "the commit a release would name"

  printf '%s' "$project"
}

# cut runs the fabricated copy of the verb and returns its output whatever it
# exits with; each case asserts on what it said and on what it left behind.
cut() {
  local project="$1"; shift
  "$project/scripts/cut-release.sh" "$@" 2>&1 || true
}

tags() { git -C "$1" tag --list; }

step "a tag is required, and the checkout's describe default is not one"
project="$(fabricate no-tag green green)"
output="$(cut "$project")"
contains "$output" "no version given" "refuses with no tag at all"
contains "$output" "make release VERSION=" "the refusal names how to pass one"
missing "$output" "adoption walkthrough" "refuses before spending the walkthrough"

output="$(cut "$project" "v0.2.0-143-gf5e427a")"
contains "$output" "is not a release tag" "refuses \`git describe\` output, which VERSION defaults to"
output="$(cut "$project" "0.3.0")"
contains "$output" "is not a release tag" "refuses a tag with no leading v"
output="$(cut "$project" "v0.3")"
contains "$output" "is not a release tag" "refuses a tag that is not MAJOR.MINOR.PATCH"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written by any refusal"
else
  fail "a refused cut left tags behind: $(tags "$project")"
fi

step "a release is cut once"
project="$(fabricate existing-tag green green)"
git -C "$project" tag -a v0.3.0 -m v0.3.0
output="$(cut "$project" "v0.3.0")"
contains "$output" "v0.3.0 already exists" "refuses a tag that already exists"
contains "$output" "latest tag: v0.3.0" "the refusal names where the tags are up to"

step "the archives have to be the commit the tag names"
project="$(fabricate dirty-tree green green)"
printf 'uncommitted\n' > "$project/stray.txt"
output="$(cut "$project" "v0.3.0")"
contains "$output" "uncommitted changes" "refuses a dirty working tree"
contains "$output" "stray.txt" "the refusal names the file"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a release comes off the branch integration lands on"
project="$(fabricate wrong-branch green green)"
git -C "$project" checkout -q -b some-feature
output="$(cut "$project" "v0.3.0")"
contains "$output" "a release is cut from main" "refuses a cut from a feature branch"
contains "$output" "some-feature" "the refusal names the branch it is on"

step "a red walkthrough refuses the cut and names the failure"
project="$(fabricate red-walk red green)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "adoption walkthrough is red" "refuses the cut"
contains "$output" "go.mod declares 1.9" "the walkthrough's failing claim is named"
contains "$output" "nothing was written" "the refusal says nothing was left behind"
missing "$output" "stub check passed" "refuses before running the checks"
missing "$output" "stub built" "refuses before building anything"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a red check refuses the cut"
project="$(fabricate red-check green check-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "make check is red" "refuses the cut"
missing "$output" "stub built" "refuses before building anything"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a failed build leaves no tag to undo"
project="$(fabricate red-build green build-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "release build for v0.3.0 failed" "refuses the cut"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written, because the tag is written last"
else
  fail "the failed build left tags behind: $(tags "$project")"
fi

step "green all the way through: one invocation, a tagged build with checksums"
project="$(fabricate green green green)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "documented adoption path works" "the walkthrough ran"
contains "$output" "stub check passed" "the checks ran"
contains "$output" "stub built v0.3.0" "the archives were built for the tag"
contains "$output" "yoyo_v0.3.0_stub.tar.gz" "the checksums are reported"
contains "$output" "git push origin v0.3.0" "publishing is named as the operator's own next command"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi
if [ "$(git -C "$project" rev-parse v0.3.0^{commit})" = "$(git -C "$project" rev-parse HEAD)" ]; then
  pass "the tag names the commit the archives were built from"
else
  fail "the tag does not name HEAD"
fi
if [ -n "$(git -C "$project" cat-file -t v0.3.0 | grep -x tag || true)" ]; then
  pass "the tag is annotated"
else
  fail "the tag is not annotated"
fi
# Nothing left the repository: a cut that had pushed would have needed a
# remote, and this one has none.
if [ -z "$(git -C "$project" remote)" ]; then
  pass "the cut needed no remote, because it does not publish"
else
  fail "the scratch repository gained a remote"
fi

step "make release passes the tag through, and withholds the describe default"
wiring="$(make -C "$repository" -n release VERSION=v9.9.9 2>&1 || true)"
contains "$wiring" "scripts/cut-release.sh v9.9.9" "make release VERSION=<tag> reaches the verb with the tag"
wiring="$(make -C "$repository" -n release 2>&1 || true)"
missing "$wiring" "cut-release.sh v" "make release with no VERSION passes no tag, so the verb asks for one"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'cut-release.sh refuses what it should and cuts what it should\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
exit "$failures"
