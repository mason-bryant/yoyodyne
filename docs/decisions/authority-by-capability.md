---
id: authority-by-capability
kind: decision
title: Authority is enforced as capabilities in Go; role names become data only after parity
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:57:48Z
      reason: recorded while promoting the configurable-workflows brief; refines the fixed-roles ruling the harness design records
---

# Authority is enforced as capabilities in Go; role names become data only after parity

**Status:** Accepted. Refines the fixed-roles ruling (recorded via amendment e43b8198) rather than reversing it.

## Context

The fixed-roles ruling held that authority a project could declare is authority a project could widen. It was right, and it also makes every specialist - an observer, an assessor - a harness code change, which does not scale to other people's projects.

**Decision.** Authority semantics stay in trusted Go as capability primitives plus runtime separation policy. Ordinary configuration still cannot touch authority. What becomes configurable — protected, operator-activated, digest-pinned, and only after authorization-by-capability reaches behavioral parity with the five shipped roles — is the *composition* of known primitives into named bundles.

**Rejected.** Roles fixed as Go constants forever: blocks specialists behind code changes and invites bespoke orchestration per specialist, which is the pattern this whole design retires. Authority in ordinary configuration or personas: rejected before, still rejected, and now enforced by invariant. A plugin API for new primitives: rejected — a primitive's semantics must be implemented and reviewed in-tree, because somebody must be accountable for what a capability *means*.

**Consequences.** The parity sequence in the configurable-workflows design is binding: the authority inventory and capability registry land before anything is configurable, authorization call sites convert from role names to capability-and-scope checks before operator-defined bundles load, and the closed role-name type is removed last. Durable records created under the original five names keep compatibility decoding. The harness design's fixed-roles statement gains the refinement rather than being erased.
