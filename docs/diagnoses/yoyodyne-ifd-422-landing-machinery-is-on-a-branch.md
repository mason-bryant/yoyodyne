# yoyodyne-ifd.422: the landing checks this bounds are on a branch

yoyodyne-ifd.422 asks that landings serialize on one lease per target branch,
that a landing checkout use the repository's shared build cache, that a landing
made to wait says so in `yoyo status` and the sweep record, that a test drive
two landings and assert they serialize, and that the landing-checks section of
`docs/operations.md` say all of that.

Every one of those is a change to the landing checks — the machinery
yoyodyne-ifd.401 adds, which runs a configured `landing_checks` list once per
landing over a detached checkout of the integrated commit. The item's own
description says where it came from: *"Three reviewer reports on 401"*. That
machinery has not landed. It exists, whole and twice reviewed, on the branch of
yoyodyne-ifd.401 behind pull request #548, which is unmerged and parked on an
escalation to the operator. This run checked, found nothing on this base to
bound, and this is the record.

## The machinery is not here

These are the commands, written so they can be copied and run. They were run in
this worktree on 2026-09-19 at base commit `0c41031`.

```sh
# 1. does any Go on this base name the landing checks, their record, or the
#    checkout they run in?
grep -rnE 'LandingChecks|landing_checks|runLandingChecks|CheckoutCommit' \
  --include='*.go' internal cmd | wc -l

# 2. are the files 401 adds for them here?
ls internal/gitworktree/landing.go internal/orchestrator/landingchecks_test.go

# 3. does docs/operations.md have a landing-checks section to amend?
grep -n 'landing_checks\|landing check' docs/operations.md docs/configuration.md | wc -l

# 4. did ifd.401 land on main?
git log --oneline main --grep='ifd.401' | wc -l
git merge-base --is-ancestor 8412175 main; echo "exit=$?"
```

1. **0.** Not one line of Go on this base names any of the four.
2. **Neither exists.**
3. **0.** The word *landing* appears in `docs/operations.md` eight times, and
   every one of them is the developer's landing claim — `discharged`,
   `evidence`, `escalate` — which is a different thing with the same name. There
   is no section on landing checks to say anything in.
4. **0, and exit 1.** No commit on main mentions the item, and main does not
   contain the branch's tip.

Four negatives on names somebody chose are only as good as the guess behind
them, so the first pattern was run once more against the branch that does have
the code:

```sh
git show 8412175:internal/orchestrator/pipeline.go | grep -cE 'runLandingChecks|LandingChecks'
```

It answers **13**. The pattern matches; this base is what has nothing in it.

The machinery is real and it is elsewhere. `origin/yoyodyne/yoyodyne-ifd-401/26de6313`,
tip `8412175`, carries 3,848 lines across 61 files: `runLandingChecks` in
`internal/orchestrator/pipeline.go`, run inline after `deliveryCleanUp` with
no lease and no concurrency bound of any kind; the `runstate.LandingChecks`
record it writes; `Manager.CheckoutCommit` and `RemoveCheckout` in
`internal/gitworktree/landing.go`, which cut and remove the detached
`landing-<run>` checkout; the `landing_checks` and
`execution.landing_check_timeout` keys; and the section *What a check stage may
cost, and where the whole suite runs* in `docs/operations.md`, which is the
section this item's last clause is an amendment to. Its tracker record shows the
reviewer judging every visible part as meeting the acceptance criteria twice
over, and refusing both times because the bounded patch cut
`internal/runstate/state.go`, `summary.go`, and `internal/readmodel/standing.go`
— the record types this item would extend — with fixture JSON ordered ahead of
them; the second refusal was raised as an escalation, and the item is parked on
the operator's answer.

## Why nothing was written against it

**Every seam this item hooks is a symbol that does not exist here.** The lease
goes around `runLandingChecks`; the wait goes on `runstate.LandingChecks` and
is projected where `readmodel/standing.go` projects the landing; the test
drives two `activeRun`s through a landing; and the documentation amends a
section that is not in the file. Written on this base, none of it compiles and
the last of it has nothing to attach to.

**Writing it again would be writing it twice.** The alternative to depending on
those types is declaring them, which is re-authoring a twice-reviewed change so
that two versions of one record type arrive at main from two branches. The
second to integrate would be a conflict in exactly the files the reviewer has
twice been unable to see, rather than a contribution.

**Carrying the branch in would be integrating it.** Merging or cherry-picking
401's commits onto this branch would put somebody else's parked work inside this
change, which is the harness's promotion to make and not a developer's — and
would land 401 under this item's number, past the review bound that has stopped
it twice.

## What this item actually adds, once the machinery is here

This is the part worth keeping, so the run that picks the item up after 401
lands does not work it out again. Everything named here was read on `8412175`.

