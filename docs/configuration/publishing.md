# Configuring publishing, branches, and promotion

How a finished branch reaches the remote, what to do when you cannot push to
it, which branch is authoritative, what publishing needs from the machine, and
what happens when two promotions race for the same target.

[The configuration index](../configuration.md) lists the other guides.

## Publishing through pull requests

By default Yoyodyne is entirely local: it creates a branch and a worktree, runs
the work, and fast-forwards your target branch. Nothing is pushed, and a
repository with no remote never notices publishing exists.

A project opts in the way it opts in to automatic integration. **Both settings
matter**: publishing opens the pull request, and integration is what merges it.

```yaml
approvals:
  publishing: automatic
  integration: automatic   # required for the harness to merge what it opened

execution:
  remote: origin   # the default; name another remote if yours is not origin
```

If you cannot push to that remote — you are contributing to somebody else's
repository — name your fork as well:

```yaml
execution:
  remote: upstream     # where pull requests are opened; you need no push access
  push_remote: fork    # where run branches go; the one you can push to
```

With both on, a run works like this:

1. **The developer phase publishes.** When a developer attempt finishes, the
   harness commits its work under its own identity, pushes the run branch to
   `execution.remote` — or to your fork, if you named one; see [Publishing from
   a fork](#publishing-from-a-fork) — and opens a pull request against the
   target branch on `execution.remote`. Each repair attempt pushes
   onto the same branch and updates the same pull request, so one change never
   ends up with two places to be reviewed. This happens *before* the checks run:
   a pull request is where work is reviewed, and work that does not pass yet is
   exactly what a reviewer should be able to see.
2. **The reviewer's verdict merges it.** An approving verdict authorizes the
   merge, and the harness asks the forge to perform it — it never pushes your
   target branch. Nothing about the gate changes: the same passing checks, the
   same independent-reviewer evidence, and the same fast-forward rule that gate
   integration also gate the merge, and the remote target is checked again right
   before the call, so a target that moved in the meantime refuses the merge
   rather than having the forge reconcile it. That holds of a merge
   [reissued after a dropped connection](recovery.md#waiting-out-a-network-that-dropped)
   too: the check is made again on each attempt rather than once in front of
   them, so what authorizes a merge made after a wait is a reading of the target
   taken after that wait.
   The merge is asked for as of *when your branch protection is satisfied*
   rather than as of now, so required checks that are still running are waited
   for by the forge instead of refused seconds after the approval. Administrator
   override is never used to get past them. Waiting that way needs **"Allow
   auto-merge"** enabled in your repository settings, which is off by default;
   when it is off and nothing is holding the pull request back, the harness
   simply merges, so a repository without branch protection needs no setting
   changed at all. Only the combination of the two — something holding the
   request back and no way to queue the merge behind it — cannot be published
   to, and the run says exactly that and names the setting rather than
   reporting a merge that mysteriously fails.
3. **The merge method is a merge commit.** The harness names it rather than
   taking your repository's default, because it is the only method that puts the
   reviewed commit itself on your target branch. A squash replaces it with a
   commit nobody reviewed, and GitHub's rebase always rewrites what it merges —
   new committer, new SHA, even when the request needs no rebasing — so both
   would leave the remote carrying a copy of the work your local branch does not
   have. The method is recorded on the run and on the work item, along with the
   commit the merge produced.
4. **The merge is confirmed, then the branch is cleaned up** on both sides,
   locally and on the remote, on the same compare-and-swap evidence. The
   confirmation waits briefly and boundedly, because a forge's own record of a
   request can lag the merge it just performed. If the forge refuses outright —
   a request that conflicts with its base, a merge method the repository
   forbids — the run reports which requirement was unmet rather than a generic
   failure.
5. **A merge the forge queued ends the run rather than being waited for.** It
   lands minutes later, when your checks pass. The run reports the pull request
   as queued and finishes: your change is already in the local target branch,
   which is the authoritative one (on a
   [protected target](#a-protected-target-lands-through-its-pull-request) it is
   not, and nothing local moves until the forge merges), and the run branch stays on the remote
   because that is what the forge still has to merge. The work item is the one
   thing the run does **not** settle — it stays open, with the queued merge named
   on it, because closing it as integrated would record a publication that has
   not happened and may not. `yoyo reconcile` settles both afterwards: it asks
   the forge, and either finishes the publication and settles the item — closed,
   or put back in the backlog parked or waiting on a named impediment where the
   run's own records say its change does not discharge it, its landing claim or
   what its reviewer approved (merge
   commit recorded and your local target branch caught up
   onto the forge's merge commit, with the branch the merge consumed deleted
   afterwards as hygiene that cannot hold the closure up) — or, if the forge dropped the queued merge
   because something it required went unmet, records an outstanding publication
   and hands the item back to you with a blocker rather than closing it. It never
   merges anything itself: a requirement that stopped the forge is yours to
   satisfy, and re-arming a dropped merge is a bounded triage decision — one per
   publication, carried out by `yoyo triage rearm` — rather than something a sweep
   does.

`gh` is invoked by the harness and never by a developer or reviewer: no role is
given a credential, a tool, or a request to push or merge. For the reviewer that
is a hard boundary — it runs with no tools at all, so the role whose verdict
authorizes a merge has no way to perform one, and cannot be talked into merging
something the checks would have refused.

For the developer it is not. A developer has a shell in its worktree and runs
under your account, so it could in principle reach a `gh` you have
authenticated; what stands in the way is its backend's sandbox and the harness
contract in its prompt, not a boundary the harness enforces. What does hold is
that your local target branch is authoritative: work an agent pushed by itself
is not integrated by having been pushed, and a pull request merged behind the
harness's back moves the remote away from the local branch, which the harness's
own check of the remote target then refuses rather than force-resolves.

### Publishing from a fork

Everything above assumes you can push to the remote you publish into. When you
cannot — the ordinary situation for a contributor to somebody else's
repository — `execution.push_remote` names the remote your run branches go to
instead, and the pull request is opened across the two repositories:

```yaml
execution:
  remote: upstream
  push_remote: fork
```

Both remotes have to exist in your checkout. Add the fork the way you would add
any remote, and check both with `yoyo doctor`, which names whichever one is
missing:

```bash
git remote add fork git@github.com:you/theirproject.git
yoyo doctor
```

Nothing else about publishing changes, and nothing about it is a second mode.
Run branches are pushed to the fork, the pull request is opened against the
remote you publish into with the head qualified by your account, and every
question about the target branch — whether it may still be merged into, where
the forge's merge left it, catching your local branch up afterwards — is asked
of the repository the work is going into. The merged run branch is deleted from
the fork, because that is the only place it ever was.

Your fork's account is read from the fork remote's URL rather than configured
separately, so there is nothing to keep in step with it. A repository that does
not have the remote you named publishes nothing and says which remote it was
looking for, the same way a repository with no remote at all does.

### Publishing without automatic integration

`approvals.publishing: automatic` with `approvals.integration: human` is
supported and does exactly half of the above: the harness pushes and opens the
pull request, and then stops. **It merges nothing.** You get an open pull
request, a run branch that stays on the remote, and a preserved worktree; you
merge, and the harness never touches any of the three afterwards.

That is deliberate rather than a gap. Merging is a promotion, promotion is what
`approvals.integration` governs, and a harness that merged under a `human`
integration policy would be taking the decision that setting reserves for you.

| `publishing` | `integration` | What you get |
| --- | --- | --- |
| `human` | `human` | Local branch and worktree, preserved for you. |
| `human` | `automatic` | Local fast-forward into the target branch, artifacts removed. Nothing pushed. |
| `automatic` | `automatic` | Pull request opened, merged on approval — or queued with the forge until your required checks pass — and the branch removed locally, then on the remote once the merge has happened. |
| `automatic` | `human` | Pull request opened and left for you. Nothing merged, nothing cleaned up. |

### Which branch is authoritative

**The local target branch.** Your work is where that branch says it is. The
exception is a target branch the forge protects, where the forge's copy leads and
the local one follows it: see
[a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).

Merging is not a second promotion performed on the remote. The harness
fast-forwards the local target exactly as it always has, and the forge merges
the pull request carrying exactly that commit. One promotion, one reviewed
commit, the same commit on both sides.

The merge itself does not leave the two at the same commit, and no forge merge
method would: **the merge leaves the remote target at your local target plus one
merge commit**, made by the forge and identical in content. The last step of the
promotion is to catch your local branch up onto the remote, which is an ordinary
fast-forward onto a commit that already contains the promotion. Nothing is
rewritten, reset, or merged, and nothing is decided: that is the `git pull` you
used to run yourself.

A catch-up the harness cannot make cleanly is held rather than forced, and says
why:

- **Uncommitted work in your checkout that the incoming commits would
  overwrite.** The branch is left where it is and the file is named. The
  exception is the work tracker's own exports — `.beads/issues.jsonl` and
  `.beads/interactions.jsonl`, the same two a run is allowed to rewrite in your
  checkout while it works. They are derived from a store that is authoritative
  elsewhere, so their churn is discarded and the catch-up goes through.
- **A remote that has diverged from your local branch** — a history somebody
  rewrote, or work that reached the remote another way. Which of the two is
  right is your answer rather than the harness's, so it is reported and nothing
  moves.

A merge that landed after its run had finished, and any catch-up that was held,
are swept by `yoyo reconcile`, which also removes the leftover local branches of
settled runs whose work the target already carries. Catching a branch up takes
that branch's promotion lease, so it never races a run promoting into it.

Because the forge performs the merge, the harness checks that relationship
rather than assuming it. Before the merge, the remote target must contain the
commit your promotion was made from and carry exactly its content — that is what
tells a target another run already published into from someone else's work.
After the merge, it must contain the promoted commit itself, unrewritten. It
need not carry exactly its content: a merge that lands among others — ten held
requests merged in one sitting — leaves every promotion but the last under a
merge commit later merges have built on, and requiring equality there reported
nine confirmable publications as unconfirmable for good. What is recorded as the
merge commit is the one the forge names for the pull request, where that commit
is on the remote target with the promoted commit as a parent, or otherwise the
one found in the remote history with the promoted commit as a parent; the
forge's record never decides the confirmation, only what is recorded. A forge that rewrote the
commit is reported, not reconciled, and the run branch is left on the remote for
whoever decides which history is right.

If a promotion onto an unprotected target cannot be published — the forge is
unreachable, the remote target moved, or the forge refused the merge — the run
still succeeds and closes its item, and reports an *outstanding publication*. A forge that could not be reached
is [waited out and asked again](recovery.md#waiting-out-a-network-that-dropped) first, so
an outstanding publication over a dropped connection is one that went on being
dropped rather than one reset the next attempt would have survived. The change is integrated where
it counts; only its publication is unfinished, and it is reconciled by hand.
Nothing is ever force-pushed to resolve it. On a protected target the same
failures leave nothing integrated, so the run stops rather than closing the item;
the next section says how.

### A protected target lands through its pull request

Before it promotes, a publishing run asks the forge whether the target branch is
protected, both ways GitHub protects one: per-branch protection, and a ruleset
carrying a pull-request, required-status-check, or update rule. It is the same
question `make release` asks before a cut. The answer decides the order of the
two halves above.

**An unprotected target** is promoted exactly as described above: the local
target is fast-forwarded onto the reviewed commit, and the forge merges the pull
request carrying it.

**A protected target is never moved locally ahead of the forge.** The run
commits the change, checks that the local target still stands where the change
was written against, and asks the forge to merge, with the local target left
where it was. The reviewed commit reaches the remote only through the forge's
merge, and the local target follows by the catch-up above: a fast-forward onto
the remote, taken under the branch's promotion lease. So no ending of the run
leaves your local target ahead of the remote:

- **Merged.** The catch-up brings the local target onto the forge's merge
  commit, and the item closes.
- **Queued.** Nothing moves locally. The worktree and branch are kept, because
  nothing proves the change is on the target yet; `yoyo reconcile` catches the
  local target up once the forge merges, closes the item, and cleans up.
- **Refused or dropped**: a required review, a failing check, anything the
  forge would not merge. The change is on its pull request and on no target
  branch, so the item is not closed. The run stops and hands the item back with
  the forge's answer as the blocker, keeping the record `yoyo triage rearm`
  repeats the merge from once the requirement is met.
- **The remote target moved** after the landing was prepared. Nothing was
  promoted, so this is a lost race rather than a divergence: the local target
  is fast-forwarded onto the remote and the change is replayed onto it, with
  the checks and the review re-earned, under the same
  [retry budget](#losing-a-race-for-the-target-branch).
- **The process was killed** after the landing was prepared and before the
  forge's answer was heard. The run recorded the landing before asking, and
  `yoyo reconcile` settles it on what the forge says rather than on the local
  target, which says nothing here: merged is confirmed, caught up onto, closed,
  and cleaned up; queued is recorded as a queued landing and settled like any
  other; anything else hands the item to a person, as the run itself would have.

**Why.** On a protected target, promoting locally first strands commits. A
merge the forge refuses or holds leaves the local target ahead of the remote,
and every later run that has to bring the target onto the remote collides with
it. That happened on 2026-09-20 and again on 2026-09-24, and both times every
run stalled until the checkout was reset by hand.

**A forge that cannot be asked is treated as protecting the branch**, and the
run says so on the work item. The protected path costs an unprotected target
nothing it needs, since the change still lands by the merge, while the other
reading could move a branch the forge then refuses. Every publishing run
records which path it took, and why, as the `Target branch:` line in its notes
on the work item.

### What publishing needs

- A remote by the configured name. **Without one the run is purely local**,
  reports `publishing skipped`, and behaves exactly as it did before publishing
  existed. That is a property of the repository, not an error.
- The GitHub CLI, installed and authenticated (`gh auth login`). If a project
  asked to publish and `gh` is missing or logged out, the run **fails before it
  claims anything** — a harness that quietly stopped publishing would look the
  same as one with nothing to publish.
- Permission to merge the pull request. The target branch itself is never
  pushed, so a branch protected against direct pushes — requiring a pull
  request, a build check, or a review — is merged into normally, provided the
  account `gh` is authenticated as may merge and the request satisfies whatever
  the protection requires. Only the run branch is pushed. If the protection is
  not satisfied, the run stops with the unmet requirement named, the item is
  handed back to you, and your local target is left where the forge has it —
  see [a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).
- **Merge commits allowed** in the repository's settings, since that is the
  method the harness asks for. A repository that permits only squashing or only
  rebasing refuses the merge, and the run reports that refusal — it does not
  fall back to a method that would replace the reviewed commit with a rewritten
  copy your local branch does not have. A protection rule requiring linear
  history has the same effect.

## Losing a race for the target branch

A run promotes its change by fast-forwarding the branch it was written against,
which requires that branch to still be where the run started from. On a target
the forge protects the local branch is not fast-forwarded — the change lands
through its pull request ([a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request))
— but the same requirement holds, and a remote target that moves between the
landing being prepared and the forge being asked to merge is a lost race too,
answered by the same replay and the same budget. It may not
be: another run can promote into the same branch first, and an operator who
commits to it while a run is working moves it just as effectively. The
promotion fails closed in both cases — nothing is force-merged and nothing is
reset — and the run then re-prepares rather than dying on it:

```yaml
execution:
  integration_retries_before_reconciliation: 2
```

Each retry replays the change onto wherever the target went, runs the
configured checks again, and obtains a **fresh independent review**. The earlier
approval is discarded rather than carried over: it described a diff on the base
the change no longer sits on, and an approval that survived a replay would be
authorizing a promotion nobody judged. Nothing is handed back to the developer,
so a retry spends no repair attempt — the change is not what went wrong.

Retries are counted in durable run state before each one begins, so a process
that dies mid-retry resumes against the budget it had rather than a fresh one. A
run that spends the budget stops and records a blocker on the work item saying
plainly that the checks passed and the reviewer approved, and that what needs
looking at is the target branch. Setting the bound to `0` restores the earlier
behavior: the first refused promotion ends the run.

A replay that **conflicts** is never retried and never resolved automatically.
The replay is abandoned, the branch and worktree are left exactly as they were,
both sides of the conflict survive, and the run stops with a blocker on the
item. Which side of a conflict is right is a decision about the product, not a
Git operation.

A replay the harness itself ended — its local Git budget ran out, its context
was cancelled, or it went silent past its liveness bound — is **not** a
conflict, although it leaves the same half-applied state behind. It is told
apart by how the rebase stopped, abandoned the same way so the worktree is back
on its branch before anything is recorded, and reported as an environmental
integration stop of cause `replay-killed` that
`yoyo triage resume` picks up, rather than as a conflict for a person to settle.

A published run's pull request follows the replay: the run branch is replaced on
the remote from exactly the commit the harness published there, so the request
carries the change that would actually be promoted. That is the same
compare-and-swap every other write makes — a remote branch carrying anything
else is refused rather than overwritten — and the refusal stops the run, because
nothing has been promoted yet and there is nothing outstanding to report.
