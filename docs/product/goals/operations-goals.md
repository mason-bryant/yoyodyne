---
id: operations-goals
kind: goals
title: Operations goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-20T20:00:52Z
      reason: post-v1 operations intent, drafted by the product manager and approved by the operator as drafted on 2026-08-20; recorded now so work that arrives ahead of it is designed knowing where it leads
    - action: amended
      by: product-manager
      at: 2026-09-19T14:40:00Z
      reason: 'the spend-follows-the-work goal, drafted for the operator''s 2026-09-19 direction that model spend follows the work rather than the role, so cost findings can be admitted against a goal rather than raised as concerns; approved by the operator the same day'
approvals:
    - revision: 0
      by: operator
      at: 2026-08-20T20:12:20.146916Z
      reason: 'Approved by the operator on 2026-08-20 as drafted by the product manager: post-v1 operations outcomes recorded so work arriving ahead of them is designed knowing where it leads.'
    - revision: 1
      by: operator
      at: 2026-09-19T14:40:00Z
      reason: 'Approved by the operator on 2026-09-19 as drafted by the product manager: spend follows the work.'
---

# Operations goals

These are post-v1 outcomes: Yoyodyne operating the software it builds. They
support the brief's goal that the system can operate what it ships, and none of
them gates v1 or its releases. Recorded now so work that arrives ahead of them
is designed knowing where it leads.

## Goals

- An operations role runs monitoring, uptime watching, and reboot-class recovery as routine work within capability ceilings: observation freely, runbook actions within recorded bounds, world-mutating actions only through an approval gate.
  *Supports: the system can operate what it ships.*
- A deploy happens only with the operator's explicit approval, recorded like any approval.
  *Supports: the system can operate what it ships.*
- [spend-follows-the-work] Spend follows the work: the model, the context, and the number of provider calls a task costs are chosen for that task rather than for the role that does it, and running cost is visible and classified so the operator can see what each kind of work costs and decide what it should.
  *Supports: the operator can see what the system does on their behalf.*
