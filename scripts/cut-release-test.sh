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
# is a git repository with a walkthrough, a notes writer, and a Makefile beside
# it, so each case builds one: a scratch repository holding a copy of the
# script, a stub walkthrough that is green or red on request, a stub notes
# writer, and a stub Makefile whose `check` and `dist-verify` targets do the
# same. Everything the script does to a repository -- reading it, drafting a
# release's notes into it, tagging it -- then happens to the scratch one.
#
# The real scripts/release-notes.sh is stubbed rather than copied, because what
# cut-release.sh is gated on is a file being there and a writer that can say it
# failed; what that writer puts in the file is
# scripts/release-notes-test.sh's claim.
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
# for the walkthrough, $3 is "green", "check-red", or "build-red" for make, and
# $4 is "present" (the default), "absent", or "draft-red" for this release's
# notes. Most cases want notes already committed, because the gate they are
# about is further down.
fabricate() {
  local name="$1" walk="$2" mk="$3" notes="${4:-present}"
  local project="$scratch/$name"

  mkdir -p "$project/scripts"
  cp "$repository/scripts/cut-release.sh" "$project/scripts/cut-release.sh"

  # A stub notes writer, so the notes gate is exercised without a tracker. The
  # real scripts/release-notes.sh reads bd and renders with python3; what
  # cut-release.sh needs from it is that it writes docs/releases/<tag>.md under
  # its own repository and says whether it could, and that is what this does.
  if [ "$notes" = "draft-red" ]; then
    cat > "$project/scripts/release-notes.sh" <<'SH'
#!/usr/bin/env bash
echo "release-notes: bd is not installed" >&2
exit 1
SH
  else
    cat > "$project/scripts/release-notes.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$repository/docs/releases"
printf '# %s\n\n## Key functionality\n\n- **The scratch release** (`stub-1`)\n' "$1" \
  > "$repository/docs/releases/$1.md"
echo "wrote docs/releases/$1.md from 1 closed work item(s)"
SH
  fi
  chmod +x "$project/scripts/release-notes.sh"

  # The notes a cut is gated on are committed with the commit the tag names, so
  # a repository that has them has them in its history rather than beside it.
  if [ "$notes" = "present" ]; then
    mkdir -p "$project/docs/releases"
    printf '# v0.3.0\n\n## Key functionality\n\n- **The scratch release** (`stub-1`)\n' \
      > "$project/docs/releases/v0.3.0.md"
  fi

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
    elif [ "$mk" = "no-checksums" ]; then
      # A build that succeeded and put its checksums somewhere else: what a
      # rename of the real dist recipe's output would look like from here.
      printf '\t@echo "stub built $(VERSION), checksums elsewhere"\n'
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

step "a tag does not name a commit origin does not have"
project="$(fabricate diverged-from-origin green green)"
# A bare repository on disk is an origin a fetch can reach with no network, so
# the comparison itself is executed rather than only its unreachable path.
origin="$scratch/diverged-origin.git"
git init -q --bare "$origin"
git -C "$project" remote add origin "$origin"
git -C "$project" push -q origin main
shared="$(git -C "$project" rev-parse HEAD)"
git -C "$project" commit -q --allow-empty -m "a commit origin does not have"
output="$(cut "$project" "v0.3.0")"
contains "$output" "HEAD is not where origin/main is" "refuses a HEAD that has diverged from origin"
contains "$output" "$shared" "the refusal names the commit origin has"
missing "$output" "adoption walkthrough" "refuses before spending the walkthrough"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "and cuts when origin agrees, without pushing anything to it"
project="$(fabricate agrees-with-origin green green)"
origin="$scratch/agreeing-origin.git"
git init -q --bare "$origin"
git -C "$project" remote add origin "$origin"
git -C "$project" push -q origin main
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main agrees" "a reachable origin that agrees is checked and said so"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi
# The strongest form of "it does not publish": there was a remote to push to,
# and the cut left nothing on it.
if [ -z "$(git -C "$origin" tag --list)" ]; then
  pass "the tag was not pushed, because publishing is the operator's own command"
else
  fail "the cut pushed a tag to origin: $(git -C "$origin" tag --list)"
fi

step "a release with no notes drafts them and refuses, and cuts once they are committed"
project="$(fabricate no-notes green green absent)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "has no notes yet" "says the notes are missing"
contains "$output" "drafted docs/releases/v0.3.0.md" "names the file it drafted"
contains "$output" "commit it, then cut v0.3.0 again" "names what to do with it"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
missing "$output" "stub built" "refuses before building anything"
if [ -f "$project/docs/releases/v0.3.0.md" ]; then
  pass "the draft is really on disk, which is the one thing the refusal left behind"
else
  fail "the refusal claimed a draft that is not there"
fi
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi
# The second half of the same story: the operator reads the draft, places each
# item, commits it, and the cut goes through. This is the daily loop, and these
# are the commands docs/developing-yoyo.md tells them to run, spelled the same
# way on purpose -- the draft is a file git has never seen, so `git commit -a`
# would stage nothing and stop with "no changes added to commit". A test that
# staged it some easier way would leave that hole in the documentation.
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "v0.3.0 release notes"
output="$(cut "$project" "v0.3.0")"
contains "$output" "docs/releases/v0.3.0.md is present" "the gate passes once the notes are committed"
contains "$output" "documented adoption path works" "and the walkthrough runs after it"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written, and it names a commit carrying its own notes"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi

step "the notes commit has to reach origin before the cut goes through"
# The documented loop's push, executed. Every other case here has an origin it
# cannot reach and asserts the SKIPPED path, so this is the only place the
# remote gate runs for real -- and committing the notes without pushing them is
# exactly the state the loop puts an operator in halfway through.
project="$(fabricate notes-not-pushed green green absent)"
origin="$scratch/notes-origin.git"
git init -q --bare "$origin"
git -C "$project" remote add origin "$origin"
git -C "$project" push -q origin main
output="$(cut "$project" "v0.3.0")"
contains "$output" "drafted docs/releases/v0.3.0.md" "the first cut drafts the notes"
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "v0.3.0 release notes"
output="$(cut "$project" "v0.3.0")"
contains "$output" "HEAD is not where origin/main is" "committing the notes is not enough on its own"
missing "$output" "is present and committed" "it refuses before the notes gate, so nothing further is spent"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi
git -C "$project" push -q origin main
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main agrees" "pushing them satisfies the remote gate"
contains "$output" "docs/releases/v0.3.0.md is present" "and the notes gate passes behind it"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written, and origin has the commit carrying its notes"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi

step "a cut whose notes cannot be drafted refuses rather than cutting without them"
project="$(fabricate undraftable-notes green green draft-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "notes could not be drafted" "refuses the cut"
contains "$output" "bd is not installed" "the drafting failure is shown rather than swallowed"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

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
contains "$output" "SKIPPED: origin is unreachable" "an origin it cannot reach is named as unchecked rather than passed over"
contains "$output" "docs/releases/v0.3.0.md is present" "the notes gate ran"
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

step "a finished cut is never reported as a failure"
# Once the tag exists the cut has happened, so nothing left to print may fail
# it: an operator told the release failed, holding a tag that is real and
# never shown the push it needs, is worse off than one told nothing.
project="$(fabricate silent-checksums green no-checksums)"
if output="$("$project/scripts/cut-release.sh" "v0.3.0" 2>&1)"; then status=0; else status=$?; fi
if [ "$status" = "0" ]; then
  pass "a cut whose checksums are not where it looked still exits 0"
else
  fail "the cut exited $status after tagging -- got: $output"
fi
contains "$output" "is not where it was expected" "it says the checksums were not found"
contains "$output" "git push origin v0.3.0" "the push the tag needs is still printed"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag it reported is really there"
else
  fail "expected v0.3.0, got: $(tags "$project")"
fi

step "make release passes the tag through, and withholds the describe default"
wiring="$(make -C "$repository" -n release VERSION=v9.9.9 2>&1 || true)"
contains "$wiring" "scripts/cut-release.sh v9.9.9" "make release VERSION=<tag> reaches the verb with the tag"
wiring="$(make -C "$repository" -n release 2>&1 || true)"
missing "$wiring" "cut-release.sh v" "make release with no VERSION passes no tag, so the verb asks for one"
wiring="$(make -C "$repository" -n release-notes VERSION=v9.9.9 2>&1 || true)"
contains "$wiring" "scripts/release-notes.sh v9.9.9" "make release-notes VERSION=<tag> reaches the notes writer with the tag"
wiring="$(make -C "$repository" -n release-notes 2>&1 || true)"
missing "$wiring" "release-notes.sh v" "make release-notes with no VERSION passes no tag either"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'cut-release.sh refuses what it should and cuts what it should\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
exit "$failures"
