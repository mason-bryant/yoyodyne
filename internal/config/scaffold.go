package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ScaffoldOptions are the project facts a bundle cannot supply, because they
// describe the project being worked on rather than the harness working on it.
type ScaffoldOptions struct {
	ProductID  string
	Repository string
	// Detection is what reading the project's own files proposed as checks. Its
	// confident proposals become the checks list; its candidates are written
	// beside them, commented out and marked, for the operator to choose from.
	Detection Detection
}

// ScaffoldFile is one generated file, named relative to the project's
// .yoyodyne directory so a caller decides where that directory lives.
type ScaffoldFile struct {
	Path    string
	Content []byte
}

// Scaffold is a complete project configuration together with the persona files
// it references. Nothing in it is inherited at load time: the bundle is the
// template these files were generated from, not a layer underneath them.
type Scaffold struct {
	// Bundle names the template the files were generated from, for reporting.
	Bundle   string
	Config   ScaffoldFile
	Personas []ScaffoldFile
	// Lock records what the template supplied, so a later executable that
	// improves one of these values can say so rather than being invisible to a
	// project that owns its file outright. It is generated and committed, and
	// nothing reads it when the configuration loads.
	Lock ScaffoldFile
}

// Files lists everything the scaffold writes, configuration first and the
// baseline last: the configuration is what a caller reports and loads back, and
// the baseline is the one file here that decides nothing.
func (s Scaffold) Files() []ScaffoldFile {
	files := make([]ScaffoldFile, 0, len(s.Personas)+2)
	files = append(files, s.Config)
	files = append(files, s.Personas...)
	return append(files, s.Lock)
}

// NewScaffold renders a complete project configuration from a built-in bundle.
// The bundle supplies the agents, their selectors, and their personas; the
// options supply what only the project knows. The result states every value
// explicitly, so a project written this way loads without consulting the bundle
// again — and, deliberately, without receiving later changes to it.
func NewScaffold(bundleName string, options ScaffoldOptions) (Scaffold, error) {
	template, err := loadBuiltinBundle(bundleName)
	if err != nil {
		return Scaffold{}, err
	}
	// Resolving the bundle on its own fills in the harness defaults it does not
	// state and loads its persona text, so the rendered file can state both
	// rather than leaving them to be supplied again at load time.
	resolved, err := resolveLayers([]layer{{
		origin:   template.name,
		document: template.document,
		personas: template.personas,
	}})
	if err != nil {
		return Scaffold{}, err
	}

	effective := resolved.Config
	effective.Extends = ""
	effective.Product.ID = domain.ProductID(strings.TrimSpace(options.ProductID))
	effective.Product.Repository = strings.TrimSpace(options.Repository)
	effective.Checks = options.Detection.Commands()
	// Validate what loading the rendered file will see rather than what the
	// struct happens to hold, so the repository id is derived from the product
	// id here exactly as it is derived there. It stays out of the rendered file
	// for the same reason: it comes from a value the reader can already see.
	validated := effective
	validated.Product.RepositoryID = domain.RepositoryID(validated.Product.ID)
	if err := validated.Validate(); err != nil {
		return Scaffold{}, err
	}

	personas, err := scaffoldPersonas(effective)
	if err != nil {
		return Scaffold{}, err
	}
	// The baseline is taken from the same template, at the same moment, so what
	// it records is exactly what the file beside it was generated from. Taking it
	// later -- from a rerun, or from a project's own values -- would record a
	// guess about where those values came from.
	lock, err := NewLock(template.name)
	if err != nil {
		return Scaffold{}, err
	}
	return Scaffold{
		Bundle:   template.name,
		Config:   ScaffoldFile{Path: FileName, Content: renderScaffoldConfig(effective, template.name, options.Detection)},
		Personas: personas,
		Lock:     ScaffoldFile{Path: LockFileName, Content: lock.Render()},
	}, nil
}

