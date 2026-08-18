# TypeScript fixture project

A managed project the harness is driven over in `language_agnostic_test.go`. It
is deliberately not Go: no module, no `go.mod`, no Go source, and no check that
runs a Go command.

Invariant 11 of `docs/v1-harness-design.md` says the harness assumes no
language, build system, or test framework in the managed project — verification
is the commands the project declares, run in the worktree, and the harness
decides only whether they passed. Every other fixture in this repository is a
Go repository verified by Go commands, so the invariant would go on passing its
tests even on a harness that had quietly learned to expect one. This project is
what makes that fail instead.

The checks in `.yoyodyne/config.yaml` run the shell scripts in `scripts/`. They
are POSIX shell on purpose: what is being verified is that the harness needs no
knowledge of a toolchain, and a fixture that ran `npx tsc` would verify that the
CI image has Node.js installed as much as anything about Yoyodyne.
