---
id: artifact-contract
kind: specification
title: "The artifact contract: specification shape and identity"
supports:
    - v1-harness-design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: approved amendment c84e23a5 from yoyodyne-ifd.87 - the contract's normative home moves out of docs/product/goals/README.md, a directory index the harness skips by design, into a governed architect-owned specification, per the architect's ifd.87 decision
    - action: amended
      by: architect
      at: 2026-09-05T20:10:00Z
      reason: approved amendment 330a8eb3 from yoyodyne-ifd.280 - the 100.2 ruling recorded where developer runs can read it, a How-a-write-reaches-disk section carrying the settled publish shape; three spent runs measured the cost of it living in tracker notes
---

# The artifact contract: specification shape and identity

This is the normative statement of what the harness checks of governed documents. It was previously taught only in a goals-directory index, which is ungoverned by design — indexes link and describe, and normative prose lives in governed artifacts. The configuration guide describes this contract for operators; this document defines it.

## Specification shape

A specification opens with an introduction saying what the thing is and why it exists, then states its goals under a heading whose **whole text** is `Goals`, at any level — a title merely opening with the word is a title. Each goal is one top-level list entry; its statement is that entry's opening paragraph rejoined onto one line, ending at a blank line, an unindented line, a nested entry, or the emphasized `*Supports: …*` trailer — recognized by the emphasis it opens with, written directly under the entry, indented with it, no blank line between. Content after the statement describes the goal; a heading below the `Goals` heading divides goals; the section ends at the next heading at the same level or above, or at **any** heading stating what the product will not do, wherever nested. A non-goals document states its content under a `Non-goals` heading and states no goals; index and non-goals documents are not malformed for lacking goals, and a document that should state goals and does not is still reported.

## Identity

The file name is the id — lower-case letters, digits, hyphens — and a frontmatter id disagreeing with it is refused. Kinds: `brief`, `goals`, `non-goals`, `design`, `specification`, `decision`. Status: `draft`, `active`, `superseded`, `retired`, agreeing with the revision log, which is append-only and records at least the creation, under the role that owns the kind. Approvals are append-only and name the revision they were given for. Refused, never guessed at: no usable frontmatter, unknown fields, an id claimed by two files (both refused, each naming the other). Ungoverned by design: a `README.md` in any artifact home, and everything in the invariants directory, which carries its own scheme.

## How a write reaches disk

A typed artifact write — a document created or revised from a conversation and approved by the operator — stops at the operator's working tree. Authorize records the approval in the document's frontmatter against the revision it produced; publication is the operator's own commit, under their own identity. Until they commit, the changed document is an uncommitted change runs refuse to start over, deliberately: the artifact homes are what runs read as context, and a run based on half-landed intent is worse than a run that waits. The surface that performed the write says so at the moment of writing.