// scaffoldPersonas collects the persona text every agent refers to, at the path
// the rendered configuration refers to it by. Agents that share a persona share
// one file, because two copies of one persona is two things to edit.
func scaffoldPersonas(effective Config) ([]ScaffoldFile, error) {
	byPath := map[string]string{}
	for _, name := range sortedNames(effective.Agents) {
		persona := effective.Agents[name].Persona
		if !persona.Defined() {
			continue
		}
		existing, seen := byPath[persona.Path]
		if seen && existing != persona.Text {
			return nil, fmt.Errorf("persona %q has two different texts in the template", persona.Path)
		}
		byPath[persona.Path] = persona.Text
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]ScaffoldFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, ScaffoldFile{Path: path, Content: []byte(byPath[path])})
	}
	return files, nil
}

// renderScaffoldConfig writes the configuration as a file meant to be read and
// edited rather than only parsed: every value the harness uses, in a stable
// order, with the reasoning an operator needs to change one safely.
func renderScaffoldConfig(effective Config, bundleName string, detection Detection) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, `# Yoyodyne project configuration.
#
# This file is complete. Every value the harness uses is written here, so what
# the file says is what runs and there is nothing to look up somewhere else.
# It was generated from the %s template inside the executable, and
# the personas beside it were copied from there at the same time; both are this
# project's to edit now.
#
# That ownership has a price worth knowing up front: a later Yoyodyne that
# improves a persona or corrects a model selector does not change this project,
# because nothing here is inherited. What it does instead is tell you. The
# config.lock beside this file records what the template supplied when this was
# generated, "yoyo doctor" and "yoyo config validate" say so on any run where
# something you never edited has improved, and "yoyo config drift" shows what
# each value was and is so you can take the ones you want.
#
# "yoyo config show --effective --origins" reports the resolved values and
# where each one came from.
version: %d

product:
  id: %s
  repository: %s
`, bundleName, effective.Version, effective.Product.ID, effective.Product.Repository)

	fmt.Fprintf(&builder, `  # The product manager reads product intent from the specifications under this
  # directory and from nowhere else in the repository. Beside them, labeled as a
  # description of what is built rather than as intent, it is given the README,
  # a fixed set of operator-facing documents under docs/, and the help the
  # commands print -- not this file, not the source, not the design document. It
  # must stay inside the repository.
  specifications: %s
  # The architect's durable architectural invariants: one Markdown file per
  # constraint, named by its id. The harness delivers the ones relevant to a
  # work item into the developer's context and the reviewer's evidence. A
  # project with no such directory simply has no invariants yet.
  invariants: %s
  # The architect's two artifact homes: designs and specifications, and the
  # decision records the invariants above are extracted from. Every Markdown
  # file in these directories and in the specifications directory carries its
  # identity and metadata in frontmatter -- id, kind, status, what it supports,
  # and a revision log -- so downstream work can refer to one durably. A
  # directory that does not exist simply records no artifacts of that kind yet.
  designs: %s
  decisions: %s

execution:
  max_concurrent_developers: %d
  repair_attempts_before_replan: %d
  # A promotion that loses a race -- to another run, or to whoever moved the
  # target branch while this one was working -- is replayed onto where the
  # target went and tried again, up to this many times. Every retry re-runs the
  # checks and asks for a fresh independent review, because the change that
  # would now be promoted is not the one that was approved. A replay that
  # conflicts is never resolved automatically: it stops the run and is left for
  # a person.
  integration_retries_before_reconciliation: %d
  # A provider invocation that dies without judging the work -- an API error its
  # own retries did not outlast, or a response cut off mid-flight -- is reissued
  # in the same worktree and the same session, up to this many times, rather than
  # failing the run. Nothing here is a fault in the change, so a relaunch spends
  # no repair attempt. A run that spends this budget stops and records a blocker.
  transient_relaunches_before_blocking: %d
  worktree_root: %s
  # The remote publishing opens pull requests against, and pushes run branches
  # to unless push_remote names another. It is only consulted when
  # approvals.publishing is automatic; a repository with no remote by this name
  # publishes nothing and stays purely local.
  remote: %s
  # Set push_remote to your fork's remote when you cannot push to the remote
  # above. Run branches then go to the fork and pull requests are opened across
  # the two, which is how a contributor without push access publishes at all.
  # Leaving it out pushes run branches to the remote above.
  # push_remote: fork
  # A provider usage limit that is exhausted pauses a run rather than failing
  # it. The maximum covers the five-hour limit and deliberately stops short of
  # the seven-day one, so a capacity problem that needs a person reaches one
  # instead of a timer. The in-process bound is how much of that a run spends
  # sleeping here rather than exiting with its deadline recorded. The unknown
  # reset pause is the interval between probes, whether or not the provider
  # named a reset: a quoted reset bounds the wait rather than gating it, because
  # capacity can be bought and windows can roll before the quoted edge.
  usage_limit_max_pause: %s
  usage_limit_in_process_pause: %s
  usage_limit_unknown_reset_pause: %s
  # A provider whose own servers are transiently overloaded refuses without
  # naming any reset at all, so a run waits this much shorter interval and asks
  # again. It spends the same maximum above, so an overload that never lifts
  # reaches that bound rather than reissuing forever.
  server_overload_pause: %s
  # The total budget one check below gets: the whole time it may run, not the
  # time it may stay quiet. Raise it as the suite grows, and raise it again for
  # concurrency -- N runs at once multiply the wall clock of every suite without
  # multiplying the cores it runs on, so either this scales with
  # max_concurrent_developers or the checks are left to serialize themselves.
  check_timeout: %s
  # "yoyo work --watch" stays open instead of returning when the queue is empty,
  # and this is how long it waits before reading the queue again. Nothing is
  # cached between readings, so this is also the delay on work you admit or
  # reorder while it is running. An idle watch costs one local tracker read per
  # interval and asks no provider anything.
  work_poll: %s
  # The failure-storm brake for a session left running unattended: this many runs
  # blocking in a row, with nothing landing between them, holds intake and leaves
  # it held for you to lift. It is aimed at a broken machine rather than a broken
  # item -- an item that keeps failing is left alone until something about it
  # changes -- and "0" turns it off, leaving you as the only thing that holds
  # intake.
  blocked_runs_before_intake_hold: %d
  # Every new run compiles the built-in delivery definition and records where it
  # sent the run, beside the run's own record. The delivery is the same delivery
  # either way -- the definition's steps perform nothing -- so what this buys is
  # the account. Setting it to "false" is the rollback to the legacy path, and it
  # reaches new runs only: a run already in flight finishes on whatever it
  # started on.
  declarative_delivery: %t

# When work that has stopped moving is looked at, and what looking at it may
# spend. An approved publication nobody has merged is docketed once it has sat
# this long -- an age rather than a deadline, because what makes it stuck is
# that nothing happened to it. The rounds cap is the total review rounds one
# work item may accumulate before triage stops handing it back for repair;
# past it triage may still escalate the item or re-scope it, and "0" means an
# item that reaches triage is never repaired again.
#
# There is a third setting, "repair_grant_attempts": how many repair attempts
# triage hands an item worth another go. It is left out deliberately, the way
# the repository id above is, because unstated it follows
# execution.repair_attempts_before_replan and keeps following it when you change
# that. State it here to size a grant differently; it may not be zero, since a
# grant of nothing leaves the item exactly where it was.
triage:
  stuck_merge_age: %s
  review_rounds_cap: %d

# How long one role may go on asking another one something. Roles can put a
# question to each other through the harness -- the product manager asking the
# architect what a goal costs, the architect asking the product manager whether a
# trade-off is one a user would accept -- and every exchange is recorded where
# you can read it. This is the hard limit on rounds in one thread. Reaching it
# closes the exchange as unresolved and tells you about it, because two judgement
# models can defer to each other politely for ever and the only thing that ends
# that is a number. It may not be zero: the way to leave the channel unused is to
# leave it unused.
exchange:
  max_rounds: %d

# What you approve, and what runs without asking. The brief and the goals are
# "human" deliberately: they are what you state, and everything else traces back
# to them. "yoyo artifact approve <id>" records your approval in the document's
# frontmatter, against the revision it was given for, so a document amended
# afterwards reads as approved-and-amended-since rather than as approved. An
# unapproved document still loads and still governs what is downstream of it;
# what your approval of the goals decides is what reaches the work queue, under
# work_items below. Designs are "automatic" because a design serving an approved
# goal is the architect's judgement about how.
#
# work_items is the one approval that gates rather than records, and it is where
# you say how much of this you want to watch. "human" puts every work item to you
# before it is admitted to the queue, which is what you get until you say
# otherwise; it refuses the product manager's direct "create" as well as the
# automatic admission, so the work is proposed to you instead of arriving through
# a door the setting left open. "automatic" moves that approval up to your goals:
# work that traces to a goal you approved is then admitted without asking you,
# and you are told afterwards what went in. Work that traces to no goal, work
# that would cut against one, and a change to the goals themselves still stop and
# ask either way. Turning it on gets you a second ramp for free -- nothing is
# admitted without asking until you have actually approved a goal, so it still
# asks about everything until your first "yoyo artifact approve".
#
# Integration and publishing are opted in to separately. Automatic integration is
# refused unless it is actually gated by the checks below and a reviewer agent,
# and publishing is what pushes a branch and opens a pull request where other
# people can see it.
approvals:
  brief: %s
  goals: %s
  designs: %s
  work_items: %s
  integration: %s
  publishing: %s
`,
		effective.Product.Specifications,
		effective.Product.Invariants,
		effective.Product.Designs,
		effective.Product.Decisions,
		effective.Execution.MaxConcurrentDevelopers,
		effective.Execution.RepairAttemptsBeforeReplan,
		effective.Execution.IntegrationRetriesBeforeReconciliation,
		effective.Execution.TransientRelaunchesBeforeBlocking,
		effective.Execution.WorktreeRoot,
		effective.Execution.Remote,
		renderScaffoldDuration(effective.Execution.UsageLimitMaxPause),
		renderScaffoldDuration(effective.Execution.UsageLimitInProcessPause),
		renderScaffoldDuration(effective.Execution.UsageLimitUnknownResetPause),
		renderScaffoldDuration(effective.Execution.ServerOverloadPause),
		renderScaffoldDuration(effective.Execution.CheckTimeout),
		renderScaffoldDuration(effective.Execution.WorkPoll),
		effective.Execution.BlockedRunsBeforeIntakeHold,
		effective.Execution.DeclarativeDelivery,
		renderScaffoldDuration(effective.Triage.StuckMergeAge),
		effective.Triage.ReviewRoundsCap,
		effective.Exchange.MaxRounds,
		effective.Approvals.Brief,
		effective.Approvals.Goals,
		effective.Approvals.Designs,
		effective.Approvals.WorkItems,
		effective.Approvals.Integration,
		effective.Approvals.Publishing,
	)

	renderScaffoldReporting(&builder)

	renderScaffoldChecks(&builder, detection)

	renderScaffoldAccounts(&builder, effective)

	builder.WriteString(`
# Every agent is stated in full: nothing about them is inherited. A model
# selector that names a family, such as "opus", floats to that family's current
# default; an exact identifier such as claude-opus-5 pins a version. Persona
# paths are relative to the directory this file is in and must be Markdown
# inside it.
# Each agent names the account it runs under, from the mapping above. Delete an
# agent to remove it, edit one to change it.
agents:
`)
	for _, name := range sortedNames(effective.Agents) {
		renderScaffoldAgent(&builder, name, effective.Agents[name])
	}
	return []byte(builder.String())
}

