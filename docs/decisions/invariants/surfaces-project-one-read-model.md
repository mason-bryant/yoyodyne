---
id: surfaces-project-one-read-model
title: Operator surfaces are projections of the one durable service and its read model
status: active
established_by:
    - yoyodyne-ifd.139
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:29:09.242128Z
      reason: Recorded while promoting the shared-observability-and-reduced-dashboard brief under the operator's 2026-08-22 mandate.
    - action: amended
      by: architect
      at: 2026-09-03T16:52:52.54705Z
      reason: yoyodyne-ifd.228, ruled by the architect in chat-11558d325e9a214ebfd00bb4a0012750 turn 27 - the grep-resistance corollary recorded where the invariant lives; the two existing copies (orchestrator.Outcome.Status, orchestrator.Reconciliation.Status) conform via conversion rather than being exempted, and the check is a conformance test
---

## Must hold

Every operator surface - CLI, Slack, dashboard, recovery, and any later supervisor view - derives status, attention, throughput, cost, and capacity from the shared server-side read model, and owns no workflow, conversation, provider, or configuration state of its own. No surface reimplements a domain derivation the model provides, and no user interface directs work except through the harness's existing command and directive paths. No package redeclares a governed vocabulary as a local type: the owning type, a Go type alias of it, or a single named conversion adjacent to the owning type are the only ways a governed vocabulary's values appear outside their owning package, and a conformance test finds violations mechanically.

## Why

Two surfaces computing one number differently is a disagreement only the operator can adjudicate, which converts visibility into work; and a surface that keeps private state is a second control plane growing in the place a projection was promised. The operator has ruled both out. The rule binds every future surface - which is exactly the code that will never mention it. A surface-local copy of a governed vocabulary is a private read model in miniature, and it is grep-resistant: two such copies each cost a coverage sweep to rediscover, which is the measured price of leaving this to convention.
