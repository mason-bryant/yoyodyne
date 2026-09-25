---
id: program-manager-is-a-shipped-bundle
kind: decision
title: The program manager ships as a Go role bundle, not as the first operator-defined bundle
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: recorded for yoyodyne-ifd.430.6, the program-manager design; the choice between adding a sixth shipped role and opening operator-defined bundles early
---

# The program manager ships as a Go role bundle, not as the first operator-defined bundle

**Decision.** The program manager is a sixth shipped role in the role-capability registry, with its bundle written in Go beside the five, and its instances are agents filling that role. It is not the first protected operator-defined bundle.

**Rejected.** Opening operator-defined bundles for it: authority-by-capability admits those only after the five roles reach behavioral parity through the registry, and parity has not been demonstrated; a first configurable bundle admitted ahead of that would be the escalation path the invariant `configuration-never-grants-authority` exists to refuse, arriving under a reason nobody would remember. A new orchestration for it beside the recurring-task machinery: that is the bespoke-per-specialist pattern the configurable-workflows design retires.

**Consequences.** Adding the role is a harness change touching the closed role-name set, the conversation authority table, the registry, the authority inventory, and the terms register together. The lane scope is a scope on existing tracker-action capabilities rather than a new primitive class. When operator-defined bundles do open, the program manager is the first shipped bundle to be re-expressed as one, which is what makes this decision reversible.
