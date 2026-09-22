# yoyodyne-ifd.380: the invariants index stopped being reported as a gap on 2026-08-30

Work item: yoyodyne-ifd.380, admitted 2026-09-18 from fifteen reports dated
2026-08-23 through 08-27. **The condition it states is already true, and has
been for 23 days.** The item anticipated this: *if the loader already exempts
it, the fix is the deployed binary, and the item closes on that evidence rather
than a code change.* This document is that evidence, written from the
repository, the durable run records, and the tracker's export, so nobody
diagnoses it a third time.

No code changed under this item. The loader's exemption, the refusal of the
name, and both of the tests that hold them were already in the tree this run was
cut from, base commit `fe638d2`.

## Which of the two accounts was right

The item records two explanations that disagree. The developers on
yoyodyne-ifd.174 and .176 were right, and the development manager's account —
that the artifact store exempts indexes while the invariant reader does not —
described the code as it read before 2026-08-23 21:05.

| when | what landed | where |
| --- | --- | --- |
| 2026-08-23 21:05 -0700, `e5187ae`, yoyodyne-ifd.186 | the `Load` skip, the refusal of `readme` as a constraint id, and the fixture test | `internal/invariant/store.go:108`, `:562`, `TestTheDirectoryIndexIsNeitherLoadedNorReported` |
| 2026-08-30 22:24 -0700, `58683ae`, yoyodyne-ifd.201 | the test asserted against this repository's own invariants home, and nothing else — that run found the code already right | `TestThisRepositorysInvariantsHomeLoadsWithNoGapToReport` |

So the fix was merged within hours of the first report, and yoyodyne-ifd.201 was
itself an item admitted for work that was already done. yoyodyne-ifd.380 is the
same shape again, one month later.

## What the runs actually recorded

Every run writes what the delivered set was missing onto its work item, as
`Invariant not delivered:` lines (`renderInvariantNotes`,
`internal/orchestrator/pipeline.go:6832`). Matching those lines in the tracker's
export back to the run records under the state directory gives the whole window
rather than a sample of reports:

| | |
| --- | --- |
| runs that recorded the phantom gap | 41, across 37 work items |
| first | `run-68a2632e274a`, started 2026-08-24T00:36:56Z |
| last | `run-af66cb33c6b3`, started 2026-08-30T06:30:10Z |
| first run recorded clean afterwards | `run-ce3a113500ea`, started 2026-08-30T14:58:13Z |
| runs started since that one | 389, none of them recording the gap |

The deployed binary was therefore rebuilt in the eight-hour window between
06:30Z and 14:58Z on 2026-08-30 — six days after the merge that fixed it, which
is the whole of why the reports kept arriving. Nothing else changed in that
window: `e5187ae` is the only commit that touched the loader's directory scan.

## What is true of the binary the harness runs now

Read in this run's worktree on 2026-09-22, against
`/Users/mbryant/github/yoyodyne/bin/yoyo`, built 05:35 that morning:

```
$ yoyo version
v0.5.0-62-gfe638d2-dirty
$ yoyo invariant list        # 9 active constraints listed, nothing on stderr
```

`yoyo invariant list` names an unreadable file on stderr and said nothing, so
the binary that dispatches runs skips the index. This run's own delivered
context says the same from the other side: it opens `9 of the 9 active
invariant(s) recorded here were selected as relevant` and carries no
`## Invariants that could not be read` section, which `Delivery.Text` emits
whenever `Problems` is non-empty (`internal/invariant/invariant.go:473`).

**One exemption covers both halves of the item.** The run's context and the
reviewer's evidence are two renderings of one `invariant.Set`, loaded once per
run (`loadInvariants`, `pipeline.go:1843`, read by `deliveredInvariants` and
`reviewedInvariants`); `yoyo review` loads the same store the same way
(`branchreview.go:317`). There is no second directory scan that could disagree.

## What holds it, and what a regression would look like

Two tests, both in `internal/invariant/invariant_test.go`: the fixture test for
the rule, and the home test asserted against `docs/decisions/invariants` itself.
Disabling the skip at `store.go:108` and running both, in this worktree on
2026-09-22, fails them with the reported wording verbatim:

```
--- FAIL: TestThisRepositorysInvariantsHomeLoadsWithNoGapToReport
    docs/decisions/invariants/README.md is reported as unreadable, so every run
    is told the invariant set is incomplete: it does not open with `---`
    frontmatter naming its id, title, status, origin, and revisions
--- FAIL: TestTheDirectoryIndexIsNeitherLoadedNorReported
```

The mutation was reverted; this change carries none of it. The gap channel
itself is untouched and still works:
`TestRelevantInvariantsReachTheDeveloperAndTheReviewerWithoutBeingTranscribed`
(`internal/orchestrator/invariant_test.go:24`) pins that a genuinely unreadable
constraint is named in the developer's context and written to the work item.

## The check to make next time

A report of this shape is a claim about the build that produced it, not about
the tree. Every run records the revision it ran (`Build`, set from
`buildinfo.Commit()` in `internal/cli/run.go:528`) and `yoyo status <id>` prints
it on the run's `ran under ... harness ...` line. Resolving a report's run id to
that revision and asking whether it contains the fix is a minute's work, and it
is what would have told the difference between a defect and a stale binary on
2026-08-25 rather than on 2026-09-22.
