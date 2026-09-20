# yoyodyne-ifd.239: bin/yoyo-status was already retired, and this is what proves it

The second developer run for yoyodyne-ifd.239 —
`run-734af671446321e851cf60ecdc6dc16b`, 2026-09-20 — was dispatched to retire
`bin/yoyo-status` and found the script gone from the tree it was cut from, base
commit `6c93d40`. This is the record of how that was established, what the tree
still carried of the script, and why the retirement stays a deletion rather than
becoming a wrapper — written from the repository and the run records rather than
from memory, so the item can close on evidence and the next reader does not
derive it again.

**One earlier landing did the work, and it is an ancestor of this branch.**

| run | integrated commit | pull request |
| --- | --- | --- |
| `run-6b8cd17f23957d781853b82dd7ecdcda` (yoyodyne-ifd.63) | `5f30b72` | [#531](https://github.com/mason-bryant/yoyodyne/pull/531), merged 2026-09-18 |

That commit is the last one in the history to touch either file, and what it did
to them is delete them: `bin/yoyo-status` (727 lines) and
`scripts/yoyo-status-test.sh` (688 lines) are removed outright, `yoyo status`
gained `--follow`, `--events`, `--list`, and `--spend` in
`internal/cli/statusstream.go`, and the documents that named the script —
`README.md`, `docs/operations.md`, `docs/conversation.md`, `docs/reporting.md`,
`docs/work.md`, `docs/developing-yoyo.md`, `scripts/walk-adoption.sh` — were
pointed at the verb in the same change. The decision to delete rather than wrap
is recorded twice there, in
[operations](../operations.md#following-a-run-a-conversation-or-a-branch-review)
and in the header of `internal/cli/statusstream.go`.

This item's first run, `run-618012e292ad070dfa96d0f028af0049`, was cut from
`a7285d9` on 2026-09-03, before ifd.63 landed, and reduced the script to a
wrapper whose banner says following an event stream is something nothing does
yet. It died opening its pull request — `error connecting to api.github.com` —
and its branch `yoyodyne/yoyodyne-ifd-239/618012e2` is preserved. Nothing on it
is worth carrying: every sentence it wrote about what the binary lacks stopped
being true fifteen days later.

## What the tree at `6c93d40` still carried of the script

Two references, both stale in the same direction — each written when the script
was present and never revisited when it went — and both corrected by this change:

| where | what it said | what it does now |
| --- | --- | --- |
| `.gitignore` | `!/bin/yoyo-status`, an un-ignore for a path with nothing at it | the line is gone; `!/bin/yoyo-account` stays, since that script is still tracked |
| `bin/yoyo-account`, the comment over `state_root` | mirrors the harness's state root "exactly as `bin/yoyo-status` does", present tense | says the script resolves a path and reads nothing under it, and that the shell script that derived state beside the binary is gone |

Everything else that names `bin/yoyo-status` at this commit is an account of
its retirement or of the day it was missed, not a claim that it exists:
`docs/operations.md`, `docs/configuration.md` (the 2026-08-18 briefing gap), the
`internal/cli/statusstream.go` header, the revision reason ifd.63 left on the
harness design, `docs/releases/v0.5.0.md`, and two test fixtures that use the
name as a string.

## What satisfies each of the item's done-means clauses

| the item asks for | what satisfies it at `6c93d40` plus this change | what pins it |
| --- | --- | --- |
| no shell derivation of run, conversation, exchange, or cost state remains | the shell this repository tracks is `bin/yoyo-account`, the four release scripts under `scripts/`, and `scripts/walk-adoption.sh`; none opens a run, conversation, exchange, or event record. `bin/yoyo-account` resolves the state root to place a login home and reads nothing under it; `walk-adoption.sh` calls `yoyo status --spend` and asserts on what the verb printed | `internal/composition` classifies every tracked shell file and `bash -n`s it, so a script arriving unlisted fails `make test` |
| the invariant's every-surface reading has no standing exception | no document, comment, or configuration names `bin/yoyo-status` as a surface that exists, and the only status surfaces are `yoyo status` and the read model behind it | the sweep above; `docs/operations.md` names the verb as the only follow surface |
| `-L` follow-latest is covered in the binary | `yoyo status --follow --latest`: the flag is declared at `internal/cli/status.go:134` and refused without `--follow` at `status.go:173` | `TestStatusFollowLatestDrainsTheStreamItLeaves` (`internal/cli/statusstream_test.go:426`) and `TestStatusRefusesStreamOptionsItCannotHonor` (`statusstream_test.go:595`), run at this commit with `-count=1`, passing |
| `scripts/yoyo-status-test.sh` follows its subject | deleted with the script in `5f30b72`; what covers the follow now is `internal/cli/statusstream_test.go` and `internal/runstate/stream_test.go` | `make test` |
| documentation references point at `yoyo status` | done in `5f30b72`; this change adds the flag-for-flag mapping to `docs/operations.md` for the reader whose fingers still know the old ones | `internal/doclink` resolves the link this change adds |
| an operator typing the old name still lands somewhere useful | the shell's *no such file or directory*, and the section below on why that is the better of the two answers | — |

## Why the retirement stays a deletion

The item lets the builder choose between deleting the script and leaving a
one-line `exec yoyo status "$@"` at the old name, with the operator's typing
habits in mind. The habit was `bin/yoyo-status -L` — following the newest run,
and moving when a later one starts, which is what yoyodyne-ifd.218 was about.
The script's flags were its own and none of them is the verb's:

| the script took | the verb takes |
| --- | --- |
| `-L`, `--follow-latest` | `--follow --latest` |
| `-l`, `--list` | `--list` |
| `-c`, `--cost` | `--spend` |
| `-n`, `--lines` | `--lines`, and only with `--follow` |
| `--runs`, `--chats`, `--reviews` | `--kind runs`, `--kind chats`, `--kind reviews` |
| nothing at all | followed the newest stream; the verb reports the run records |

So a one-line wrapper would answer `bin/yoyo-status -L` with
`flag provided but not defined: -L` and the verb's usage, and would answer a
bare `bin/yoyo-status` — the other habit — with the recorded report rather than
the follow it always was. That is the old name kept and the old meaning lost,
which is worse than the old name gone: an operator whose fingers typed it gets a
surface that looks like the one they meant and does something else. A wrapper
that translated the flags would carry logic of its own under the old name, a
shell surface tracking the verb's option set by hand, which is the drift ifd.63
retired the script to end. The checkout's daily reading is `./bin/yoyo status`
as [operations](../operations.md#what-became-of-the-runs-and-what-remains-of-them)
documents it,
so the old spelling is not one the daily habit still reaches for. Where it is
typed, *no such file* is at least a true answer, and the mapping above is now
in the section of operations that documents the verb.

## What was run

The minute-zero probe, `make check` at `6c93d40` before any edit: `fmtcheck`,
`test`, `race`, `vet`, exit 0. The same four after the change, exit 0, and the
two `--latest` tests above with `-count=1`.

## What remains

Nothing to build. The preserved branch `yoyodyne/yoyodyne-ifd-239/618012e2` from
the first run is wholly superseded by `5f30b72` and can be deleted whenever the
sweep or a person reaches it.