// renderScaffoldDuration writes a duration the way an operator would type it.
// Go's own rendering spells six hours "6h0m0s", and a file people are meant to
// edit should not teach them a longer spelling than the one it accepts.
func renderScaffoldDuration(duration Duration) string {
	text := duration.String()
	switch {
	case strings.HasSuffix(text, "h0m0s"):
		return strings.TrimSuffix(text, "0m0s")
	case strings.HasSuffix(text, "m0s"):
		return strings.TrimSuffix(text, "0s")
	default:
		return text
	}
}

// The headings a generated configuration puts over commented commands. They are
// deliberately unmissable and deliberately stable, and they are three rather
// than one because they ask three different things: a demand to choose belongs
// only where a run cannot happen until somebody does, and putting it anywhere
// else teaches an operator to skip past it.
const (
	// CandidateMarker heads candidates when nothing was written into "checks":
	// a run is refused until one of them is chosen, so a decision is owed now.
	CandidateMarker = "# YOU MUST CHOOSE"
	// UndecidedMarker heads the same candidates when a checks list was written
	// anyway. The question is still open, but the file already runs, so nothing
	// waits on the answer.
	UndecidedMarker = "# ALSO FOUND, AND NOT DECIDED"
	// AlternativeMarker heads commands detection read and decided against,
	// because what it wrote already covers them. Nothing is owed here at all;
	// they are shown so an operator can swap one in.
	AlternativeMarker = "# ALSO FOUND, AND NOT NEEDED"
)

