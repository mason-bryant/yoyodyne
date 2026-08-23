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
---

## Must hold

Every operator surface - CLI, Slack, dashboard, recovery, and any later supervisor view - derives status, attention, throughput, cost, and capacity from the shared server-side read model, and owns no workflow, conversation, provider, or configuration state of its own. No surface reimplements a domain derivation the model provides, and no user interface directs work except through the harness's existing command and directive paths.

## Why

Two surfaces computing one number differently is a disagreement only the operator can adjudicate, which converts visibility into work; and a surface that keeps private state is a second control plane growing in the place a projection was promised. The operator has ruled both out. The rule binds every future surface - which is exactly the code that will never mention it.
