---
id: repository-confined-writes
kind: design
title: "Repository-confined writes: physical containment for every owned writer"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T04:09:30Z
      reason: promoted from the operator's V1 scope-reconciliation brief on repository-confined writes, under the 2026-08-22 mandate; deviations from the brief recorded in the promotion decision
---

# Repository-confined writes: physical containment for every owned writer

## What this is for

The August 21 security analysis confirmed symlink-escape behavior in the artifact, invariant, and initialization writers: lexical path containment — prefix checks over cleaned strings — is not containment when any existing path component can be a symlink, and symlinks are legal repository content. A repository-scoped operation could create or mutate content outside the repository through symlink traversal, path replacement, or missing-target ambiguity. This design establishes the boundary that closes that class whole, serving the goal that safety invariants hold whatever the configuration says: a boundary that keeps writes inside the repository is not one a project's content should be able to opt it out of.

## The contract

One shared safe-write primitive — a single narrowly cohesive package — through which every harness-owned write to a declared root passes. A root is the boundary a writer declares: the repository, a harness-managed worktree, or the state directory. The contract's requirements, each of which the conformance suite proves rather than assumes:

- **Containment is physical, proven at mutation time.** The primitive resolves the actual filesystem topology and refuses a target any existing component of which resolves outside the root. String prefixes and cleaned paths prove nothing here and are not accepted as evidence.
- **Check and use are not separable.** Validation and mutation must not be separated by an unbounded opportunity for the topology to change. The mechanism is descriptor-relative traversal — the walk that validates is the walk that writes, component by component, refusing symlinks that leave the root as it descends. A platform where that is impractical may use another mechanism only if this design is first revised to explain how it closes the time-of-check/time-of-use and symlink gaps; no writer decides that locally.
- **A missing descendant is accepted only under a contained ancestor.** Creation is permitted when the nearest existing ancestor physically resolves inside the root, and proceeds descriptor-relative from that ancestor so the chain being created cannot be redirected mid-creation.
- **A dangling symlink is not an absent file.** Writing through one creates the target wherever the link points, so it is refused, not treated as a fresh path.
- **No caller can opt out.** The package exposes no unconfined write. A writer whose legitimate target is another root declares that root; there is no flag, parameter, or fallback that waives containment for convenience.
- **Failure is explicit, attributable, and clean.** A refused operation names the operation and the reason, and leaves both the external target and the root's state unchanged — no partial write survives outside the boundary.

## The conformance suite

One shared topology matrix, exercised per migrated writer rather than once in the abstract, safe to run in disposable temporary directories: a normal path; an existing symlinked file; a symlinked directory component; a dangling symlink; missing descendants under a safe ancestor; nested traversal; and attempted replacement or race behavior where the platform permits a deterministic test. Each gated writer produces the same safe result, or the same class of error, for each row. The minimal reproductions of the confirmed escapes are preserved as regression tests, distinct from the matrix, so the specific defects that forced this design cannot silently return.

## The gate

The implementation gate on other V1 code lifts when, and only when: the shared contract is implemented; the artifact, invariant, and initialization writers use it; the known escape cases have regression coverage; and the topology matrix passes for each of those writers. The gate is deliberately narrow — it restores delivery while closing the confirmed defects completely — and it does not extend: broader migration proceeds after it lifts, not inside it. Lifting the gate is a release of the code-implementation hold, not a claim that filesystem security work is complete. An independent reviewer verifies physical containment and the adversarial matrix before the gate lifts.

## Deferred, deliberately

- Inventory and migration of every remaining harness-owned writer to the primitive.
- A structural check that refuses new direct write patterns outside the approved low-level package, so the boundary is enforced against future code rather than against the writers someone remembered.
- Git worktree, ref, branch-publication, and remote-boundary behavior, covered directly: remote Git mutation is not assumed safe because ordinary file writers migrated.
- The post-v1 risk-triggered security-review stage, connected to management readiness rather than to a new security-coordinator role.
