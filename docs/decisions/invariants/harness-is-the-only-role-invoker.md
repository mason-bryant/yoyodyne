---
id: harness-is-the-only-role-invoker
title: The harness is the only thing that invokes a role or execution agent
status: active
established_by:
    - yoyodyne-ifd.138
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:33:43.675582Z
      reason: Recorded while promoting the management-and-supervision brief under the operator's 2026-08-22 mandate; the one of the contract's seven invariants that binds code which will never mention it.
---

## Must hold

Only the harness invokes, resumes, or wakes a role or execution agent. A role never spawns, invokes, or schedules another role; inter-role communication is persisted, typed requests that the harness delivers by invoking the target under its own lease, gates, and budgets. No coordination feature, client, or supervisor introduces a second invoker.

## Why

Every gate the harness holds - authority tables, tool posture, the intake hold, the spending pause, budgets, independence evidence for review - is enforced at the point of invocation. A role that can invoke another role routes around all of it at once, which is why this rule was ratified by the operator and why it must bind coordination code that will never mention it. Until now it lived in a machine-local note outside the repository; this is its governed home.
