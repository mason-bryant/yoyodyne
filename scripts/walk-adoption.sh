#!/usr/bin/env bash
#
# walk-adoption.sh - execute the README's "Getting started" steps against a
# throwaway project that is not this one and is not written in Go, so that
# section is verified rather than asserted. This file's history is the reason: a
# README that asserted something false about the product once reached the
# operator as fact, and a claim a stranger will act on is worth executing before
# it ships.
#
# The README's spine is three steps -- install, `yoyo init`, `yoyo chat` -- and
# the numbered steps below are this script's own finer sequence through the
# claims those three make.
#
#   scripts/walk-adoption.sh                 walk every step that needs no provider
#   WALK_PROVIDER=1 scripts/walk-adoption.sh also invoke the provider on step 11
#
# The provider step is opt-in because it spends real capacity: it hands an item
# to a developer agent. Everything before it is free and deterministic.
#
# Requires go, git, bd, and python3. jq is optional and only the yoyo-status
# cost report needs it.
#
# No state an operator owns is written: the scratch project, the worktrees, the
# binary `go install` produces, and the run state all live under one temporary
# root that is removed on exit, so a real state directory is never touched. Two
# things outside it are written, and both are caches rather than state. Go's
# build cache goes to $GOCACHE, which defaults into the scratch root here and is
# left alone when a caller already set one; Go's module cache is deliberately
# shared, because isolating it would mean re-downloading the module graph on
# every walk to protect a directory whose whole purpose is to be rebuilt. The
# repository's own ./bin/yoyo is also rebuilt, which is what `make build` does
# and what the step is there to check.
#
# The documented claims this cannot check are the ones that need the network:
# whether the clone URL is reachable, and whether `go install` of a published tag
# actually fetches. It checks the verifiable halves instead -- that the URL the
# README names is this checkout's origin remote, and that the module path the
# README installs is the one go.mod declares -- and names the rest as skipped.

set -euo pipefail

readme_clone_url="https://github.com/mason-bryant/yoyodyne"
readme_install_module="github.com/mason-bryant/yoyodyne"

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyodyne-walk.XXXXXX")"
project="$scratch/calc"
failures=0
skips=0

cleanup() {
  # Remove the scratch worktrees through git where the scratch repository still
  # exists, so nothing is left registered, then take the whole root.
  if [ -d "$project/.git" ]; then
    git -C "$project" worktree prune >/dev/null 2>&1 || true
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT

export YOYODYNE_STATE_HOME="$scratch/state"
export GOCACHE="${GOCACHE:-$scratch/gocache}"
mkdir -p "$YOYODYNE_STATE_HOME"

step()   { printf '\n=== %s\n' "$*"; }
run()    { printf '$ %s\n' "$*"; "$@"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }
# A claim this environment cannot exercise is named rather than passed over. A
# silent skip reads as coverage, which is the failure mode this whole script
# exists to avoid.
skip()   { printf '  SKIPPED: %s\n' "$*"; skips=$((skips + 1)); }

# contains asserts that a haystack holds a substring, which is how a claim about
# what a command *says* is checked: the README quotes these messages.
contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

for tool in go git bd python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "walk-adoption.sh needs $tool" >&2; exit 2; }
done

step "prerequisites: the build requirement the README states"
go_directive="$(sed -n 's/^go \([0-9.]*\)$/\1/p' "$repository/go.mod")"
printf 'go.mod declares go %s\n' "$go_directive"
case "$go_directive" in (1.24*) pass "README's \"Go 1.24 or newer\" matches go.mod" ;;
  (*) fail "README says Go 1.24 or newer, go.mod declares $go_directive" ;; esac

step "1. install the binary"
origin="$(git -C "$repository" remote get-url origin 2>/dev/null || echo "(none)")"
printf 'origin: %s\n' "$origin"
case "$origin" in (*mason-bryant/yoyodyne*) pass "README's clone URL names this checkout's origin (reachability not checked)" ;;
  (*) fail "README names $readme_clone_url, origin is $origin" ;; esac

# The README leads its install section with `go install`, which needs the module
# to be named by its repository path. That is a property of go.mod rather than a
# policy, so it is checked rather than trusted.
module="$(sed -n 's/^module \(.*\)$/\1/p' "$repository/go.mod")"
printf 'module: %s\n' "$module"
if [ "$module" = "$readme_install_module" ]; then
  pass "go.mod names the module the README's \`go install\` line names"
else
  fail "README installs $readme_install_module, go.mod declares $module"
