---
id: repository-writes-are-physically-confined
title: Harness-owned writes are physically confined to their declared root
status: active
established_by:
    - yoyodyne-ifd.136
revisions:
    - action: created
      by: architect
      at: 2026-08-23T04:09:56.923059Z
      reason: Recorded while promoting the repository-confined-writes brief into governed designs under the operator's 2026-08-22 mandate; the one constraint of the brief's five that binds work which never mentions it.
---

## Must hold

Every harness-owned filesystem write passes through the shared safe-write primitive, declaring the root it is confined to, and no write mutates anything that physically resolves outside that root - through symlink traversal, dangling symlinks, path replacement, or a check-to-use race. No caller opts out, and no code path outside the approved low-level package performs a direct repository-scoped write.

## Why

Lexical path checks were proven insufficient by confirmed escapes in the artifact, invariant, and initialization writers: symlinks are legal repository content, so a path string never proves where a write lands. Containment held per-writer is containment each new writer can silently lack; one primitive with no opt-out is a boundary a reviewer can find violations of by looking for writes that bypass it.