**One of the five clauses is already true on that branch, by construction.**
The item says the landing checkout runs against *"a cold Go build cache per
checkout"*. It does not. The landing checks are run through the same
`checks.Runner` the per-run gate uses, and that runner builds every check's
environment with `execution.WithGoBuildCache(nil, request.Directory)`
(`internal/checks/runner.go:143` on the branch), which resolves the checkout's
`.git` file to its administrative directory, follows `commondir` to the Git
directory every worktree of the repository shares, and points `GOCACHE` at
`<that directory>/yoyodyne/go-build` — the one cache every developer worktree
on this machine compiles against, and the one the harness exported into this
run (`GOCACHE=/Users/mbryant/github/yoyodyne/.git/yoyodyne/go-build`). The
landing checkout is cut with `git worktree add --detach` from the primary
repository (`internal/gitworktree/landing.go:64`), so it carries exactly that
bookkeeping. `TestEveryWorktreeOfARepositoryCachesInTheOneGitDirectory` in
`internal/execution/gocache_test.go` pins the resolution for a worktree of that
shape and passes on this base. What the successor run should add is not a
redirect but a test that a landing request's environment carries the
repository's cache — so the property holds by evidence rather than by
construction — and a sentence in the documentation saying it.

**What is left is four things, and the seams for each:**

1. **The lease.** `runstate.Store.LeasePromotion` in
   `internal/runstate/promotion.go` is the model: an advisory file lock per
   product and per target branch, named by the encoded branch, that the
   operating system drops when its holder dies, with a bounded wait that tells
   a cancelled caller and a caller that waited out the bound apart. A landing
   lease is its sibling — a second lock file per branch, `.landing-<branch>.lock`
   beside `.promotion-<branch>.lock` — and deliberately not the promotion lease
   itself: a promotion is one short step and its queue is bounded at fifteen
   minutes for that reason, while a landing suite runs for up to
   `landing_check_timeout` times the number of landing checks, and a landing
   holding the promotion lease would hold every run on the branch out of
   integration for that long. The bound on the landing queue wants to be the
   landing's own budget rather than the promotion's fifteen minutes, or the
   queue behind a two-hour suite ends unverified. It is taken in
   `runLandingChecks` between recording `StartedAt` and cutting the checkout,
   under the run's own lease, which the process already holds through the
   landing (the sweep's live-process case,
   `TestTheSweepLeavesALandingALiveProcessIsRunningAlone`, depends on that and
   is unchanged by this).

2. **The wait, named.** `runstate.LandingChecks` gains when the landing began
   waiting and when it was admitted, and `Describe()` says
   `landing checks waiting over <commit> behind another landing on <branch>,
   4m so far` while `FinishedAt` is nil and the lease is not yet held — its
   running case today says `landing checks running over <commit>, each bounded
   at <bound>`. That one method is where every surface reads the landing from:
   `yoyo status` prints `run.LandingChecks.Describe()` under the run
   (`internal/cli/status.go:1001` on the branch, off the `runstate.Summary`
   projection), so the wait reaches the terminal once the record carries it,
   and nothing in `readmodel/standing.go` needs a rendering of its own — the
   run is terminal by then, so it is not on the Running line. The sweep record
   is the reconcile sweep's settle line for a run whose landing is unfinished
   (`internal/orchestrator/reconcile.go:383` on the branch, which says
   `settled the landing checks the run's process died inside as unverified:`
   and then `Describe()`): a dead process found waiting is thereby settled as
   having died waiting rather than running, with `CloseInterrupted` saying
   which.

3. **The test.** `internal/orchestrator/landingchecks_test.go` already drives
   one landing end to end against a fake `LandingCheckouts` and a fake check
   runner. Two of them, started against one target branch with a check runner
   that blocks the first until the test releases it, assert that the second's
   record reads as waiting while the first runs, that the second's checkout is
   not cut until the first's is removed, and that the two never run a check at
   once. The lease is a file lock, so the two must be two `Store`s on one state
   root in one process, which
   `TestPromotionsIntoOneTargetBranchHappenOneAtATime` in
   `internal/runstate/promotion_test.go` already does for the promotion lease.

4. **The documentation.** The sentence to change in *Where the whole suite runs
   is once per landing* is *"the next run starts beside them"*: it stays true
   of developer runs and becomes false of a second landing, which now waits.
   The section's `unverified landing:` example gains the shape a landing takes
   when it waited out the bound, and the `yoyo status` example under *While
   the checks run* gains a waiting landing line.

**One thing to decide rather than assume.** The item bounds landings to one
suite at a time and leaves developer runs unbounded beside it, so with two
seats the load ceiling is one landing suite plus two narrowed gates. That is
what the operator's note on `max_concurrent_developers` in `.yoyodyne/config.yaml`
(*"Three needs the checks' load bounded first"*) was waiting on, and the
successor run should say in its summary whether one landing beside two gates is
the bound that note meant, or whether the landing should also wait while a
per-run check stage is running — which the item does not ask for and this
diagnosis does not recommend, since a landing that waits on every gate on a
busy machine never runs.

## What would release this item

1. **yoyodyne-ifd.401 lands.** Its change is checked green and judged, on
   everything the reviewer could see, to meet its criteria; what stops it is
   the review's own bound, which is the operator's to settle. Nothing else
   blocks this item, and nothing in it can be started before that.
2. **ifd.422 is released**, and the four things above are written against the
   machinery that then exists, with the build-cache clause pinned by a test
   rather than re-implemented.

**Since released and done.** yoyodyne-ifd.401 landed, and the four things
above were written against its code under yoyodyne-ifd.422 itself: the landing
lease (`runstate.Store.LeaseLanding`), the named wait in `yoyo status` and the
sweep record (worded "since <time>" rather than "<n> so far"), the two-landing
test in `internal/orchestrator/landingqueue_test.go`, the build-cache test in
`internal/gitworktree/landing_test.go`, and the landing-checks section of
`docs/operations.md`. This record is kept as the diagnosis it was.
