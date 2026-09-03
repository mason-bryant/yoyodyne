---
id: developer-verifies-before-submitting
title: Developers probe execution at minute zero, and evidence what the checks would judge
status: active
established_by:
    - yoyodyne-ifd.183
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:46:12.623529Z
      reason: The architect's wording of the operator's principle from the sandbox-blindness incident, with his minor-change leeway; transcribed from her 2026-08-31 rulings batch.
---

## Must hold

Every developer run begins with a minute-zero execution probe: before any edit, the developer executes the declared checks' entry point in its worktree and records the result, with no exemption for any kind of work. A submission whose diff touches files the declared checks exercise must carry submission evidence - the developer's own record of executing the relevant checks against its change before handing it to review. A diff touching only files no declared check exercises may submit on the probe alone.

## Why

A reviewer judges evidence, and a developer that never executed anything submits claims. The probe is universal because it is cheap and catches the broken-sandbox case - a toolchain that cannot run at all - at minute zero instead of at review, which is where it has historically been discovered. Submission evidence is scoped to what the checks exercise because demanding full-suite evidence for a diff the suite never touches prices honesty out and teaches padding, not verification.