fi
# Whether the tag is actually fetchable needs the network and a published tag,
# neither of which this script assumes. What it can settle offline is that the
# path is well-formed as a module path at all, which is exactly what the old
# `module yoyodyne` was not.
install_output="$(GOBIN="$scratch/gobin" go install "$readme_install_module/cmd/yoyo@latest" 2>&1 || true)"
missing "$install_output" "malformed module path" "\`go install\` reaches the proxy rather than refusing the path"
if [ -x "$scratch/gobin/yoyo" ]; then
  pass "go install $readme_install_module/cmd/yoyo@latest produced a binary"
  printf 'installed version: %s\n' "$("$scratch/gobin/yoyo" version)"
else
  skip "go install of a published tag: needs network access and a pushed tag, neither assumed here"
fi

run make -C "$repository" build >/dev/null
yoyo="$repository/bin/yoyo"
if [ -x "$yoyo" ]; then pass "make build wrote ./bin/yoyo"; else fail "make build did not write ./bin/yoyo"; fi
# The README says a from-source build names the commit it came from rather than
# only saying "dev", which is a claim about the Makefile's version stamp.
built_version="$("$yoyo" version)"
printf 'built version: %s\n' "$built_version"
if [ "$built_version" = "dev" ]; then
  fail "make build reported \"dev\" rather than a git description"
else
  pass "yoyo version names the build it came from"
fi

step "the scratch project: not this repository, not Go"
mkdir -p "$project/tests"
cd "$project"
git init -q -b main .
git config user.email walk@example.invalid
git config user.name "Adoption Walk"
printf 'def add(a, b):\n    return a + b\n' > calc.py
: > tests/__init__.py
cat > tests/test_calc.py <<'PY'
import unittest

from calc import add


class TestCalc(unittest.TestCase):
    def test_add(self):
        self.assertEqual(add(2, 2), 4)
PY
git add -A
git commit -qm "a tiny Python project"
pass "created a Python project with one commit at $project"

step "2. initialize the tracker"
run bd init >/dev/null 2>&1
if [ -d "$project/.beads" ]; then pass "bd init created .beads"; else fail "bd init created no .beads"; fi
run bd ready >/dev/null
pass "bd ready answers in the scratch project"

step "3. write the configuration"
run "$yoyo" init
for persona in architect developer development-manager product-manager reviewer; do
  if [ -f "$project/.yoyodyne/personas/$persona.md" ]; then
    pass "wrote personas/$persona.md"
  else
    fail "did not write personas/$persona.md"
  fi
done
refusal="$("$yoyo" init 2>&1 || true)"
contains "$refusal" "pass --force to overwrite it" "a second init refuses rather than overwriting"

step "4. init proposes checks from what this project already declares"
# This project keeps its tests in tests/ and names no runner anywhere, so there
# are two plausible commands and nothing here says which one is the gate. What
# init must not do is pick one: it leaves both commented under a marker and
# leaves the list empty.
if grep -q '^checks: \[\]' .yoyodyne/config.yaml; then
  pass "an undecidable toolchain leaves checks empty rather than guessing"
else
  fail "a checks list was written for a project that names no test runner"
fi
if grep -q '^# YOU MUST CHOOSE' .yoyodyne/config.yaml; then
  pass "the candidates carry an explicit you-must-choose marker"
else
  fail "the candidates carry no you-must-choose marker"
fi
for candidate in "#  - python3 -m pytest -q" "#  - python3 -m unittest discover -q -s tests -t ."; do
  if grep -qF "$candidate" .yoyodyne/config.yaml; then
    pass "offers a candidate, commented out: $candidate"
  else
    fail "offers no candidate: $candidate"
  fi
done
if grep -qF "tests/test_calc.py" .yoyodyne/config.yaml; then
  pass "names the file the candidates were derived from"
else
  fail "names nothing the candidates were derived from"
fi
for language in "# Go" "# TypeScript / Node" "# Python" "# Java (Maven)"; do
  if grep -qF "$language" .yoyodyne/config.yaml; then
    pass "carries a commented $language example"
  else
    fail "carries no commented $language example"
  fi
done

# A project that does say which runner it uses gets a list it can run instead.
# `init --json` reports what was detected and what was written, so this is
# asserted rather than eyeballed. Both of the projects below are their own
# scratch directories, so the documented walk above is left undisturbed.
decided="$scratch/decided"
mkdir -p "$decided"
printf '[tool.pytest.ini_options]\naddopts = "-q"\n' > "$decided/pyproject.toml"
report="$("$yoyo" init --directory "$decided" --product decided --json)"
printf '%s\n' "$report"
summary="$(printf '%s' "$report" | python3 -c '
import json, sys

