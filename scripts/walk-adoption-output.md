# The adoption walkthrough, run against this README

*The recorded output of `make adoption` — [`scripts/walk-adoption.sh`](walk-adoption.sh) —
against the README this change delivers. It is here because yoyodyne-ifd.121.5
made it the gate on the README split's reduction rather than leaving the
reduced README to review alone: a section that moved out of the README is a
step somebody following it can no longer take, and only executing the section
says whether that happened.*

**This is a record and not a check.** Nothing re-runs it, and it goes stale the
moment the README's install or getting-started sections move. Re-run
`make adoption` and replace this file rather than citing it if they have.

## What it was run against

| | |
|---|---|
| Run on | 2026-09-05 |
| `HEAD` at the walk | `11c45d2e8d235b38ba8d1b09b3b7d294a1d5b6f5` — this branch's tip, which carries every README edit this change makes |
| `README.md` sha256 | `9db4bdab2b1a33c945145bb4201f351572ed495033185f8f82f9730246203771` |
| Modified in the working tree at the walk | `docs/developing-yoyo.md`, `internal/doclink/doclink.go`, `internal/doclink/doclink_test.go` — this file is the fourth, written afterwards from the output below |

**The digest is what binds this record, not the commit**, because a developer
run makes no commits: the walk runs against a working tree, and the harness
commits it afterwards under a hash that did not exist while the walk was
happening. So `HEAD` above names where the walk stood and the digest names what
it actually walked. A README that no longer hashes to that value is not the one
these steps were taken against, whatever any commit says.

`yoyo version` inside the output reports `-dirty` for the three files listed
above. None of them is `README.md`, which was unmodified against `HEAD` when the
walk ran, so the README exercised here is the README being merged, byte for
byte.

## Result

**`the documented adoption path works as written`** — 57 assertions passed, none
failed, and one claim was named as unexercisable rather than passed over: `go
install` of a published tag needs network access and a pushed tag, and this
environment has neither. Step 12, which hands an item to a developer agent,
is opt-in behind `WALK_PROVIDER=1` and was not run; everything before it needs
no provider.

## The output

