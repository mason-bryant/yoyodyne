---
id: selected-work-passes-intake-and-records-why
title: Work the harness selects itself passes the intake hold and records why
status: active
established_by:
    - yoyodyne-ifd.26
revisions:
    - action: created
      by: architect
      at: 2026-08-19T11:19:18.956497Z
      reason: 'Recorded by the architect (conversation chat-11558d32) before ifd.3 is pulled: an invariant recorded after the scheduler runs constrains nothing, and delivery into the developer''s context is the mechanism''s whole point. Scope deliberately unset — a scope that misses the file where claiming happens fails silently.'
---

## Must hold

No process claims a work item the operator did not name without first reading the product's intake hold and finding it clear, and every such claim records in the run's durable state the reason that item was selected. An item the operator named is exempt from the hold; naming it is the operator deciding it is the exception.

## Why

Holding intake is the operator's narrow control over autonomous work — stop choosing, let what is running finish — and it is worth nothing if the thing that chooses does not consult it. The selection reason is the other half of the same guarantee: work that runs without a visible reason is what the operator most needs to see, and it looks exactly like work happening behind their back. Both bind code that will never mention this constraint — the ifd.3 scheduler first among it.