payload = json.load(sys.stdin)
written = ";".join(payload["checks"])
sources = ";".join(entry["source"] for entry in payload["detected"]["checks"])
print("written: %s from: %s" % (written, sources))
')"
contains "$summary" "python3 -m pytest -q" "a project that names pytest gets a runnable check written"
contains "$summary" "pyproject.toml" "the report names the file the check was derived from"
if grep -q '^  # from pyproject.toml' "$decided/.yoyodyne/config.yaml"; then
  pass "the written check carries its provenance as a comment"
else
  fail "the written check carries no provenance comment"
fi

# Detection reads; it does not run. A Makefile whose target would leave a trace
# is how that is checked from outside: the target is detected and its trace is
# never written.
untrusted="$scratch/untrusted"
mkdir -p "$untrusted"
printf 'check:\n\ttouch %s/ran\n' "$untrusted" > "$untrusted/Makefile"
"$yoyo" init --directory "$untrusted" --product untrusted >/dev/null
if grep -q '^  - make check$' "$untrusted/.yoyodyne/config.yaml"; then
  pass "a Makefile's check target is proposed as the project's gate"
else
  fail "a Makefile's check target was not proposed"
fi
if [ -e "$untrusted/ran" ]; then
  fail "init executed a target from the project it was configuring"
else
  pass "init read the Makefile without running anything in it"
fi

step "5. an empty checks list validates, but a run refuses it"
validate="$("$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "config validate passes with checks: []"

# Something has to be in the tracker before a run can be refused for anything
# else, so the first work item is filed here rather than where the README files
# one, which is inside its third step.
bd create --title="Add a subtract function" \
  --description="calc has add and nothing else. Add subtract(a, b) with a test." \
  --type=feature --priority=2 >/dev/null 2>&1
item="$(bd ready --json 2>/dev/null | python3 -c '
import json, sys
payload = json.load(sys.stdin)
issues = payload if isinstance(payload, list) else payload.get("issues", payload)
print(issues[0]["id"])
')"
printf 'work item: %s\n' "$item"
refusal="$("$yoyo" run "$item" 2>&1 || true)"
contains "$refusal" "requires at least one configured check" "yoyo run refuses a run with no checks"

step "6. choose one of the candidates init offered"
# The README says choosing costs one character: open the empty list, then delete
# the leading "#" from the candidate that belongs. That is done here exactly as
# written rather than by writing the line out again, because the claim being
# checked is that the generated file can be edited that way.
chosen="python3 -m unittest discover -q -s tests -t ."
python3 - "$chosen" <<'PY'
import pathlib
import sys

chosen = sys.argv[1]
path = pathlib.Path(".yoyodyne/config.yaml")
text = path.read_text()
if "#  - " + chosen + "\n" not in text:
    raise SystemExit("init offered no candidate for %r" % chosen)
text = text.replace("checks: []\n", "checks:\n", 1)
text = text.replace("#  - " + chosen + "\n", "  - " + chosen + "\n", 1)
path.write_text(text)
PY
pass "uncommented the chosen candidate in place"
effective="$("$yoyo" config show --effective 2>&1)"
contains "$effective" "$chosen" "the uncommented candidate is the effective checks list"
# The README says each entry runs through /bin/sh -c and must exit non-zero on
# failure, so the declared command is executed exactly that way.
if /bin/sh -c "$chosen" >/dev/null 2>&1; then
  pass "the chosen check passes through /bin/sh -c"
else
  fail "the chosen check does not pass through /bin/sh -c"
fi
validate="$("$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "config validate passes with the project's own checks"
mkdir -p src/nested
validate="$(cd src/nested && "$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "configuration is discovered from a subdirectory"
rm -rf src

step "7. write down what the product is for"
mkdir -p docs/product
cat > docs/product/calc.md <<'MD'
# Calc

A tiny arithmetic library, kept small enough that a change to it is obvious.

## Goals

- Arithmetic is correct for the operations the library claims to support.
- Every operation has a test that would fail if the operation broke.
MD
pass "wrote a specification with an introduction and goals"

step "8. a run refuses an uncommitted primary checkout, and names the files"
refusal="$("$yoyo" run "$item" 2>&1 || true)"
contains "$refusal" "uncommitted changes" "the run refuses an uncommitted primary checkout"
contains "$refusal" ".yoyodyne/config.yaml" "the refusal names the file that is dirty"
contains "$refusal" "docs/product/calc.md" "the refusal names every file that is dirty"

# The README also says the tracker's own exports are excepted from that refusal.
# Reaching that case through `yoyo run` would put a developer agent on the item,
# so it is verified where it is enforced instead.
( cd "$repository" && run go test ./internal/gitworktree \
  -run TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges -count=1 )
