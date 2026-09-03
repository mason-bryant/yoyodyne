---
id: integration-requires-revision-bound-evidence
title: Integration requires typed evidence bound to the exact candidate revision
status: active
established_by:
    - yoyodyne-ifd.210
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:58:12Z
      reason: extracted from the configurable-workflows promotion - the existing promotion gate restated so it survives configurable topology, where a definition can route anywhere but an action must still refuse without proof
---

## Must hold

No action integrates or publishes a candidate without verifying trusted, typed evidence that every required gate - protected-path acceptance, configured checks, and independent review - approved the exact candidate revision being integrated. That evidence is minted only by the registered actions that performed those operations, and must not have been invalidated by any later change to the candidate, the check set, or the target branch. Graph topology, step naming, and reachability are never proof.

## Why

Once topology is project-owned data, a definition can route directly to integration, and validation may miss the path a project writes next year. The gate must therefore live in the action rather than in the graph: an integration reached by any route refuses for want of evidence, whatever the definition says. This binds every workflow nobody has written yet, which is the code that will never mention it.
