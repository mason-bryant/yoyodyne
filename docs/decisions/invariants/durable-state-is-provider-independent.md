---
id: durable-state-is-provider-independent
title: Durable state never depends on a provider session
status: active
established_by:
    - yoyodyne-ifd.138
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:40:02.079261Z
      reason: Recorded while promoting the multi-model execution and account routing brief under the operator's 2026-08-22 mandate.
---

## Must hold

No durable record - work, conversation, artifact, review, or run state - has a provider-native session as its only copy. A provider session may accelerate resumption; its loss never loses work, and every provider invocation is recorded pinned to backend, model, account alias, and configuration revision, so attribution and reconstruction survive the session.

## Why

Provider sessions are execution details the harness does not control: they expire, change shape, and belong to someone else's software. Work that lives only there is work one provider change deletes. The rule binds every future feature tempted to treat a resumable session as storage - which is exactly the code that will never cite it.
