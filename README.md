# Yoyodyne

Yoyodyne is a local, single-operator harness that runs bounded Beads work items with AI coding agents. The current bootstrap supports one Claude Code developer in an isolated Git worktree, followed by configured checks and durable outcome recording.

## Quick start

Prerequisites:

- Go 1.24 or newer
- Git and Make
- Beads (`bd`) initialized in the repository
- Claude Code installed and authenticated

From an initialized Yoyodyne checkout, verify the tools, run all checks, and build the CLI:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyo config validate
```

A repository that has no `.yoyodyne/config.yaml` yet gets one from `./bin/yoyo init`, which writes a complete configuration and copies the agent personas into `.yoyodyne/personas/`. Fill in its `checks` before running work.

Review `.yoyodyne/config.yaml`, commit any intended product changes, and choose an open, unblocked work item:

```sh
git status --short
bd ready
```

Run Yoyodyne from a terminal that can access your Claude Code login:

```sh
work_item_id="replace-with-a-ready-beads-id"
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit, change summary, checks, and agent summary.

What happens next depends on `approvals.integration`. This repository sets it to `automatic`, so a run that passes its checks and is approved by the independent reviewer is committed, fast-forwarded into the target branch, closed in Beads, and its worktree and branch removed — the JSON reports the integrated commit and what was cleaned up. A freshly generated configuration says `human` instead, so a new project preserves the worktree for external integration until it opts in. Either way the harness refuses `automatic` unless deterministic checks and a reviewer agent both exist.

A reviewer verdict of `repair` returns the findings to the same developer, up to `execution.repair_attempts_before_replan` attempts, before the run gives up and records a blocker.

