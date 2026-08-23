---
id: safe-write-physical-containment
kind: decision
title: One safe-write primitive proving physical containment; lexical checks rejected
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T04:09:30Z
      reason: recorded while promoting the repository-confined-writes brief under the 2026-08-22 mandate; established by yoyodyne-ifd.136
---

# One safe-write primitive proving physical containment; lexical checks rejected

**Status:** Accepted.

## Context

The August 21 security analysis confirmed that the artifact, invariant, and initialization writers could be led outside the repository through symlinks in existing path components, dangling symlinks treated as absent files, and check-to-use races. Symlinks are legal content in any repository Yoyodyne manages, so the topology under a write can never be trusted from its path string.

## Decision

All harness-owned writes to a declared root pass through one shared primitive that proves physical containment by descriptor-relative traversal, where the walk that validates is the walk that writes. The primitive exposes no unconfined write, and no caller can opt out.

## Rejected

- **Lexical containment** — prefix checks over cleaned paths — proven insufficient by the confirmed escapes: a clean string says nothing about what its components resolve to.
- **Resolve-then-write** (`EvalSymlinks` followed by an ordinary write): closes the symlink gap and reopens it as a time-of-check/time-of-use window between the resolution and the write.
- **Per-writer bespoke fixes**: three writers fixed three ways is three containment models to audit, and every future writer choosing a fourth. The confirmed defects were one class; the fix is one boundary.

## Consequences

- The repository-confined-writes design specifies the contract; the invariant `repository-writes-are-physically-confined` binds all future writers to it.
- A writer with a legitimate target outside the repository declares its root — worktree, state directory — rather than bypassing the primitive.
- The primitive is platform-sensitive; an alternative mechanism on any platform requires a revision to the design explaining how it closes the same gaps.
