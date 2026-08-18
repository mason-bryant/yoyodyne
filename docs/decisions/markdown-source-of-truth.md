---
id: markdown-source-of-truth
kind: decision
title: Repository Markdown is the human-readable source of truth
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the record itself is unchanged
---

# Repository Markdown is the human-readable source of truth

**Status:** Accepted. Recorded by the operator, ratifiable by the architect when
the role runs. This decision was made and acted on during the v1 design; it was
stated in [the v1 goals](../product/goals/v1-goals.md) until it was moved here,
because it is a decision about how Yoyodyne is built rather than an outcome the
product is trying to reach.

## Context

The traceable chain from brief to merged change is only useful if a person can
read it. It has to be reviewable by the same means as the code it governs,
diffable, versioned alongside the changes it justifies, and legible without
running Yoyodyne at all.

## Decision

The brief, goals, designs, specifications, decision records, and invariants are
Markdown files in the repository. They are the source of truth for artifact
content. Any machine-readable metadata those documents carry travels with them in
the repository rather than in a separate store.

## Consequences

- Artifact content is versioned and reviewed through the same Git history and the
  same pull requests as code.
- Ownership of a document is an authorization boundary rather than a convention:
  a role that does not own a document proposes a change to it instead of writing
  one.
- A document's structure can be checked. That checking must distinguish documents
  that are expected to state goals from index and non-goals documents; see
  yoyodyne-ifd.43.
- Yoyodyne remains readable and auditable by someone who has never run it, which
  is what the product's traceability goal ultimately rests on.
```

---
