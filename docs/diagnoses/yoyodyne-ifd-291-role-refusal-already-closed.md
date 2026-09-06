# yoyodyne-ifd.291: the unrecognized role is already refused at load, and this is what proves it

A developer run — `run-9e6d75b43a7d7fda84437a294b4e3304`, 2026-09-06 — was
dispatched for yoyodyne-ifd.291 and found nothing to implement: every requirement
the item states was already true of the tree it was cut from, base commit
`0d392c4`. This is the record of how that was established, written from the
repository rather than from memory, so the next run dispatched for this item can
read it instead of deriving it again.

**The work landed a year's worth of commits ago, in the item that named it.**

| commit | item | what it did |
| --- | --- | --- |
| `43fb723`, 2026-08-19 | `yoyodyne-ifd.97` — *Refuse an unknown role at configuration load, before any work is claimed* | added `domain.Roles()` and `AgentRole.Valid()`, refused an unrecognized role in `Config.Validate`, gave `Backend.SupportsRole` its `!role.Valid()` guard, and documented both |
| `f07a6ba`, 2026-08-27 | `yoyodyne-ifd.32` — provider plugins | moved the backend's answer off a switch on the identifier and onto `backend.Descriptor.Roles`, so which roles a provider serves is declared data rather than a `true` a built-in returns for everything |

## What satisfies each of the item's requirements

| the item asks for | what satisfies it at `0d392c4` | what pins it |
| --- | --- | --- |
| an unrecognized role in any agents block refused at configuration load | `Config.Validate`, `internal/config/config.go:813-818` — the refusal is on the effective configuration, so a role no single layer expressed is caught too | `TestValidateRefusesAnUnknownRole`, `TestInvalidInheritanceFailsClosed/typoed_role_on_an_inherited_agent`, `.../unknown_role_on_a_new_agent` |
| the refusal names the bad value and the recognized set | `agent %q has unknown role %q; roles are %s`, the set read from `domain.Roles()` through `describeRoles` (`internal/config/config.go:1061`) rather than restated | `TestValidateRefusesAnUnknownRole` asserts the agent name, the mistyped value, and the known roles are all in the message |
| the typo shape as the test case | `role: developor` on the shipped developer agent, both stated outright and as a one-key overlay on an inherited agent | `TestValidateRefusesAnUnknownRole/typo`, `TestInvalidInheritanceFailsClosed/typoed_role_on_an_inherited_agent` |
| before any work is claimed or any provider invoked | `LoadResolved` and `DecodeResolved` validate before they return (`internal/config/resolve.go:98`, `:124`), and `buildComponents` loads the configuration before it builds a store, a worktree manager, or a backend at all (`internal/cli/run.go:145`) | `TestValidateRefusesAnUnknownRole` fails the load itself; nothing downstream is reachable with an unknown role in the file |
| `SupportsRole`'s always-true answer honest, or no longer consulted as if it were | both. `domain.Backend.SupportsRole` no longer exists; `backend.Descriptor.SupportsRole` (`internal/backend/registry.go:117`) answers false for a name that is not a role and otherwise consults the descriptor's declared `Roles`. `Config.Validate` does not consult it for an unknown role at all — the `case !roleKnown` arm at `internal/config/config.go:828` keeps the backend from being blamed for a typo on the role line | `TestValidateRefusesAnUnknownRole` asserts the message does **not** say *does not support role*; `TestAPluginThatCouldNeverWorkIsRefused/serving_a_name_that_is_not_a_role` |

The design's fail-before-claiming language is
`docs/designs/v1-harness-design.md:381` — *"Validation runs against the effective
configuration, so a combination no single layer expressed still fails before any
work is claimed."* The inheritance cases above are that sentence's test: the
overlay names only `role:`, and the backend and model it inherits are the
bundle's.

`docs/configuration.md` already states the behaviour in both places it belongs —
the `role` key names the five and says anything else is refused when the
configuration loads, and *What fails closed* lists the typo among the errors
reported before any work is claimed.