// checksGuide points at the per-language examples and the reasoning behind them,
// for a project that has no checkout of Yoyodyne to look them up in.
const checksGuide = "https://github.com/mason-bryant/yoyodyne/blob/main/docs/configuration.md#checks"

// slackGuide points at the whole Slack recipe -- the app, the invitation, and
// where the two tokens go -- which is the part of it that does not happen in
// this file. It is a URL for the reason checksGuide is one: a generated project
// has no checkout of Yoyodyne to read docs/slack/setup.md out of.
const slackGuide = "https://github.com/mason-bryant/yoyodyne/blob/main/docs/slack/setup.md"

// renderScaffoldReporting writes the two optional top-level sections a project
// is otherwise given no sign of: where it reports, and which humans it
// recognizes. Both are written commented out, because both are off by default
// and a project that says nothing about either reports nothing and recognizes
// nobody -- which stays the default, since uncommenting is the whole gesture
// asked for here.
//
// They are written at all because the alternative is what an operator following
// the setup recipe actually met: a step saying to add a section to a file that
// gives no hint of its shape, or that the capability exists. Every line below
// is written so that deleting its leading "# " leaves a valid entry, the way
// the commented checks further down are.
func renderScaffoldReporting(builder *strings.Builder) {
	fmt.Fprintf(builder, `
# Reporting into Slack, which is optional and off. A project that says nothing
# about it reports nothing -- which is every project until it opts in -- and an
# installation without reporting runs work exactly as one with it does. Switched
# on, it is one thread per work item and one message per milestone, posted by a
# separate "yoyo slack" process that holds this project's two tokens. No run and
# no agent ever holds one, which is why no token belongs in this file. Uncomment
# the block below and name this workspace's channel, by id from the channel's
# About panel or by #name; an id is worth preferring because a rename does not
# break it. "yoyo setup" writes these two values for you if you would rather.
# Creating the app, inviting it, and storing its tokens are the rest of it:
# %s
#
# slack:
#   enabled: true
#   channel: C0123456789

# The humans this project recognizes, and what each of them may do. A project
# that names nobody -- which is every project until it names somebody -- is
# closed rather than open: it recognizes nobody, not everybody. An entry binds
# one person's identifier namespaces, because an act carries an identifier and
# never a person -- a commit carries an address, a push carries a forge account,
# a thread reply carries a member id -- and binding them together is what lets an
# authority check resolve whichever one arrived to the same human. "own-intent"
# is authority over what the product is for, and at most one human may hold it;
# "direct-work" is authority to steer work already in flight, including the
# thread replies the Slack sink records as directives. Who may steer from Slack
# is derived from this rather than authored beside it -- the humans granted
# direct-work who have bound a member id, and nobody else -- so until somebody is
# named here, every reply in a thread is answered saying it was not acted on.
#
# operators:
#   your-name:
#     git_email: you@example.com
#     forge_account: your-account
#     slack_member_id: U0123456789   # your Slack profile -> "Copy member ID"
#     grants:
#       - own-intent
#       - direct-work
`, slackGuide)
}