Runs are local until a project sets `approvals.publishing` to `automatic`. With publishing on, the developer phase is what pushes: when a developer attempt finishes, the harness commits it, pushes the run branch to `execution.remote`, and opens a pull request against the target branch — and each repair attempt updates that same request. The approving reviewer verdict is what merges it: the harness asks the forge to merge the pull request, subject to exactly the checks, independence evidence, and fast-forward rule that gate integration, plus a fresh check that the remote target has not moved. The harness makes every push and every merge request itself and routes neither through an agent: no role is given a credential, a tool, or a request for either, and the reviewer — the role whose verdict authorizes the merge — runs with no tools at all, so it cannot perform one. A developer does have a shell in its worktree and runs under your account, so "no agent pushes" describes what the harness does rather than a boundary it enforces; the [design document](docs/v1-harness-design.md#what-is-enforced-and-what-is-not) says which half is which. The local target branch stays authoritative: the harness fast-forwards it as it always has, and the forge merges the pull request carrying exactly that commit under a merge commit — the one method that puts the reviewed commit itself on the base, where a squash or a rebase would substitute a rewritten copy. So the remote target is your local branch plus one forge merge commit per published run, identical in content; the harness checks that relationship on both sides of the merge and never rewrites the local branch to match. Your target branch itself is never pushed, so a branch protected against direct pushes is merged into normally; a forge that refuses reports which requirement was unmet, and a merge that did not carry the promotion is reported rather than reconciled. The merge is asked for as of when your branch protection is satisfied rather than as of now, so required checks that are still running are waited for by the forge rather than refused seconds after the reviewer approved. Waiting that way needs "Allow auto-merge" enabled on the repository; when it is off and nothing is holding the pull request back the harness just merges, so only a repository that has something to wait for and no way to wait for it is reported as unpublishable, naming the setting. Administrator override is never used to get past a protection rule. A run whose merge is queued that way reports the pull request as queued and finishes; [`yoyo reconcile`](#recovering-interrupted-runs) settles it once the forge has merged, or reports an outstanding publication if the forge dropped the queued merge. A repository with no configured remote publishes nothing and behaves exactly as it did before.

Merging belongs to `approvals.integration`, so the two settings compose rather than imply one another. Publishing with `integration: human` opens the pull request and stops: nothing is merged, the run branch survives on the remote, and the worktree is preserved for you — which is what a `human` integration policy means. See the [configuration guide](docs/configuration.md#publishing-through-pull-requests).

When the provider reports that a usage limit is exhausted, the run pauses instead of failing — for either provider invocation a run makes, the developer attempt or the review. The reset time the provider named is recorded in durable run state before any waiting starts, and nothing is cleaned up: the worktree, the branch, the claimed Beads item, and the developer session are all kept, so the reissued attempt continues the same change rather than starting it over. A review that was declined is simply asked for again once the limit resets, without redeveloping the change or spending a repair attempt. A wait shorter than `execution.usage_limit_in_process_pause` is waited out and the run finishes on its own; a longer one exits with the run still in flight, and running `yoyo run` on the same item after the reset time continues it. Either way nothing retries before the deadline, and a restart during the wait honors the recorded deadline rather than asking the provider again. `execution.usage_limit_max_pause` bounds what one run may spend waiting in total rather than each wait separately, so a provider that keeps refusing cannot walk a run past it. A limit reported without a reset time is unknown rather than unwaitable: the monthly overage allowance reports this way while the ordinary rolling window keeps resetting on its usual schedule, so the run waits `execution.usage_limit_unknown_reset_pause` — thirty minutes by default — and asks again. That wait spends the same budget as any other, so a provider that keeps refusing walks into the maximum rather than polling forever. A limit the harness genuinely cannot wait for — a reset that is not in the future, or one that no longer fits the run's remaining budget — stops the run and records a blocker rather than guessing a wait. Transient throttling is not this: the provider CLI retries that itself, and the harness does not duplicate it.

A provider invocation is bounded by two separate questions, because one deadline cannot answer both. Whether it is stuck is answered by activity: the harness already stamps every event it parses, so a gap of five minutes with no event at all means nothing is happening, and the invocation is stopped as stalled. Whether it is worth continuing is answered by a total budget of four hours, because an agent can stay live and unproductive — retrying, looping, thrashing — and no liveness signal will ever catch that. An agent that emitted a tool result seconds ago is demonstrably working, so elapsed time alone never stops it. Both stops leave the run in flight rather than failing it, exactly as a usage-limit pause does: the worktree, the branch, the claimed Beads item, and the developer session are all preserved, and running `yoyo run` on the same item continues that run — the developer resumes its session, and a stopped review is simply asked for again without redeveloping the change or spending a repair attempt. The reason is reported as what it was, a stall or an exhausted budget, and neither is ever described as the agent having reported a failure, because it reported nothing. Only a stop with nothing to continue from — no session, no worktree — ends the run, and it still says the harness stopped the provider. Short Git commands keep their flat deadlines, which is the right bound for a command whose duration is known.

Documentation counts as part of a work item rather than as follow-up: the developer contract makes updating the documents that describe changed behavior part of the assigned work, and the reviewer reports a change that leaves a document asserting something the change has made false. That reconciliation is diff-scoped, and the limit is worth stating plainly — the reviewer is given one change, not the repository, so it catches a contradiction with documentation it can see and misses a claim invalidated in a file the change never touches. Nothing in the harness yet compares the accumulated documentation against reality.

## Architectural invariants

Some constraints outlive the work item that established them: a contract one change created that later changes must not break. Those live under `product.invariants` — `docs/decisions/invariants` by default — as one Markdown file per constraint, named by its id, carrying what must hold, why, what established it, and a recorded revision history. They belong to the architect, and it is worth being precise about which half of that is enforced and which is not. Recording, amending, and retiring one goes through a single code path that refuses every role but the architect, so `yoyo invariant` and any future architect agent are bound by it and no other role has an authorized way to write one. A developer, though, has a shell in its worktree, exactly as it does for the [pushes and merges the harness never routes through an agent](docs/v1-harness-design.md#what-is-enforced-and-what-is-not): what stands in the way of it editing an invariant is its contract, which forbids it and tells it to propose the amendment instead, and the reviewer, which is told that a change creating, amending, retiring, or editing one is a finding. Treat "only the architect changes an invariant" as the authorized path plus a caught one rather than as something the harness makes impossible.

They exist because a change whose own work is correct can still break something outside its scope, and they are only worth having if they reach the people doing the work. The harness selects the ones relevant to a work item and delivers them into the developer's context and the reviewer's evidence, so nothing depends on whoever wrote the bead having remembered the constraint. Selection is what keeps that affordable: an invariant with no declared scope is repository-wide and reaches every item, and a scoped one is delivered when the evidence names a path it constrains. The developer's evidence is the work item's own prose; the reviewer's adds the change itself, so an invariant scoped to code the item never mentioned still reaches the gate that judges the change that touched it. The reviewer is told that a change violating a delivered invariant draws a finding naming it, and that editing one is a finding of its own. The limits are stated where they apply: the delivered set is never presented as the whole set, an invariant the harness could not read is named as a gap rather than dropped silently, and what was delivered is recorded on the work item so an operator can see afterwards which constraints applied.

The architect agent does not execute yet, so the lifecycle is reachable from the command line, acting with the architect's authority and recording that it did:

```sh
./bin/yoyo invariant list
./bin/yoyo invariant create \
  --title "One process at a time acts on an in-flight work item" \
  --statement "Every entry into an in-flight run takes the run's exclusive lease first." \
  --rationale "The lease is the only thing keeping two developers off one item." \
  --established-by yoyodyne-ifd.2.7 \
  --scope internal/runstate,internal/orchestrator \
  --reason "extracted from the decision that added the reservation" \
  one-writer-per-item
./bin/yoyo invariant show one-writer-per-item
./bin/yoyo invariant retire --reason "the reservation moved into the store" one-writer-per-item
```

Retirement is explicit and recorded rather than a deletion: the file stays, the constraint stops being delivered, and the reason it was lifted is readable by whoever read the invariant last month. A repository with no invariants directory simply has none, and runs are unaffected.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got to. `yoyo reconcile` settles what it left behind:

```sh
./bin/yoyo reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes the run's own remaining step or hands the item to you. A run whose work reached the target branch is closed and its worktree and branch removed, including when the run died before it could record the promotion. A run stopped anywhere earlier becomes a durable blocker naming the branch and worktree that were preserved. A run that finished with its merge queued at the forge is settled here too: reconcile asks the forge, finishes the publication once the merge has landed, and — if the forge dropped the queued merge because something it required went unmet — reports an outstanding publication on the work item instead of merging past the requirement. It never invokes a provider: a lost process handle is not a reason to start a second developer for an item. Repeating it is safe — a settled run is no longer outstanding, and cleanup over artifacts that are already gone does nothing. A run another process still holds is left to that process, and a run `yoyo run` can continue on its own — one inside its repair loop, one paused for a provider usage limit, or one whose provider the harness stopped on time — is left exactly as it is for that command to pick up.

## Talking to the product manager

`yoyo run` and `yoyo reconcile` are administrative and recovery entry points. The intended interface is a conversation with the product manager, and ordinary work no longer needs anything else: you state intent there, approve what it becomes, and run, watch, redirect, and stop the work from inside the same conversation.

```sh
./bin/yoyo chat
./bin/yoyo chat --message "What is missing from the brief?" --json
```

The product manager reads the product specifications — every Markdown file under `product.specifications`, which defaults to `docs/product` — plus the open Beads items, and discusses product intent with you. It owns the queue that serves that intent, and it manages it directly rather than dictating changes for you to type.

That queue is a backlog with an order, and the order is the product manager's. What is admitted to it and what comes before what are product decisions — the same intent it already owns, expressed as what to do next — while decomposition, dependencies, and assignment stay a development manager's, which takes work from the top of that order rather than choosing for itself. A role that disagrees with the ordering proposes a change to it exactly as it would propose a change to a goal; none of them reorders it or admits work to it. The order is written down as Beads priority — 0 first, 4 last — so there is one place it lives rather than a second copy that could disagree with the tracker, and items left at the same priority are in no order anybody decided: the product manager says which comes first by giving it a higher one. Admitting work says where it goes as part of admitting it, because a new item has no identifier until the tracker answers and an ordering left for a later step is an item sitting at the tracker's default in the meantime — including for an item you approved from a proposal, which is admitted at that default until the product manager places it. `/backlog` shows it to you in order, with what is holding each unready item back and what would be pulled next.

Nothing pulls from that order on its own yet, and the difference is worth stating plainly: the harness runs a work item when you ask it to with `/work`, and the development manager that would take from the top of the backlog without being told is not built. What exists today is the ordered queue it will take from, an owner for that order, and your view of both.

Work leaves the backlog in one of two ways, and both are recorded on the item. `close` says the work is done. `retire` says it will not be done, and is the only way admitted work leaves the queue without being done: there is no delete and no third way, so scope you asked for cannot quietly disappear from the queue — it is withdrawn in the open, with a reason, and the item afterwards says it was retired rather than finished.

A specification opens with an introduction saying what the thing is and why it exists, and states the goals that serve it after that introduction. That shape is the contract, and the harness checks it: one that has no goals, no introduction before them, or an empty goals section is named on stderr when the conversation opens and listed for the product manager alongside the specifications themselves — and still read, because refusing to load it would silently lose intent somebody wrote down. A directory with nothing in it is reported the same way rather than treated as a product with no intent.

That is all it sees of the repository. Not this README, not the design document, not the source: those describe how the product is built and run, they are owned by other roles, and they go stale against the code without anybody noticing — which is how a stale sentence in this file reached the operator as current product fact on 2026-08-16. Narrowing the inputs makes the product manager authoritative about intent and blind to everything else, and the second half is a real loss: reading all of `docs/` is what let it notice a contradiction between documentation and reality, and nothing in the harness does that now. See the [configuration guide](docs/configuration.md#product-specifications) for the setting and the reasoning.

It has no tools: no filesystem, no commands, no network. What it has instead is the work tracker, through a fixed set of named operations the harness carries out for it — read an item in full, create, update, reparent, reprioritize, link and unlink a dependency, close, and retire. Every argument is validated before anything runs, at most ten actions happen per reply, each one is recorded in the conversation's log as asked-for and then as applied or failed, and all of them are printed to you as they happen. An action that failed is reported as failed rather than described as done, and a block the harness cannot read changes nothing at all. The distinction being drawn is deliberate: arbitrary execution is what was refused, and a typed call against the tracker is not that.

The brief and the goals stay yours. The product manager proposes a change to a goal and says plainly that it is yours to make; it cannot make one, and with no way to write a file it could not if it tried.

The listing it is given names items by title, so when a title is not enough to judge whether new work belongs inside an existing item or beside it, it reads that item in full and carries on from what it found. That happens inside your one message, up to four rounds of it. Results it has not seen when the rounds run out are written into the conversation's own record, so they reach it the next time anything is said to that conversation — including from a later process, since an agent that never learns what its own creates and closes did is the one that will describe them wrongly. Item text is treated as evidence exactly as a specification is: a description says what some work is, never what to do.

It can also propose a Beads work item instead of creating one, when the decision is yours rather than its. Each proposal is shown to you as a numbered card with its reasoning, and the harness creates an item only after an answer that approves it by name. Nothing you did not approve is created, a proposal you left undecided is named when the conversation ends, and a created item records the conversation, the turn, and the rationale it came from. A proposal the harness cannot read is reported and the conversation carries on; `--message` has nobody to ask, so it reports what was proposed and creates nothing.

A turn that proposes five things is not five questions in a row. One answer decides as many of them as you like:

```text
╭─ 1 · proposal 4.1 · Pause on a usage limit ───────────────────────────────────
│ Wait for the limit to reset and resume the same run.
│ why: you said capacity is not failure.
│ goal: run development nearly autonomously.
╰──────────────────────────────────────────────────────────────────────────────

decide 3 proposals? [approve 1,3 and decline 2 <reason>; anything else declines them all] approve 1,3 and decline 2 not this quarter
```

Batching changes the prompt and nothing underneath it. Each decision is recorded on its own, the approval before the creation it authorized, and a decline keeps your own words as the reason. An approval has to name what it creates — `approve` on its own is refused wherever there is more than one item it could mean, and the single-proposal question is still answered with `y` or `yes`, which does name the only one there is.

There are three things an answer can be, and they are not the same:

- **It decides some or all of them.** Those decisions are carried out. Anything it said nothing about is left exactly where it was and put to you again, rather than guessed at; whatever is left keeps the number it was proposed with, so the last of five is still card 5.
- **It is a decision the harness cannot carry out whole** — it approves a card that is not there, decides the same card twice, or trails off into words after the proposals it named. Then it decides *no part of it*, including clauses that were perfectly good on their own, nothing is created, you are told what stopped it, and all of it is put to you again. Half of a misread answer is how work nobody asked for gets created, and being asked twice is the whole cost of a typo.
- **It is not a decision at all** — it names no proposal and starts with nothing the harness recognises, like `hmm` or `not this quarter`. That declines everything it was asked about and is kept as the reason, exactly as any answer that is not a yes always has been.

The one thing not put to you again is a proposal the tracker refused to create: it stays undecided and is named when the conversation ends, because asking you the same question until the tracker recovers is not asking you anything.

Because a decline keeps your words verbatim — the whole of what you typed about those items, down to the word you turned them down with — its reason runs to the end of the line, so write the approvals first. A decline separates the proposals it names with commas or `and`, and everything after them is your reason even when it starts with a number: `decline 2 3 weeks out` turns down card 2 for being three weeks out rather than turning down cards 2 and 3.

Work reaches the queue only with a goal named against it. Every proposal, and every item the product manager admits itself, says which goal it serves; the harness requires it and writes it onto the item, so what the work is for is recorded rather than asserted once and lost. The naming is what is enforced, not the goal: goals are prose in `docs/product` with no identifiers yet, so the harness can insist that a goal is named and recorded and cannot yet check that the named goal is one you approved. What a proposal is placed against is checked: a parent or dependency the tracker does not hold proposes nothing at all, rather than becoming an approval that fails after you have given it.

Work it will not attach to a goal is not proposed and not quietly dropped either — it stops and asks you, and the three cases stay apart because you answer them differently. Work it can find no goal for is usually a sign the goals are incomplete, so it asks which goal it should serve. Work that would cut against a goal is a conflict it puts to you rather than proposing with a caveat. Work that fits the goals as written and that it judges to be against what the product is for is an opinion it states and can be wrong about, because you can overrule an opinion it voiced and cannot overrule one it kept to itself. Each question waits for your answer before the conversation moves on, what you say reaches it on your next message, and a question you leave unanswered is named when the conversation ends rather than passing for agreement. `--message` has nobody to answer, so it prints the questions and proposes nothing.

On a terminal, the line you are composing has a region of its own at the bottom of the screen and everything the harness writes goes above it. A reply, a proposal, or a run that finishes never lands in the middle of a half-typed sentence: what you have typed stays exactly as it is, and you carry on from where you were. The conversation is written into the terminal's ordinary output rather than an alternate screen, so scrollback, selection and copying, and resizing keep working on it as they would on any other command's output. Editing the line is deliberately small — the arrow keys, home and end, backspace and delete, and Ctrl-U and Ctrl-W — and Ctrl-C still interrupts the way it always did, because the terminal keeps its own signal keys.

While a turn is being answered, the line below the conversation says what it is doing: a spinner, the phase it has reached, and how long you have been waiting. The phases are read off the same event stream the turn is already recording — your message going out, the model thinking, the model writing its reply, the harness carrying out tracker actions it asked for — and a provider that is refusing requests is named as exactly that, with the attempt it is on, because a turn that is slow because the service or your account is declining work is telling you something worth knowing. Nothing arriving for twenty seconds stops the animation: the line then says how long it has been quiet, because a display that keeps moving through a stall looks like progress, and looking like progress is worse than saying nothing. It is drawn above the line you are typing and erased when there is a reply to read, so it is never in your way and never in the scrollback.

The reply itself arrives while it is being written rather than all at once when it is finished. The provider reports the product manager's message before the terminal result the turn is recorded from, so the text already exists before the turn is over and what changed is only when you are shown it. It reads exactly as the finished reply reads — the same opening, the same Markdown, the same questions in the same colour — and it is not written a second time when the turn ends. The blocks the product manager writes for the harness rather than for you are not shown as prose: a proposal, a tracker action, a concern, and a report are each reported in their own way once the turn is over, and the source of one arriving mid-sentence would be the protocol rather than the answer. None of this touches the record: the reply that is recorded, the events, and `--message --json` are byte for byte what they would have been with nobody watching, because the fragments are the same text the harness had already redacted and already written down. A turn whose provider stops before the reply is finished says so on the line after the prose it managed to show, because prose that simply stops reads as a product manager that had nothing more to say.

Between turns that line carries what the conversation has cost: what the last answer was charged and what this session has spent, taken from what the provider itself reported per invocation and worked out no further. It is replaced rather than written into the conversation, so a running total is somewhere you can see it rather than a log of itself, and a provider that reports no cost is left unanswered rather than reported as free. Work in progress covers it while there is any, because what you are waiting on is the more urgent of the two.

A horizontal rule separates your turn from the answer to it, and colour tells apart the things you have to act on rather than read past: a question the product manager asks you is orange, and a proposal awaiting your decision and the harness's own answer to a command each have a colour of their own. A proposal is framed as a card so a batch of them reads as several things rather than one wall of text; the frame is decoration exactly as the rule is, and where decoration is suppressed the same card is its heading with the body indented under it. The states work is in are coloured too, and the same way wherever they appear — running blue, blocked orange, done green, failed red — so `/status` is read down its aligned columns rather than picked out of ragged prose. Colour is an addition to the text and never what carries the meaning — the question still ends in a question mark, the proposal still says what it is proposing, the group still says "blocked (2)" in words — so a transcript with the escapes stripped out loses the decoration and nothing else. `NO_COLOR`, a terminal that reports itself as `dumb`, and output that is not a terminal each suppress all of it together — the colour, the rules, the cards, the reply shown as it forms, the milestones a run reports, the bell and window title, and the cost line — because every one of them writes an escape or depends on there being a moment at which something unprompted can be written, and somebody who asked for an undecorated conversation asked for all of it.

The product manager writes Markdown, and on a terminal you read it as Markdown: headings, list markers, thematic breaks, and bold spans are shown as structure rather than spelled out in punctuation. That is presentation and only presentation. Nothing is added to the reply and nothing is taken out of it — every escape is inserted between characters that were already there — so the same reply stripped of its escapes is the recorded reply byte for byte, and a stream that may not be dressed is shown exactly what was written.

Anywhere else the same conversation is an ordinary stream of text. A pipe, a file, a redirected terminal, and a terminal that reports itself as `dumb` get no cursor control, no colour, and no rules at all: the same lines in the same order a redirected conversation has always had, plus each phase of a turn said once as a line of its own, with nothing in it that a clock decided — there is nothing to animate or erase on a stream, and a transcript whose contents depended on how long the provider took would not be one you could compare against another. For the same reason a stream is shown the reply when it is finished rather than as it forms, and a run it started is not watched at all: a stream has no moment where you are waiting with the screen to yourself, so anything written between two lines that are already buffered would make what the transcript holds depend on timing. None of this reaches the recorded reply, the event stream, or `--json` — it is how the conversation is shown and nothing more, so what is recorded is identical either way.

### Steering the work from the conversation

A line that begins with a slash is a command the harness carries out for you; everything else is said to the product manager:

```text
/status                  what is in flight, claimed, blocked, available, and done
/backlog                 the admitted work in order, and what would be pulled next
/show <id>               one work item in full, as the tracker holds it
/diff [id]               what a run changed, from the run's own record
/reports                 what agents reported without it stopping their work
/refresh                 re-read the repository and tracker into this conversation
/work <beads-id>         run one work item now, while you keep talking
/wait                    wait for the run this conversation started and report it
/stop [reason]           stop that run and settle what it left behind
/redirect <id> <what to do differently>
/help                    the list
/exit                    end the conversation, stopping anything it is running
```

`/backlog` shows the ordering the product manager set, which is the one thing a development manager pulls from: the admitted work that is not finished, in priority order, each unready item saying what is holding it, and the item that would be pulled next named at the end. Like `/status` it is a report rather than an export — the first twenty entries are listed and the rest are counted, so a long backlog says how much of itself you are not looking at, and the item that would be pulled next is named even when it falls outside the listed part. It is assembled from the same tracker `/status` reads rather than stored anywhere, so it cannot drift from the priorities the product manager actually set.

Whether an item can be pulled is the tracker's answer rather than one the harness works out, and that is a deliberate choice about what a listing can be trusted for. A Beads listing carries the dependencies between items, but it records only that a dependency exists: the entry reads exactly the same after the blocking work is closed as it did before. Deciding readiness from that would either name blocked work as the next thing to pull, on any listing that left dependencies out, or hold an item back forever for a blocker that finished months ago. So the harness asks Beads what is ready — the same blocker-aware question `bd ready` answers — and a dependency is named as a wait only when the work it points at is itself still in the backlog. An unready item says which of the three things is holding it: named work it waits for, a blocker recorded on the item, or the tracker simply not offering it. A tracker slice it cannot read, including that readiness answer, fails the whole report instead of returning the half it could: a survey describing part of what is happening is still worth reading, and half a queue answers "what happens next" wrongly rather than incompletely.

`/work` runs exactly what `yoyo run` would run — the same worktree, developer, checks, reviewer, and integration policy — in the background, so the conversation stays a conversation. One run at a time. `/status` reads durable run state and the tracker, so a run another process is executing is as visible as one started here, and a run that is owed a continuation rather than working — one waiting out a provider usage limit, or one whose provider was stopped on time — is named as such rather than reported as progress. On a terminal a finished run reports itself the moment it finishes, above whatever you are typing, and rings the bell and renames the terminal window as it does, because a conversation left in a background tab is exactly where a run finishing goes unnoticed; the window's name is put back when the conversation ends, so it never outlives the work it was announcing. Where the conversation is a redirected stream there is no such moment, so it is reported at the next line, or when you ask for `/status` or `/wait` for it.

A run started here also says the few things it crosses on the way, one line each above whatever you are typing: its checks passing, the reviewer's verdict, the promotion into the target branch, and — where the project publishes — its pull request being queued to merge and being merged. They are read from the run's own durable record rather than from the process executing it, so what you are told is what somebody reading that record afterwards would be told, and each is said once: a crossing is a transition rather than a state, because an event log scrolling past the conversation is what the activity line exists not to be. The product manager is told the same things as harness activity, so the next thing it says about the work is not answering about a run it believes is still developing.

`/stop` cancels the run, records why on the work item, and then settles what the cancelled run left behind exactly as `yoyo reconcile` would: integrated work is finished, and anything else becomes a durable blocker naming the branch and worktree that were preserved. Two cases are exceptions, and both are reported as what they are. A run that does not give up within the stop grace is reported as still in flight rather than described as stopped. A run that reached its own conclusion before the cancellation reached it — integrated under an automatic policy, or finished with its worktree preserved under a `human` one, since a successful run then promotes nothing — is reported as having finished on its own: nothing is recorded on the item and nothing is settled, because nothing was stopped. What separates the two is whether the harness reported a failure, not whether anything was integrated. A run that had paused itself is not one of these — whether it was waiting out a usage limit or had its provider stopped on time — because it is owed a continuation rather than finished, so the stop is recorded against it, and the report says the run is preserved and continues only if you start it again. Ending the conversation stops its run the same way, because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what was asked for: the stop is recorded as a request when you make it, and the run's outcome — what it left behind, or the integration that beat the cancellation — is recorded once it is known.

`/show` prints one work item in full — its status, priority, parent, dependencies, description, design, acceptance criteria, and notes — through the same tracker capability the product manager reads items with. What you see is what the agent discussing it could see, which is the point: the two of you are reading the same item rather than two accounts of it.

`/diff` says what a run changed. It reads the run's own durable record rather than shelling out to git, and that is what makes it survive success: a run is cleaned up once it integrates, its worktree removed and its branch deleted, so anything that answered by diffing a tree would stop having an answer exactly when the work landed. The record keeps the file listing and the diff stat the harness took while the worktree existed, along with the branch, the promotion, and the pull request the work was published through — all still there to point at after the tree is gone. Naming nothing asks about the run this conversation last started — still going, already collected, or started by an earlier process this conversation was resumed from, since the item it ran is written into the conversation's own record rather than kept in whichever process happened to start it. Naming an item asks about the most recent run of that item, whoever started it. A run whose record holds no summary says so rather than printing an empty listing that reads like one.

`/redirect` records your direction in the item's notes, where the developer's context reads it on the next attempt, and stops the run first when the item you are redirecting is the one running. It never changes the item's status: saying what to do differently is not deciding that the work is done or blocked. Start it again with `/work` when you want it retried.

Only you reach any of this. The product manager owns what the queue says and the order it is in; running, stopping, and redirecting the work itself stays yours, so nothing it writes starts or stops anything — a reply that contains `/work` is prose. What it does get is an account of what you had the harness do, carried into its next turn as evidence, so the conversation keeps discussing the product as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the operating system's state directory, so leaving and running `yoyo chat` again resumes the same conversation; `--new` starts a fresh one instead. The record keeps the requested model selector, the model the provider reported serving, the provider session identifier, any action results the product manager has not been told about yet, the work item it last ran, and when its picture of the repository and tracker was gathered and against what commit, and the normalized event stream is stored beside it — including what the operator asked the harness to do, which is recorded in the conversation's own log beside the runs' logs.

### What agents report, and where it reaches you

Until now an agent could only reach you by failing. A spent repair budget becomes a durable blocker, a failed run is reported where you are already looking — and everything an agent noticed while its work *succeeded* survived only as prose in a run summary copied into an item's notes, where nothing surfaces it. Two real examples from a single session reached the operator only because a person happened to be reading: a reviewer's observation that the built-in bundle's declared version had gone inert, and a developer's report that `bd lint` could not run in its sandbox.

Every role can now say such a thing without stopping: the developer, the reviewer, and the product manager each end what they say with one small block, and the harness collects it. `/reports` shows you the pile, newest last, with the twenty most recent listed and the rest counted.

A report is deliberately not a blocker, and nothing about it behaves like one. The run carries on exactly as it would have: an approving verdict that mentions something still approves, a developer that reports something still finishes, and a report the harness cannot read or cannot store costs its run nothing at all — it is named on the outcome instead, because a report nobody kept would otherwise be silence. That is the property worth relying on, since a channel that could cost an agent its run is one agents learn not to use.

Each collected report carries the role and the configured agent that made it, the run or conversation it came from, the work item where there is one, a severity — `critical`, `warning`, or `note` — and the text. That is enough structure to filter the pile later without deciding now how it should be filtered; an agent that judges which of its own observations are worth your attention is a later question, and nothing here does it. The severities are deliberately not the reviewer's `blocker`/`major`/`minor`: a finding decides whether a change is repaired, and a report decides nothing.

Volume is the risk this design has, and the answer to it is in the role contracts rather than in a filter. Every contract says what merits a report — a risk worked around, an assumption that may not hold, a defect or a stale document outside the assigned work, something in the environment that stopped a check being run — and says plainly that most replies should carry none, because a channel full of routine observations is worse than nothing: it looks like coverage. That guidance is in Go, alongside the rest of each contract, so no persona can loosen it.

The pile lives outside the repository under the operating system's state directory, beside the run and conversation records rather than among them. It outlives them: a run is settled and its worktree and branch are removed, and what it reported is still there for you to read.

### How fresh the conversation's picture is, and how to refresh it

The specifications and tracker the product manager reads are gathered once, when a conversation opens, and sent on its first turn only. Every later turn resumes a provider session that already holds them, so re-sending would pay to restate what it was already told. The consequence is worth knowing before it surprises you: **a resumed conversation keeps the snapshot it opened with.** Change a specification, and a conversation started beforehand will still describe the old one, confidently, because that is genuinely the evidence it has.

So the conversation says so itself, on one line, as it opens and as it resumes:

```text
context gathered 2h ago; 14 commits and 3 tracker changes since. /refresh reads what moved into this conversation.
```

Freshness is a comparison rather than a timestamp. The picture records when it was assembled and what commit the repository was on, both durably, so the process that resumes a conversation can say how old it is without having been the one that briefed it. What has moved since is two cheap questions: what `HEAD` holds that the picture did not, and what the tracker wrote into its own interactions log after the picture was taken. Either comparison can fail — an unrecorded commit, a repository that will not answer — and a comparison that could not be made is reported as unknown rather than counted as nothing, because "0 commits" from a broken comparison is the same confident staleness this exists to end. The tracker's log is an export rather than its live state, so the count is a floor on what has moved; a log a tracker has never exported, and one too large to read to the end, are both reported as unknown rather than as unchanged, because a truncated comparison that answered "nothing moved" would be the same false confidence in a smaller place. A one-shot `--message` says the same line on stderr, where it cannot disturb the reply on stdout or the `--json` document.

`/refresh` re-reads the repository and the tracker into the running conversation. It discards nothing: what has been said stays said, and the new picture reaches the product manager on your next message, framed as evidence with an account of what moved, so it reconciles what it believed rather than having it swapped underneath. The transcript says the refresh happened, the conversation's own log records it, and the durable record only says the conversation is working from the new picture once a turn has actually carried it — a refresh nobody was told about never reads as one that landed.

It was never frozen entirely. Every turn carries what you did through the harness since the last reply — the runs you started, stopped, and redirected — so `/work`, `/stop`, and `/redirect` reach a resumed conversation, and reading an item goes to the tracker as it stands rather than to that opening snapshot. What did not reach it before was anything else that changed outside those commands: edits under `docs/product`, and items created or closed by something other than this conversation. That is exactly what `/refresh` is for.

`--new` is now a different tool rather than the answer to staleness. A refreshed conversation and a new one end up equally current; they differ in what they remember, and that difference is the point. Start a new one when the history itself is the problem — an unrelated topic where its memory of the last one is not worth carrying — and refresh when the ground has moved under a discussion worth keeping. `--new` replaces the recorded conversation: there is one per product, so the previous discussion is not kept alongside it.

## Configuring a project

A project owns its configuration outright. `yoyo init` writes it:

```sh
./bin/yoyo init                 # configure the current directory
./bin/yoyo init --product example --directory path/to/project
```

That writes a complete `.yoyodyne/config.yaml` — every agent with its role, backend, model selector, instance count, and persona reference, plus the execution, approval, and product settings — and copies the five personas into `.yoyodyne/personas/`, where they are ordinary Markdown files in your repository. Nothing is inherited when the file loads, so `yoyo config show --origins` names the project file for every configured value — the one exception being `product.repository_id`, which is reported as `derived:product.id` because the file states the product id and lets the repository id follow from it. Editing a field is the whole of what changes the harness's behavior.

```yaml
# .yoyodyne/config.yaml, abbreviated
version: 1

product:
  id: example
  repository: .
  specifications: docs/product
  invariants: docs/decisions/invariants

checks: []          # yours to write; a run with none is refused

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
    persona:
      version: v1
      path: personas/developer.md   # relative to .yoyodyne/
  # ... product-manager, architect, development-manager, and reviewer
```

`checks` is the one thing `init` cannot write for you: the harness has no way to guess a project's toolchain, and a run with nothing to verify has no gate to integrate behind, so `yoyo run` refuses it. The generated file carries commented examples for Go, TypeScript, Python, and Java.

**What owning the configuration costs.** A later Yoyodyne that improves a persona or corrects a model selector does not reach a project that already has its own copy of it. Re-run `yoyo init` in a scratch directory and merge the difference if you want those improvements. The executable's built-in bundle is now the template `init` generates from rather than a layer projects keep inheriting — inheritance still works for a project that writes `extends: builtin:v1`, and is the right choice for a fleet that should improve together, but the explicit file is what Yoyodyne ships.

Yoyodyne finds the nearest `.yoyodyne/config.yaml` from the current directory upwards, so it runs from the project root or anywhere beneath it. To see what a configuration resolves to and where each value came from:

```sh
./bin/yoyo config show --effective --origins
```

See the [configuration guide](docs/configuration.md) for the full layout, the `init` flags, precedence, merge and removal semantics, persona rules, extending a bundle, and migration from `.yoyodyne.yaml`.

See the [v1 harness design](docs/v1-harness-design.md) for the architecture and self-hosting sequence.