pass ".beads/issues.jsonl and .beads/interactions.jsonl are excepted from that refusal"

git add -A
git commit -qm "adopt yoyo"
pass "committed the adoption"

step "9. the commands the README points a new project at"
reconcile="$("$yoyo" reconcile 2>&1)"
contains "$reconcile" "no runs need reconciliation" "yoyo reconcile reports nothing outstanding"
invariants="$("$yoyo" invariant list 2>&1)"
contains "$invariants" "no invariants are recorded" "a project with no invariants directory simply has none"
origins="$("$yoyo" config show --origins 2>&1)"
contains "$origins" "$project/.yoyodyne/config.yaml" "config show --origins names the project file"
missing "$origins" "builtin:v1" "nothing is inherited from the built-in bundle"

step "10. following a run or a conversation"
# yoyo-status resolves the state directory the same way the harness does, which
# is what makes the temporary state root above enough to keep this off an
# operator's real runs. Both spellings are exercised. What it reports about
# runs and conversations is checked by scripts/yoyo-status-test.sh, which needs
# no provider and no repository to build both.
# Both are pointed at roots that hold nothing, so what is being checked is which
# directory the tool resolved rather than what happens to be in this walk's own.
status_listing="$(YOYODYNE_STATE_HOME="$scratch/named" \
  "$repository/bin/yoyo-status" -l 2>&1 || true)"
contains "$status_listing" "no Yoyodyne state at $scratch/named/products" \
  "yoyo-status honors YOYODYNE_STATE_HOME"
status_listing="$(env -u YOYODYNE_STATE_HOME XDG_STATE_HOME="$scratch/xdg" \
  "$repository/bin/yoyo-status" -l 2>&1 || true)"
contains "$status_listing" "no Yoyodyne state at $scratch/xdg/yoyodyne/products" \
  "yoyo-status honors XDG_STATE_HOME by appending yoyodyne"
if ! command -v jq >/dev/null 2>&1; then
  cost="$("$repository/bin/yoyo-status" -c 2>&1 || true)"
  contains "$cost" "needs jq" "yoyo-status -c says it needs jq when jq is absent"
fi

step "11. drive it from the conversation"
if [ "${WALK_PROVIDER:-0}" = "1" ]; then
  # Everything up to here is free. This is the step that spends capacity, so it
  # only runs when it was asked for. What is asserted is that the harness got
  # all the way to the provider: no configuration, tracker, or repository
  # refusal stood between the documented steps and a developer being asked.
  outcome="$("$yoyo" run "$item" 2>&1 || true)"
  printf '%s\n' "$outcome"
  missing "$outcome" "requires at least one configured check" "the run got past the checks gate"
  missing "$outcome" "uncommitted changes" "the run got past the repository readiness gate"
  contains "$outcome" "run-" "a run was created and reported an outcome by id"

  # There is a run to look at now, so the tool the README points at for watching
  # one is exercised against a real record rather than an empty directory.
  listing="$("$repository/bin/yoyo-status" -l 2>&1 || true)"
  printf '%s\n' "$listing"
  contains "$listing" "run-" "yoyo-status -l lists the run"
  if command -v jq >/dev/null 2>&1; then
    cost="$("$repository/bin/yoyo-status" -c 2>&1 || true)"
    printf '%s\n' "$cost"
    case "$cost" in
      (*"USD"*|*"TOTAL"*|*"no completed provider invocations"*)
        pass "yoyo-status -c reports spend when jq is installed" ;;
      (*"Operation not permitted"*|*"mkstemp failed"*)
        # The report aggregates through a temporary file under TMPDIR, and a
        # restricted environment can deny even that. That is the environment
        # refusing the tool, not the tool being wrong.
        skip "yoyo-status -c: this environment denies the temporary file it needs" ;;
      (*)
        fail "yoyo-status -c produced no report -- got: $cost" ;;
    esac
  fi

  chat="$("$yoyo" chat --message "What should we do first?" 2>&1 || true)"
  printf '%s\n' "$chat"
  contains "$chat" "context gathered" "the conversation opened and gathered its picture"
else
  printf 'skipped: set WALK_PROVIDER=1 to invoke the provider on this step.\n'
  printf 'Everything before it ran without one.\n'
fi

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'the documented adoption path works as written\n'
else
  printf '%d documented claim(s) did not hold\n' "$failures"
fi
if [ "$skips" != "0" ]; then
  printf '%d claim(s) this environment could not exercise, named above\n' "$skips"
fi
exit "$failures"
