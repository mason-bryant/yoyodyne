---
id: one-promotion-per-target-branch
title: At most one promotion per target branch, taken by the harness
status: active
established_by:
    - yoyodyne-ifd.48
revisions:
    - action: created
      by: architect
      at: 2026-08-18T13:56:16.917375Z
      reason: Settled with the operator on 2026-08-18 as the concurrency invariant when integration contention was turned from a race into an ordered queue.
---

## Must hold

At most one promotion per target branch happens at a time, enforced by a lease in the runstate store that the promoting run's own harness acquires before it promotes and releases once the promotion settles. No agent acquires, releases, or otherwise touches that lease, and no agent performs a promotion.

## Why

Development is parallel and integration is serial. A promotion reads where the target branch is and then moves it, so two of them interleaved is a race every concurrent run can lose rather than two promotions. Putting the serialization in the store rather than in agent behaviour is what makes it hold across processes and survive a crash: the lease is an advisory file lock the operating system drops when its holder dies, so a killed promotion leaves no stale lock and reconciliation settles the half-finished work behind it. Keeping agents away from it is the same boundary that keeps them away from the merge — the roles that authorize a promotion must not be able to perform one.
