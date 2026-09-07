# yoyodyne-ifd.310: the link checker is already in the gate, and the script the item names is on branches that never landed

`yoyodyne-ifd.310` was admitted on 2026-09-06 to close an enforcement gap: five
reports across three weeks said `scripts/check-doc-links.py` was tracked and
that nothing ran it, so link resolution across this repository was held only by
a reviewer reading a diff.

**Both halves of that premise are false of main, and they are false for two
different reasons.** There is no `scripts/check-doc-links.py` in the tree this
run was cut from, and link resolution has been a declared check since
2026-08-23. This is the record of how that was established — from the
repository's own history and from checks executed in this worktree rather than
from memory — so the next run dispatched on the same premise reads it instead of
deriving it again.

Base commit: `205ccea`.

## Where the script the reports saw actually lives

`scripts/check-doc-links.py` was written three times and landed none of them.
Every commit that adds it is on a preserved developer branch that is not an
ancestor of main, which is why `git log` finds the file and `git ls-files` does
not:

| commit | date | branch it is on | ancestor of `205ccea` |
| --- | --- | --- | --- |
| `37e7ea6` | 2026-08-19 | `yoyodyne/yoyodyne-ifd-121/9f79dcee` | no |
| `4708bc0` | 2026-08-20 | `yoyodyne/yoyodyne-ifd-121/6ff896ba` | no |
| `b07e8f8` | 2026-08-22 | `yoyodyne/yoyodyne-ifd-121-5/79127d5f` | no |

All three are `A` additions with no matching deletion anywhere in the history:
the file was never removed from main because it was never on it. The first two
are the yoyodyne-ifd.121 attempts the harness stopped after their reviewer still
required repair — those branches are preserved deliberately, which is what makes
the file findable — and the third is an earlier yoyodyne-ifd.121.5 attempt
superseded by the tranche that landed on 2026-09-05.

That is the whole of the reporting error. A report naming a path that a
preserved branch carries reads exactly like a report naming a path main carries,
and nothing in the report says which was searched.

## What enforces link resolution instead, and since when

`internal/doclink` landed in `c32be41` on 2026-08-23 under yoyodyne-ifd.85, with
its repository-wide gate in the same commit, and `850d087` extended it on
2026-09-07 under yoyodyne-ifd.315. It is a superset of the abandoned script:
relative paths, fragments under GitHub's slug rules, repeated-slug suffixes, and
citations from Go source are what the Python did; citations from YAML and shell,
and forge blob-view URLs, are what the Go added.

So the mechanism the item asks for was in the gate two weeks before the item was
admitted, under a different name and in a different language than the reports
were looking for.

## What satisfies each of the item's three requirements

| the item asks for | what satisfies it at `205ccea` |
| --- | --- |
| the checker runs in `make check` | `check` is `fmtcheck test race vet` in the [`Makefile`](../../Makefile), and `test` is `go test ./...`, which runs `TestThisRepositoryOwnDocumentationLinksResolve` in `internal/doclink/repository_test.go` over this checkout |
| a deliberately broken link in a test fixture fails the gate naming the link | `TestALinkToADocumentThatIsNotThereIsReported` and `TestALinkToAHeadingTheTargetDoesNotCarryIsReported` in `internal/doclink/doclink_test.go` each build a fixture repository with one broken link and assert the reported path, line, and target. The mutations below establish the same thing against this repository rather than against a fixture |
| [`docs/docs-map.md`](../docs-map.md)'s closing note says it is admitted and wired | the note was already corrected: [what the split breaks that neither item mentions](../docs-map.md#what-the-split-breaks-that-neither-item-mentions) has read "**That checker now exists**" since the README split's last tranche. This run added one sentence to it naming `make check` and pointing here |

The item's fourth clause — whether the checks list in `.yoyodyne/config.yaml`
should name the checker directly, left as the operator's paste — needs no paste.
That list is `make fmtcheck`, `make test`, `make race`, `make vet`, and the
checker is inside `make test`. Naming it separately would run it a third time
and gain nothing.

## The checks, run in this worktree at `205ccea`

- `make check` — the four declared checks — exit 0, before any edit. 71 packages
  green under `go test`, and again under `go test -race`; no failures.
- `go test ./internal/doclink/ -count=1` — exit 0.

## What the mutations establish

A passing gate says nothing on its own about whether it would notice a link
going bad. Each mutation below added a deliberately broken link to
`docs/docs-map.md`, ran the named check, and was reverted; the worktree carries
none of them, and `git status` was clean after each.

| what was broken | what was run | what it said |
| --- | --- | --- |
| a link to a file that is not in the repository, and a fragment naming a heading `docs-map.md` does not carry | `go test ./internal/doclink/ -run TestThisRepositoryOwnDocumentationLinksResolve` | exit 1, two failures: *docs/docs-map.md:948: it links to "docs-map-that-is-not-there.md", and docs/docs-map-that-is-not-there.md is not in the repository*, and *…docs/docs-map.md carries no heading with that anchor* |
| a link to a file that is not in the repository | `make check` | exit 2, red at the `test` step, naming the same file, line, and target; `race` and `vet` never reached |

Both name the document making the link, the physical line, and the target as
written, which is what the item asked a failure to do.

## What remains

Nothing is unbuilt, and no development is owed. What remains is closing the
item, and that is the product manager's to do rather than this run's.

One thing is worth admitting to the backlog on the strength of this, and is
named here rather than filed, because a developer run does not admit work: a
report that names a repository path has no way to say which revision it was
searched on, and a preserved branch makes a path that never landed look tracked.
Five reports and one admitted item were spent on that ambiguity here.