```text
scripts/walk-adoption.sh

=== prerequisites: the build requirement the README states
go.mod declares go 1.24.0
  ok: README's "Go 1.24 or newer" matches go.mod

=== 1. install the binary
origin: git@github.com:mason-bryant/yoyodyne.git
  ok: README's clone URL names this checkout's origin (reachability not checked)
module: github.com/mason-bryant/yoyodyne
  ok: go.mod names the module the README's `go install` line names
  ok: `go install` reaches the proxy rather than refusing the path
go env GOPATH: /Users/mbryant/go
  ok: the documented default destination, ~/go/bin, is where go would install
  SKIPPED: go install of a published tag, and the yoyo version check on it: needs network access and a pushed tag, neither assumed here
  ok: make build wrote ./bin/yoyo
built version: v0.4.0-254-g11c45d2-dirty
  ok: yoyo version names the build it came from

=== the scratch project: not this repository, not Go
  ok: created a Python project with one commit at /tmp/claude-501/yoyodyne-walk.TvXL8X/calc

=== 2. initialize the tracker
  ok: bd init created .beads
  ok: bd ready answers in the scratch project

=== 3. write the configuration
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/config.yaml
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/personas/architect.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/personas/developer.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/personas/development-manager.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/personas/product-manager.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/personas/reviewer.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/config.lock
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/docs/product/README.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/docs/product/goals/README.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/docs/designs/README.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/docs/decisions/README.md
wrote /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/docs/decisions/invariants/README.md
the configuration is complete and inherits nothing
checks is empty: 2 candidates are commented in /tmp/claude-501/yoyodyne-walk.TvXL8X/calc/.yoyodyne/config.yaml, and one of them has to be chosen before work can run
the tracker already syncs through origin: git+file:///tmp/claude-501/yoyodyne-walk.TvXL8X/origin.git; name a URL with --tracker-remote to point it elsewhere
  ok: wrote personas/architect.md
  ok: wrote personas/developer.md
  ok: wrote personas/development-manager.md
  ok: wrote personas/product-manager.md
  ok: wrote personas/reviewer.md
  ok: a second init refuses rather than overwriting
  ok: init leaves the tracker syncing over the project's Git remote
origin               git+file:///tmp/claude-501/yoyodyne-walk.TvXL8X/origin.git
  ok: bd holds the sync remote init configured

=== 4. init proposes checks from what this project already declares
  ok: an undecidable toolchain leaves checks empty rather than guessing
  ok: the candidates carry an explicit you-must-choose marker
  ok: offers a candidate, commented out: #  - python3 -m pytest -q
  ok: offers a candidate, commented out: #  - python3 -m unittest discover -q -s tests -t .
  ok: names tests/test_calc.py as where the candidates were derived from
  ok: carries a commented # Go example
  ok: carries a commented # TypeScript / Node example
  ok: carries a commented # Python example
  ok: carries a commented # Java (Maven) example
{"bundle":"builtin:v1","checks":["python3 -m pytest -q"],"config":"/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/config.yaml","detected":{"checks":[{"command":"python3 -m pytest -q","source":"pyproject.toml"}],"candidates":[],"alternatives":[]},"external":false,"files":["/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/config.yaml","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/personas/architect.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/personas/developer.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/personas/development-manager.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/personas/product-manager.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/personas/reviewer.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/.yoyodyne/config.lock","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/docs/product/README.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/docs/product/goals/README.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/docs/designs/README.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/docs/decisions/README.md","/tmp/claude-501/yoyodyne-walk.TvXL8X/decided/docs/decisions/invariants/README.md"],"ignored":{"ignored":false},"repository":"/tmp/claude-501/yoyodyne-walk.TvXL8X/decided","status":"written","tracker":{"status":"skipped","reason":"fatal: not a git repository (or any of the parent directories): .git"}}
  ok: a project that names pytest gets a runnable check written
  ok: the report names the file the check was derived from
  ok: the written check carries its provenance as a comment
  ok: a Makefile's check target is proposed as the project's gate
  ok: init read the Makefile without running anything in it
  ok: the superseded Go commands are headed as offered, not as owed
  ok: the superseded Go commands are still shown, commented out
  ok: a configuration that already runs demands nothing

=== 5. an empty checks list validates, but a run refuses it
  ok: config validate passes with checks: []
work item: calc-q2a
  ok: yoyo run refuses a run with no checks

=== 6. choose one of the candidates init offered
  ok: uncommented the chosen candidate in place
  ok: the uncommented candidate is the effective checks list
  ok: the chosen check passes through /bin/sh -c
  ok: config validate passes with the project's own checks
  ok: configuration is discovered from a subdirectory

=== 7. doctor checks the whole installation, not only the file
  ok: doctor calls this project's configuration, repository, tracker, and checks healthy, and carries a remedy for everything it does not

=== 7b. yoyo setup reaches the same state by asking, and again changes nothing
  ok: setup converges a blank project, and doctor then calls its configuration, repository, tracker, and checks healthy
  ok: running setup again reports what is already true and changes nothing

=== 8. write down what the product is for
  ok: wrote a specification with an introduction and goals

=== 9. a run refuses an uncommitted primary checkout, and names the files
  ok: the run refuses an uncommitted primary checkout
  ok: the refusal names the file that is dirty
  ok: the refusal names every file that is dirty
$ go test ./internal/gitworktree -run TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges -count=1
ok  	github.com/mason-bryant/yoyodyne/internal/gitworktree	0.484s
  ok: .beads/issues.jsonl and .beads/interactions.jsonl are excepted from that refusal
  ok: committed the adoption

=== 10. the commands the README points a new project at
  ok: yoyo reconcile reports nothing outstanding
  ok: a project with no invariants directory simply has none
  ok: config show --origins names the project file
  ok: nothing is inherited from the built-in bundle

=== 11. following a run or a conversation
  ok: yoyo-status honors YOYODYNE_STATE_HOME
  ok: yoyo-status honors XDG_STATE_HOME by appending yoyodyne

=== 12. drive it from the conversation
skipped: set WALK_PROVIDER=1 to invoke the provider on this step.
Everything before it ran without one.

=== result
the documented adoption path works as written
1 claim(s) this environment could not exercise, named above
```
