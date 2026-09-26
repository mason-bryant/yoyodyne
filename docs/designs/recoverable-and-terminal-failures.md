---
id: recoverable-and-terminal-failures
kind: design
title: "Recoverable and terminal failures: the boundary per failure class, and what a retry may never do"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T09:00:00Z
      reason: yoyodyne-ifd.265 - the operator's 2026-09-03 rule that the harness never fails outright on anything that can recover, drawn as a taxonomy per boundary; the provider boundary's window narrowed to thirty minutes, lease-held waits bounded to the promotion queue's fifteen minutes, and what exhausting a window produces stated
---

# Recoverable and terminal failures: the boundary per failure class, and what a retry may never do

## What this is for

The operator's standing rule of 2026-09-03 is that the harness never fails outright on anything that can recover, waiting Fibonacci seconds capped at half an hour and asking again. The rule is safe only with its boundary drawn: retrying a judgment on the work is as wrong as terminating on a reset connection, and a retry that could double-promote, double-spend, or hold a shared lease through an outage is worse than the failure it replaced. This design draws that boundary, per place the harness meets somebody else's system, and serves the autonomy goal: a run should end for a reason about the work, and for nothing the next attempt would have survived.

## Three classes, one question

Every failure at a boundary is one of three things, decided by one question: **would the identical request, made again later, get a different answer for no reason of ours?**

- **Recoverable** — yes: a connection reset, a transport drop, a store too busy to answer, a server-side error that is not an answer about the request. It is waited out on the recovery series and asked again, inside the boundary's window, with every wait recorded before it is taken.
- **An answer** — no: a verdict, a failing check, a refused protected path, a conflict, an authentication refusal, a protection rule unmet, a 4xx of any kind, a merge method forbidden. It is handed to the party whose answer it is, at once, and never retried.
- **Ambiguous** — the request may have taken effect and nothing said so: a push or a merge that timed out after it was sent. It is never retried blind. The boundary re-reads the state it would have changed and either adopts what it finds or retries a request that is idempotent by construction. Every mutating request the harness makes to a remote is compare-and-swap or pinned to a commit, which is what makes that safe.

A failure whose class the harness does not recognize is treated as an answer and reported, never retried: the recoverable set is small and enumerated per boundary below, and anything outside it keeps the behavior it had. Widening a set is a change to this design.

## The boundaries

**Provider invocation.** Recoverable: a connection closed mid-response, a transport error, a provider `api_error` with a 5xx status other than 529, and a stream that ended an invocation twice. Each is reissued in the same session under `execution.transient_relaunches_before_blocking`, and past that budget only a plainly dropped connection continues onto the recovery series — inside a **thirty-minute window** for this boundary, not the two hours the forge boundaries get, because every reissue here is a full billable invocation and a provider that keeps dropping a completed reply bills for each one. Waits with their own machinery are not this class and keep it: a usage limit waits to its reset under the usage-limit pauses, an overload waits `execution.server_overload_pause`, and a provider that is not authenticated or cannot be reached is the named wait that spends nothing. Answers: a 4xx other than the authentication refusal, a stream the harness cannot read, and a stop the harness itself made for a stall or an exhausted budget, which owes a continuation rather than a retry.

**The forge and the remote** — the push, the pull request opened or updated, the remote target read, the merge request, the merge confirmation, the remote branch deleted, the catch-up. Recoverable: transport failures, 5xx answers, and a rate limit that names when to ask again. Answers: an authentication refusal, a protection rule unmet, a conflict with the base, a head that is not the integrated commit, a method the repository forbids, and any 4xx. Ambiguous: a push or a merge request that timed out after it was sent, disambiguated by reading the branch or the request's state before anything is asked again; the push is compare-and-swap and the merge is pinned to the commit and asked as of the branch's requirements, so a request that did take effect is found rather than repeated. Each of these boundaries keeps its own two-hour window.

**The tracker.** Recoverable: a `bd` killed under load, a lock that timed out, a listing that did not return. Answers: a refusal by content — an unknown item, a status the tracker will not move, a claim it declines for a reason it states. The read a run makes at a gate boundary parks the run when its window is spent rather than ending it, because a busy store is not a verdict; the read a dispatch makes before any run exists refuses the dispatch and records nothing on a run, because there is none. A conversation's calls share one window per operator message.

**Local Git.** A command the harness's own budget ended is an environmental refusal and is not retried in place: a replay it killed is abandoned and recorded as an integration stop the resume picks up, and a checkout it killed is refused with nothing charged. A conflict is an answer, to a person, with both sides preserved. Neither is a recoverable failure, because the identical command on the same machine at the same load is not a different request.

**The checks and the review.** A failing check and a repair verdict are answers to the developer and are never retried; a check the timeout ended is a failed check, because the budget is the operator's statement about the suite and a second thirty-minute run on the same machine is the load the budget exists to bound. A reviewer invocation the provider killed is the provider boundary, and is reissued without a repair attempt being spent.

## What a retry may never do

- **Double-promote.** Promotion is a compare-and-swap from the recorded base onto the exact commit the harness made, and a merge is pinned to that commit and confirmed by containment. A retried promotion or merge that finds the target moved is a lost race and replays under `execution.integration_retries_before_reconciliation`; it never forces, and it never asks twice for a merge the forge has already made. `one-promotion-per-target-branch` binds every retry as it binds the first attempt.
- **Double-spend.** Every wait is recorded on the run before it is taken, every budget is committed before it is spent, and a process that dies mid-wait comes back to the window it had spent. A retry charges no repair attempt and no review round, because nothing about the change was judged.
- **Trip the brake.** A run stopped by a boundary's window counts toward nothing the failure-storm brake counts: the brake counts verdicts on a change that was present, and a network that stayed down is a verdict on nothing.
- **Hold a shared lease through an outage.** The pre-merge check and the merge request stay together under the target branch's promotion lease, because the check is the evidence the merge stands on and a promotion admitted between them invalidates it. But a recoverable wait taken while holding that lease is bounded to the promotion queue's own bound, **fifteen minutes in total across every lease-held boundary of one promotion**. Past it the holder releases the lease with the boundary's state recorded — a merge asked and unconfirmed, a confirmation not made, a branch not deleted, a catch-up owed — and ends; reconciliation re-asks each of those under the lease again, briefly, as it already does for a queued merge. So an outage longer than fifteen minutes costs the promoting run its confirmation and nothing else, and costs the runs queued behind it nothing at all. That is the bound the notes on this item asked for, and it replaces holding the lease for the sum of five two-hour windows.

## What exhausting a window produces

Never a silent terminal failure. A window that runs out produces exactly what the boundary would have produced on an answer, with the attempts and the time in front of it: an outstanding publication on the item, a blocker naming the provider's own last message, a parked run naming the read it could not make, a refused dispatch naming the store. Each is docketed or held as that class already is, and each reaches the channel at the severity the reach rule gives that class. The escalation names the failure, the boundary, the attempts, and the window, so a person handed it knows the network was retried and for how long rather than reading the last reset as the first.

## What this orders

Two changes to the machinery as shipped, for the product manager to admit: the provider boundary's post-budget window shortened from two hours to thirty minutes, and the lease-held waits bounded to the promotion queue's fifteen minutes with the release-and-record behavior above. Everything else in this design describes the recovery rule as it ships and gives it the taxonomy it was waiting on.
