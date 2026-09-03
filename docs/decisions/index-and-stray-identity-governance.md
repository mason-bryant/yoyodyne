---
id: index-and-stray-identity-governance
kind: decision
title: Directory indexes are ungoverned, and stray artifact identity is reported rather than refused
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: recorded for yoyodyne-ifd.87.1; decided in conversation at the ifd.87 ruling
---

# Directory indexes are ungoverned, and stray artifact identity is reported rather than refused

**Decision.** Directory indexes are ungoverned by design: a `README.md` links and describes, and anything normative in one belongs in a governed artifact — enforced culturally by this record and mechanically by the artifact-contract specification being the shape's normative home. Artifact frontmatter found within the parent directories of the configured homes but outside every home is reported as stray, never refused; frontmatter on a `README.md` is reported as inert.

**Rejected.** Governing indexes: an index is derived from what it indexes, so a governed one is stale by construction and `yoyo stale` would report it forever. Scanning the whole repository for stray identity: it would walk fixtures and test data and report noise as governance.

**Consequences.** The goals README's checked-shape prose moved to the artifact-contract specification; its goal-quality guidance is the product manager's to rehouse; the bounded stray-identity scan is the reporting the harness owes.