// renderScaffoldChecks writes the checks section: what detection proposed, where
// each proposal came from, and what it could not decide. A proposal is only
// worth having if the reader can see what it was derived from, so provenance is
// written beside the commands rather than left to be reconstructed.
func renderScaffoldChecks(builder *strings.Builder, detection Detection) {
	builder.WriteString(`
# Checks are this project's own. Each entry is run through "/bin/sh -c" in the
# run's worktree, so shell syntax works. A check must be non-interactive and
# must exit non-zero on failure -- a run stops at a failing check and never
# reaches review or integration. A run with no checks at all is refused.
`)
	if len(detection.Checks) > 0 {
		fmt.Fprintf(builder, `#
# "yoyo init" proposed the list below from files this project already has, named
# against each entry. It executed nothing to find them: they are what this
# repository announces about itself rather than what it is known to need. Read
# them before the first run and edit or delete whatever does not belong. The
# configuration guide has per-language examples and the reasoning behind them:
# %s
checks:
`, checksGuide)
		lastSource := ""
		for _, proposal := range detection.Checks {
			if proposal.Source != lastSource {
				fmt.Fprintf(builder, "  # from %s\n", proposal.Source)
				lastSource = proposal.Source
			}
			fmt.Fprintf(builder, "  - %s\n", proposal.Command)
		}
	} else {
		found := `# This list is what makes the file usable rather than merely valid, and "yoyo
# init" found nothing in this project to propose for it. The configuration guide
# has these examples with the reasoning behind them:`
		if len(detection.Candidates) > 0 {
			found = `# This list is what makes the file usable rather than merely valid, and "yoyo
# init" found nothing here it could settle on its own -- what it did find is
# below, marked, and waiting on you. The configuration guide has these examples
# with the reasoning behind them:`
		}
		fmt.Fprintf(builder, `#
%s
# %s
#
#   # Go
#   checks:
#     - go test ./...
#     - go vet ./...
#     - gofmt -l . | (! grep .)
#
#   # TypeScript / Node
#   checks:
#     - npm ci
#     - npx tsc --noEmit
#     - npm test -- --run
#     - npx eslint .
#
#   # Python
#   checks:
#     - python -m pytest -q
#     - python -m ruff check .
#     - python -m mypy .
#
#   # Java (Maven)
#   checks:
#     - mvn --batch-mode --quiet verify
#
#   # Java (Gradle)
#   checks:
#     - ./gradlew --no-daemon check
checks: []
`, found, checksGuide)
	}
	listed := len(detection.Checks) > 0
	renderScaffoldCandidates(builder, detection.Candidates, listed)
	renderScaffoldAlternatives(builder, detection.Alternatives)
}

