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
approvals:
    - revision: 0
      by: operator
      at: 2026-08-20T20:12:20.146916Z
      reason: 'Approved by the operator on 2026-08-20 as drafted by the product manager: post-v1 operations outcomes recorded so work arriving ahead of them is designed knowing where it leads.'
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
