---
id: entanglement-and-merge-affinity
kind: decision
title: Concentration is essential, its monoliths are accidental, and contention gets merge affinity
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-06T17:30:00Z
      reason: operator design question of 2026-09-06 after a week of integration races and renderer collisions; ruled in conversation
---

# Concentration is essential, its monoliths are accidental, and contention gets merge affinity

**Decision.** The concentration points — one read model, one pipeline, one governed vocabulary — are essential and stay. Their file-granularity monoliths are accidental and split along already-governed seams: the orchestrator spine decomposes through the workflows conversion's action registry and nothing else, and rendering disperses into per-section files behind the read model boundary. Contention that survives decomposition is managed by merge affinity: the development manager records predicted footprints, serializes overlapping items with dependency links, parallelizes disjoint ones, and schedules whole-spine sweeps into quiet windows. Affinity becomes mechanical scheduler input only after advisory readiness is proven.

**Rejected.** Splitting the read model or pipeline into per-epic copies: re-creates disagreeing surfaces by construction. An ad-hoc spine decomposition ahead of the action registry: two competing decompositions of one file. Treating integration races as entanglement: they are serialization working as designed, addressed by ordering and by reducing collision frequency, not by package boundaries.

**Consequences.** Spine-touching epics sequence behind the wrap-in-actions milestone where the queue allows; the renderer split is immediate, cheap work the development manager can decompose; and the DM's footprint-and-dependency practice starts now, with its mechanization gated exactly where readiness gating already is.