// renderScaffoldCandidates writes what detection found and would not choose
// between. Which heading it writes turns on whether a checks list was written at
// all: with an empty list a run is refused until somebody chooses, and with a
// written one the file already works and the question is merely open. Demanding
// a choice in both cases would make the demand mean nothing in either.
func renderScaffoldCandidates(builder *strings.Builder, candidates []CheckProposal, listed bool) {
	if len(candidates) == 0 {
		return
	}
	if listed {
		builder.WriteString("\n" + UndecidedMarker + ` -- nothing below runs, and the list above stands
# without it. "yoyo init" read these out of this project too and could not tell
# whether or which of them belongs, so it wrote none of them into "checks"
# above. Uncomment what does belong -- delete the leading "#" and nothing else
# -- and delete the rest.
`)
	} else {
		builder.WriteString("\n" + CandidateMarker + ` -- nothing below runs, and the list above is empty, so a
# run is refused until this is settled. "yoyo init" found these and could not
# tell which one is this project's gate, so it wrote none of them into "checks"
# above. Uncomment what belongs here -- delete the leading "#" and nothing else
# -- then delete the rest, or write your own instead. Replace "checks: []"
# above with "checks:" first, so the list can take the entry.
`)
	}
	renderScaffoldCommented(builder, candidates)
}

// renderScaffoldAlternatives writes what detection read and decided against.
// Nothing here is owed an answer: the list above already covers it, and this
// exists so an operator who would rather have one of these can see it and swap.
func renderScaffoldAlternatives(builder *strings.Builder, alternatives []CheckProposal) {
	if len(alternatives) == 0 {
		return
	}
	builder.WriteString("\n" + AlternativeMarker + ` -- nothing below runs, and nothing below has to be
# chosen. "yoyo init" read these out of this project as well and left them out
# for the reason given against each, because what is in "checks" above already
# covers them. Swap one in only if you would rather have it.
`)
	renderScaffoldCommented(builder, alternatives)
}

