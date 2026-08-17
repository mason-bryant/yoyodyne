# Product brief

Yoyodyne turns product intent into shipped software. Someone with a repository
and an idea says what they want, in conversation. Yoyodyne carries that intent
through goals, design, decomposition, implementation, independent review, and
integration, and gives back merged code. Intent in, software out.

It exists because coding agents are good at the part that was never the hard
part. Writing code was rarely the bottleneck; deciding what should be built,
keeping the reason for it traceable to the change that resulted, and making sure
somebody other than the author looked before it landed — that is the work an
agent handed a prompt does not do, and the work a person still ends up doing by
hand around it. Yoyodyne is that surrounding structure, built so the loop can run
without a person driving each turn of it.

The nearest picture is a dark factory: a production line that runs unattended.
The picture is right about the floor and wrong about the building. The floor is
dark — work is designed, built, reviewed, and integrated without a person
watching each step, and no agent can push or merge to reach the outside world.
The office is not. A person sets what the line is for. They state intent, approve
the brief and the goals it becomes, and answer what gets escalated to them.
Autonomy here is the absence of routine per-change gates, not the absence of a
human, and a system that never needed to ask its owner anything would be a
system that had stopped taking direction.

Yoyodyne is for anyone with a repository and an idea: someone building a weekend
project, someone shipping professionally, and teams. It is not a tool for the
people who wrote it. It is finished when someone who has never seen its internals
can point it at their own repository, in their own language, and get software
back.

The bar for that is the whole management hierarchy working — product management,
architecture, development management, implementation, and review as real roles
with real authority between them, not one agent playing every part. A person's
routine surface is intent, goals, and escalations. Everything between those and
merged code belongs to the harness.

What Yoyodyne's first version deliberately does not do is bounded separately in
[the v1 non-goals](goals/v1-non-goals.md); those are decisions about where v1
stops, not limits on what the product is for.

## Goals

- **Intent goes in and merged software comes out.** A person states what they
  want in conversation and receives designed, implemented, independently
  reviewed, integrated code, without directing how the work is done.
- **Every change traces to intent somebody approved.** A reader can follow any
  merged change back through the work, the design, and the goal to the brief, and
  find nothing in the codebase that arrived from nowhere.
- **Intent is only redefined by whoever owns it.** A role that does not own an
  artifact proposes a change to it rather than making one, and that boundary is
  structural rather than a matter of an agent's good behavior.
- **Nothing lands unreviewed by someone other than its author.**
- **The human's attention goes only where it is needed.** Intent, goals, and
  escalations reach the person; routine per-change approval does not.
- **The operator can see what the system does on their behalf.** Work that runs
  without being individually approved is visible while it runs and after it
  lands, and what it costs is reported rather than discovered.
- **It works on other people's projects.** Any language, any build system, any
  test framework — Yoyodyne runs what a project declares rather than
  understanding its toolchain, and general adoption is the aim rather than
  self-hosting.
- **Safety invariants hold whatever the configuration says.** Roles, policies,
  and providers are the operator's to change; the boundaries that keep agents
  from reaching outside the harness are not optional.