## The checks, run at `0d392c4`

`make check` — `fmtcheck`, `test`, `race`, `vet` — passes at this commit: 56
packages green under `go test` and again under `go test -race`, no failures,
exit 0. The focused tests were then run with `-count=1`, all passing:

```
go test -count=1 -run 'TestValidateRefusesAnUnknownRole|TestValidateAcceptsEveryHarnessRole|TestInvalidInheritanceFailsClosed|TestBuiltinAgentsCarryTheirRolesRegisteredCapabilities' ./internal/config/
go test -count=1 -run 'TestAgentRoleValid|TestRolesIsTheWholeSet' ./internal/domain/
go test -count=1 -run 'TestEveryRoleNeedsAPosture|TestAPluginIsRefusedForAnUnsupportedRoleAsABuiltInIs|TestCodexClaimsOnlyThePostureItsSandboxHolds' ./internal/backend/
go test -count=1 -run 'TestRunRefusesARoleItHasNoPostureFor' ./internal/backend/claudecode/
```

## What the mutations establish

A passing test says nothing on its own about whether it would notice the
mechanism going away. Each mutation below removed one part of the mechanism, ran
the tests, and was reverted; the worktree carries none of them.

| what was removed | what failed | what it said |
| --- | --- | --- |
| the `else if !roleKnown` refusal in `Config.Validate` | `TestValidateRefusesAnUnknownRole` (all three cases), `TestInvalidInheritanceFailsClosed` (both role cases) | the mistyped developer loaded as *at least one developer agent is required* and nothing else, and **an unknown role on an agent the project adds loaded with no error at all** — `LoadResolved() error = <nil>` — which is the reported bug reproduced exactly |
| `AgentRole.Valid()` made to return true for every name | the same config cases, plus `TestAgentRoleValid` and `TestAMarkerThatNamesNoRoleSaysSo` (`internal/domain`), `TestABundleForARoleTheHarnessDoesNotHaveIsRefused` (`internal/rolecapability`), `TestAPluginThatCouldNeverWorkIsRefused/serving_a_name_that_is_not_a_role` (`internal/backend`) | the closed set is held in four packages, not just at the configuration check that reads it |
| the `supportedRole` refusal in `claudecode.Run` (`internal/backend/claudecode/backend.go:291`) | `TestRunRefusesARoleItHasNoPostureFor` — *run Claude Code: unexpected process call* | the second line of defence is real: with the load-time refusal in place it is unreachable from a configured run, and it is still what stops an invocation assembled for a role the adapter has no posture for |

The second mutation is the one worth keeping. What the item asked about
`SupportsRole` was whether an always-true answer was being consulted as if it
were honest; the mutation shows the opposite is now load-bearing in four
packages, so a change that quietly reopened the set would fail in all of them
rather than in none.

## Why the item was still open, and what remains

The item is not open because anything is unbuilt. It was admitted from the
architect's amendment-e43b8198 aftermath, relying secondhand on the developer's
account on yoyodyne-ifd.95, and its implementation pinpoint —
`domain.Backend.SupportsRole` at `internal/domain/types.go:54` returning true for
every role on claude-code, with the only role check at `internal/config/config.go:376`
— describes a tree from before `43fb723`. `git log -S "func (b Backend) SupportsRole"`
returns two commits: the bootstrap that introduced the method, and `f07a6ba`,
which deleted it. Neither line number names what the item says it names today.

So what remains is not development. It is closing the item.

One thing is worth admitting to the backlog on the strength of this, and is named
here rather than filed, because a developer run does not admit work: an admission
check that re-reads a secondhand implementation pinpoint against the tree before
the item is queued. This is the second item in a fortnight
(`docs/diagnoses/yoyodyne-ifd-295-detection-already-landed.md` is the other) whose
whole cost was a developer run establishing that the work was already there.