// renderScaffoldCommented writes commands commented out beneath their
// provenance, grouped by the reason they were not written rather than by
// artifact: one question is one paragraph, however many files went into it.
// Every command line is written so that deleting its leading "#" leaves a valid
// entry of the checks list above, because that is the whole gesture the headings
// above ask for.
func renderScaffoldCommented(builder *strings.Builder, proposals []CheckProposal) {
	for start := 0; start < len(proposals); {
		end := start
		for end < len(proposals) && proposals[end].Reason == proposals[start].Reason {
			end++
		}
		group := proposals[start:end]
		builder.WriteString("#\n")
		header := "#  # from " + strings.Join(ProposalSources(group), ", ")
		if group[0].Reason == "" {
			builder.WriteString(header + "\n")
		} else {
			wrapScaffoldComment(builder, header+" -- ", "#  # ", group[0].Reason)
		}
		for _, proposal := range group {
			fmt.Fprintf(builder, "#  - %s\n", proposal.Command)
		}
		start = end
	}
}

// wrapScaffoldComment writes one comment across as many lines as it takes,
// keeping the generated file inside the width the rest of it is written to.
func wrapScaffoldComment(builder *strings.Builder, first, continued, text string) {
	const width = 79
	line, prefix := first, first
	for _, word := range strings.Fields(text) {
		if len(line) > len(prefix) {
			if len(line)+1+len(word) > width {
				builder.WriteString(line + "\n")
				line, prefix = continued, continued
			} else {
				line += " "
			}
		}
		line += word
	}
	builder.WriteString(line + "\n")
}

// renderScaffoldAccounts writes the provider accounts the agents below run
// under. A new project has one, and it is written out rather than left to the
// harness default for the reason everything else here is: the file states what
// runs.
func renderScaffoldAccounts(builder *strings.Builder, effective Config) {
	builder.WriteString(`
# The provider accounts this project runs agents under, keyed by the alias each
# one is known by here. A new project has one: the agents below are each assigned
# to it, every run records the alias it ran under, and every surface that reports
# a run says it back. The alias is the whole of what an entry is for -- an entry
# never holds a credential, because authentication is the provider's own and
# lives on this machine. Rename the alias to whatever you call this account.
#
# A second entry pools the work: active accounts are round-robined a run at a
# time, a reserved one is served from only when no active account can be, and
# weekly_budget_usd stands an account down once the runs that named it have cost
# that much over seven days. bin/yoyo-account signs a second account in and
# prints the entry for it; docs/configuration.md#provider-accounts has the rest.
accounts:
`)
	for _, alias := range effective.AccountAliases() {
		description := strings.TrimSpace(effective.Accounts[alias].Description)
		if description == "" {
			// An entry with nothing under it is written as an empty mapping rather
			// than as a bare key: the file is meant to be read and edited, and a key
			// with nothing after the colon reads as something somebody deleted.
			// Describing the account here would be words the harness made up about
			// whose account it is.
			fmt.Fprintf(builder, "  %s: {}\n", alias)
			continue
		}
		fmt.Fprintf(builder, "  %s:\n", alias)
		fmt.Fprintf(builder, "    description: %s\n", description)
	}
}

func renderScaffoldAgent(builder *strings.Builder, name string, agent AgentConfig) {
	fmt.Fprintf(builder, "  %s:\n", name)
	fmt.Fprintf(builder, "    role: %s\n", agent.Role)
	fmt.Fprintf(builder, "    backend: %s\n", agent.Backend)
	fmt.Fprintf(builder, "    model: %s\n", agent.Model)
	fmt.Fprintf(builder, "    account: %s\n", agent.Account)
	fmt.Fprintf(builder, "    instances: %d\n", agent.Instances)
	if !agent.Persona.Defined() {
		return
	}
	builder.WriteString("    persona:\n")
	fmt.Fprintf(builder, "      version: %s\n", agent.Persona.Version)
	fmt.Fprintf(builder, "      path: %s\n", agent.Persona.Path)
}

func sortedNames(agents map[string]AgentConfig) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
