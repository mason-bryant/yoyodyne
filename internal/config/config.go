// Package config loads Yoyodyne's effective configuration from a project's
// .yoyodyne directory. A project owns its configuration outright: `yoyo init`
// generates a complete file from the versioned bundle shipped inside the
// executable and copies that bundle's personas into the project, so what runs
// is what the project can read. A project may still inherit the bundle by name
// with "extends" and overlay only what it changes, which trades the legibility
// of an explicit file for defaults that improve when the executable does.
// Either shape is usable from a repository with no access to the Yoyodyne
// source checkout.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const CurrentVersion = 1

// DefaultSpecifications is where the product manager reads product intent from
// when nothing names another directory. It is the layout the design recommends
// for human-readable product artifacts, so a project that followed that layout
// needs no setting at all.
const DefaultSpecifications = "docs/product"

// DefaultInvariants is where the architect's durable invariants live when
// nothing names another directory. It is the recommended layout for the same
// reason the specifications default is: a project that followed it writes
// nothing down, and a project with no such directory simply has no invariants
// yet rather than a broken configuration.
const DefaultInvariants = "docs/decisions/invariants"

// DefaultDesigns and DefaultDecisions are the other two homes canonical
// artifacts live in when nothing names another directory. Together with the
// specifications directory they are what the harness reads artifact identity
// and metadata from, and they follow the recommended layout for the same reason
// the invariants default does: a project that followed it writes nothing down,
// and a project with no such directory simply records no artifacts of that kind
// yet. The decisions home contains the invariants one by default, and the
// invariants keep their own identity scheme rather than being read twice.
const (
	DefaultDesigns   = "docs/designs"
	DefaultDecisions = "docs/decisions"
)

type Config struct {
	Version int `yaml:"version" json:"version"`
	// Extends names the built-in bundle this configuration inherits from, and
	// is empty for a complete standalone configuration.
	Extends   string                 `yaml:"extends,omitempty" json:"extends,omitempty"`
	Product   Product                `yaml:"product" json:"product"`
	Execution Execution              `yaml:"execution" json:"execution"`
	Approvals Approvals              `yaml:"approvals" json:"approvals"`
	Checks    []string               `yaml:"checks" json:"checks"`
	Agents    map[string]AgentConfig `yaml:"agents" json:"agents"`
	// Slack configures the reporting sink. It is absent from a project that does
	// not report to a workspace, which is every project until one opts in.
	Slack Slack `yaml:"slack,omitempty" json:"slack,omitempty"`
}

// Slack is what a project says about reporting into a chat workspace. What it
// deliberately does not hold is a credential: the sink's two tokens live in the
// sink process's environment and nowhere else, so nothing that is read into a
// prompt, a context bundle, or a run's environment can carry one. What is here
// is identity and addressing, which is configuration in the ordinary sense —
// checked in, reviewed with the code, and readable by anybody who can read the
// repository.
type Slack struct {
	// Enabled is the switch. A project that has not set it reports nothing, and
	// the sink refuses to start rather than posting into a workspace nobody
	// configured.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Channel is where the threads are opened, as a channel id (the stable
	// thing, which a rename does not break) or a #name. A project that enables
	// Slack without one is refused at load, before any work is claimed, because
	// the alternative is a sink that starts, reads a stream, and then discovers
	// it has nowhere to post.
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	// Operators is the allow-list of Slack user ids whose thread replies the
	// harness will act on. It is inert until the inbound half exists: nothing
	// today reads a reply at all. It lives here rather than in the environment
	// because a user id is identity rather than a secret, and it defaults to
	// empty so enabling reporting never enables anybody to steer the harness
	// from a chat workspace by accident.
	Operators []string `yaml:"operators,omitempty" json:"operators,omitempty"`
}

type Product struct {
	ID           domain.ProductID    `yaml:"id" json:"id"`
	RepositoryID domain.RepositoryID `yaml:"repository_id,omitempty" json:"repository_id,omitempty"`
	Repository   string              `yaml:"repository" json:"repository"`
	// Specifications is the directory the product manager reads product intent
	// from, relative to the repository root. It is the whole of what that role
	// is shown about the product, so it is confined to the repository: a path
	// that escapes it would put arbitrary text in front of the role that decides
	// what the product is for.
	Specifications string `yaml:"specifications" json:"specifications"`
	// Invariants is the directory of durable architectural invariants the
	// architect owns, relative to the repository root. The harness reads it to
	// deliver the ones relevant to a work item into the developer's context and
	// the reviewer's evidence, so it is confined to the repository for the same
	// reason the specifications are: a path that escaped it would put arbitrary
	// text in front of every developer as a constraint on their change.
	Invariants string `yaml:"invariants" json:"invariants"`
	// Designs and Decisions are the architect's two artifact homes, relative to
	// the repository root: the designs and specifications that serve the goals,
	// and the decision records the invariants are extracted from. With
	// Specifications they are the directories the harness reads canonical
	// artifact identity and metadata from, so they are confined to the
	// repository for the same reason the others are.
	Designs   string `yaml:"designs" json:"designs"`
	Decisions string `yaml:"decisions" json:"decisions"`
}

type Execution struct {
	MaxConcurrentDevelopers    int `yaml:"max_concurrent_developers" json:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan int `yaml:"repair_attempts_before_replan" json:"repair_attempts_before_replan"`
	// IntegrationRetriesBeforeReconciliation bounds how many times a run whose
	// promotion lost a race — to another run, or to whoever moved the target
	// branch mid-run — replays its change onto where the target went and tries
	// again. Each retry re-runs the deterministic checks and obtains a fresh
	// independent review, because the reviewed change is not the change that
	// would now be promoted. Zero never retries, which is the behavior a run had
	// before this bound existed: the first refusal ends it.
	IntegrationRetriesBeforeReconciliation int `yaml:"integration_retries_before_reconciliation" json:"integration_retries_before_reconciliation"`
	// TransientRelaunchesBeforeBlocking bounds how many times a run reissues a
	// provider invocation that died without judging the work — an API error the
	// provider's own retries did not outlast, or a response cut off mid-flight.
	// The relaunch continues the same worktree and the same session, so nothing
	// the dead attempt built is redone. One budget covers the developer and the
	// reviewer, because what it bounds is how much of the provider's weather one
	// run absorbs. Zero never relaunches, which is the behavior a run had before
	// this bound existed: the first transient death ends it.
	TransientRelaunchesBeforeBlocking int `yaml:"transient_relaunches_before_blocking" json:"transient_relaunches_before_blocking"`
	// The four triage caps bound what one harness will spend on one work item
	// across every run of it, which is the thing no budget above bounds: each of
	// those resets when a run does, so an item triaged again and again is an item
	// nothing was ever going to stop. They are counted per item and per machine
	// in the product's durable triage record.
	//
	// TriageRepairGrantsPerItem is how many times triage may hand an item another
	// go at its own change, TriageRerunsPerItem how many times it may cause the
	// item to be run again from the start, and TriageMergeRearmsPerItem how many
	// times it may re-arm a merge the forge accepted and then dropped. Zero is a
	// deliberate choice for any of them — never do this, send it to a person
	// instead — and a negative bound is not one.
	TriageRepairGrantsPerItem int `yaml:"triage_repair_grants_per_item" json:"triage_repair_grants_per_item"`
	TriageRerunsPerItem       int `yaml:"triage_reruns_per_item" json:"triage_reruns_per_item"`
	TriageMergeRearmsPerItem  int `yaml:"triage_merge_rearms_per_item" json:"triage_merge_rearms_per_item"`
	// TriageReviewRoundsPerItem is the ceiling on the reviewer verdicts one item's
	// developer attempts may produce, across every run of it. It is not an action
	// anybody asks permission for — nothing requests to be reviewed — it is what
	// every repair grant is truncated against, so a grant is never larger than
	// what the item has room left to spend.
	TriageReviewRoundsPerItem int    `yaml:"triage_review_rounds_per_item" json:"triage_review_rounds_per_item"`
	WorktreeRoot              string `yaml:"worktree_root" json:"worktree_root"`
	// Remote names the Git remote publishing pushes to and opens pull requests
	// against. It is only consulted when `approvals.publishing` is automatic, and
	// a repository that has no remote by this name publishes nothing rather than
	// failing.
	Remote string `yaml:"remote" json:"remote"`
	// UsageLimitMaxPause bounds how long a run may wait for an exhausted
	// provider usage limit to reset. A reset further away than this is treated
	// as no usable reset time at all: the run stops and records a blocker
	// instead of sleeping on it. Zero disables waiting entirely, so every
	// exhausted limit blocks immediately.
	UsageLimitMaxPause Duration `yaml:"usage_limit_max_pause" json:"usage_limit_max_pause"`
	// UsageLimitInProcessPause is how much of that bound a run will spend
	// sleeping inside this process. The process sleeps probes until it has spent
	// this much on one run and then exits with the run still in flight and its
	// deadline recorded, so a later invocation resumes it rather than holding a
	// process open for hours. It is measured against every probe this process has
	// already slept, across phases, rather than against each probe separately: a
	// bound applied per probe would not bound how long the process stays open,
	// because a probe interval that fits under it fits however many times it is
	// taken. It is never larger than UsageLimitMaxPause in effect, because a
	// pause beyond that bound is refused before either path is chosen.
	UsageLimitInProcessPause Duration `yaml:"usage_limit_in_process_pause" json:"usage_limit_in_process_pause"`
	// UsageLimitUnknownResetPause is the interval between probes: how long a run
	// sleeps before reissuing the attempt to find out whether the provider will
	// serve it now. It applies whether or not a reset time was named, which is
	// the whole of the polling discipline. A limit reported without one is not
	// the same as having no capacity — the monthly overage allowance reports this
	// way while the ordinary rolling window keeps resetting on its usual schedule
	// — so it is waitable and simply carries no deadline. A limit reported with
	// one carries a deadline that bounds the wait rather than gating it, because
	// a reset time is a claim about the provider and claims go stale in both
	// directions. Either way a run sleeps this interval or the time left to the
	// deadline, whichever is shorter, and asks again. Every probe spends
	// UsageLimitMaxPause, so a provider that keeps refusing walks into that bound
	// rather than polling forever.
	UsageLimitUnknownResetPause Duration `yaml:"usage_limit_unknown_reset_pause" json:"usage_limit_unknown_reset_pause"`
	// CheckTimeout is the total budget one configured check gets: the whole time
	// it may run, not the time it may stay quiet. It scales with the work rather
	// than with the machine, so it is configured rather than fixed — a suite
	// grows, and N runs at once multiply its wall clock without multiplying the
	// cores it runs on, so the budget has to be raised or the runs serialized.
	// A check killed at this bound is not a check that failed: it is work that
	// may have been passing the whole time, which is why every check reports
	// what it spent against this budget rather than only the one that ran out.
	CheckTimeout Duration `yaml:"check_timeout" json:"check_timeout"`
	// ServerOverloadPause is how long a run waits before reissuing an attempt the
	// provider refused because its own servers were transiently overloaded. It is
	// the same polling discipline as an exhausted limit with a different clock:
	// an overload quotes no reset time and lifts in seconds rather than hours, so
	// waiting the usage-limit probe interval would park a run for half an hour on
	// a condition that had already passed. It spends UsageLimitMaxPause like every
	// other wait, so a provider that stays overloaded walks into that bound rather
	// than reissuing forever.
	ServerOverloadPause Duration `yaml:"server_overload_pause" json:"server_overload_pause"`
}

const (
	// defaultRemote is the remote publishing uses when nothing names another. A
	// repository with no remote by this name is not an error: it simply publishes
	// nothing and behaves exactly as a local-only project does.
	defaultRemote = "origin"
	// defaultUsageLimitMaxPause covers the provider's five-hour limit with slack
	// and stops short of its seven-day one. A run that would have to sleep for
	// days is not a run that should be sleeping: it stops and records a blocker,
	// so the capacity problem reaches a person instead of a timer.
	defaultUsageLimitMaxPause = Duration(6 * time.Hour)
	// defaultUsageLimitInProcessPause equals the maximum pause, so by default
	// every wait the harness will take at all is taken in this process and the
	// run continues on its own — which is what waiting out a usage limit is for.
	// Lowering it trades that for a run that exits with its deadline recorded
	// and is resumed by a later invocation.
	defaultUsageLimitInProcessPause = defaultUsageLimitMaxPause
	// defaultUsageLimitUnknownResetPause is how long to wait between probes,
	// whether or not a reset time was named. Short enough to resume soon after a
	// window rolls, long enough not to hammer a provider that is refusing — and
	// deliberately checked at least this often even under a multi-hour quoted
	// reset, because a quoted reset can be overtaken by a window that rolls or by
	// capacity somebody bought.
	defaultUsageLimitUnknownResetPause = Duration(30 * time.Minute)
	// defaultCheckTimeout has room for a suite several times the size of the one
	// that provoked it. The flat ten minutes it replaces killed a run whose
	// tests were passing package by package under the contention of two
	// concurrent runs, so the default is set against the contended case rather
	// than the single-run one: this repository's own suite takes well under two
	// minutes with two of them running at once, and a project that outgrows
	// thirty minutes raises this rather than meeting it as a failed run.
	defaultCheckTimeout = Duration(30 * time.Minute)
	// defaultServerOverloadPause is long enough to be worth waiting — the
	// provider CLI has already spent its own ten retries on the condition before
	// the harness ever sees it — and short enough that a run resumes within a
	// minute or two of the overload lifting, which is the timescale the provider's
	// own message describes.
	defaultServerOverloadPause = Duration(90 * time.Second)
)

type Approvals struct {
	// Brief, Goals, and Designs decide which canonical documents the operator's
	// approval is asked for. `human` means the operator approves the document and
	// the approval is recorded against it, in its frontmatter and against the
	// revision it was given for, so an approved document and a draft one are
	// distinguishable and a document amended after approval is distinguishable
	// from both. Neither setting gates anything: an unapproved document still
	// loads and still governs what is downstream of it, and what is reported is
	// what is recorded. Goals governs the non-goals with the goals, because a
	// bound on intent nobody approved is as much unapproved intent as a goal is.
	//
	// The shipped bundle says `human` for the brief and the goals deliberately
	// rather than by inheritance: they are what the operator states and the whole
	// of what the design asks a person to approve routinely, and everything
	// downstream traces to them. Designs are `automatic` for the same reason read
	// the other way — a design that serves an approved goal is the architect's
	// judgement about how, and asking a person to approve each one is the
	// per-change gate autonomy is the absence of.
	Brief       domain.ApprovalMode `yaml:"brief" json:"brief"`
	Goals       domain.ApprovalMode `yaml:"goals" json:"goals"`
	Designs     domain.ApprovalMode `yaml:"designs" json:"designs"`
	Integration domain.ApprovalMode `yaml:"integration" json:"integration"`
	// Publishing decides whether the harness pushes a run's branch and opens the
	// pull request its reviewer's verdict merges. It sits beside integration
	// because publishing has the wider blast radius of the two: integration moves
	// a local branch, publishing puts the work somewhere other people see it.
	// `human` leaves pushing and pull requests to the operator, which is what a
	// project gets until it opts in.
	Publishing domain.ApprovalMode `yaml:"publishing" json:"publishing"`
}

type AgentConfig struct {
	Role    domain.AgentRole `yaml:"role" json:"role"`
	Backend domain.Backend   `yaml:"backend" json:"backend"`
	// Model is the required provider model selector for every instance of this
	// agent. There is no implicit harness default: a family alias such as
	// "opus" intentionally floats to the backend's current default for that
	// family, while an exact provider identifier pins a version.
	Model     string `yaml:"model" json:"model"`
	Instances int    `yaml:"instances,omitempty" json:"instances"`
	// Persona is the resolved role guidance handed to this agent's prompt. It
	// may specialize how an agent works; it can never remove a harness
	// invariant, which is why the immutable contracts stay in Go.
	Persona Persona `yaml:"persona,omitempty" json:"persona,omitempty"`
}

// MaxPersonaBytes bounds a persona so role guidance stays guidance rather than
// an unbounded document smuggled into every prompt.
const MaxPersonaBytes = 32 << 10

// Persona is one resolved persona: the declared revision, the path it was
// declared with, where the text was actually read from, and the text itself.
// Text is excluded from serialized diagnostics, which report its size instead,
// so `config show` stays readable.
type Persona struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Bytes   int    `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	Text    string `yaml:"-" json:"-"`
}

// Defined reports whether an agent declares a persona at all. Agents in legacy
// complete configurations may declare none, and prompt construction then falls
// back to the immutable harness contract alone.
func (p Persona) Defined() bool {
	return strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.Path) != "" || strings.TrimSpace(p.Text) != ""
}

func (c Config) Validate() error {
	var problems []string

	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.Product.ID)); err != nil {
		problems = append(problems, err.Error())
	}
	if err := domain.ValidateIdentifier("repository id", string(c.Product.RepositoryID)); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Product.Repository) == "" {
		problems = append(problems, "product repository is required")
	}
	if err := validateSpecificationsDirectory(c.Product.Specifications); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product invariants", c.Product.Invariants); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product designs", c.Product.Designs); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product decisions", c.Product.Decisions); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Execution.MaxConcurrentDevelopers < 1 {
		problems = append(problems, "max_concurrent_developers must be at least 1")
	}
	if c.Execution.RepairAttemptsBeforeReplan < 0 {
		problems = append(problems, "repair_attempts_before_replan cannot be negative")
	}
	// Zero is a deliberate choice here too — never retry a refused promotion —
	// so only a negative bound, which describes no retry anybody could take, is
	// refused.
	if c.Execution.IntegrationRetriesBeforeReconciliation < 0 {
		problems = append(problems, "integration_retries_before_reconciliation cannot be negative")
	}
	// And here, for the same reason: zero says fail on the first transient death,
	// which is a choice, and a negative bound is not one.
	if c.Execution.TransientRelaunchesBeforeBlocking < 0 {
		problems = append(problems, "transient_relaunches_before_blocking cannot be negative")
	}
	// And once more for the triage caps: zero says triage never takes this action
	// on an item and hands it to a person instead, which is a choice somebody
	// might well make, and a negative cap is not one.
	for _, triageCap := range []struct {
		key   string
		value int
	}{
		{"triage_repair_grants_per_item", c.Execution.TriageRepairGrantsPerItem},
		{"triage_reruns_per_item", c.Execution.TriageRerunsPerItem},
		{"triage_merge_rearms_per_item", c.Execution.TriageMergeRearmsPerItem},
		{"triage_review_rounds_per_item", c.Execution.TriageReviewRoundsPerItem},
	} {
		if triageCap.value < 0 {
			problems = append(problems, triageCap.key+" cannot be negative")
		}
	}
	if strings.TrimSpace(c.Execution.WorktreeRoot) == "" {
		problems = append(problems, "worktree_root is required")
	}
	if err := validateRemoteName(c.Execution.Remote); err != nil {
		problems = append(problems, err.Error())
	}
	// Zero is a deliberate choice — never wait, block on every exhausted limit —
	// so only a negative bound, which describes no wait anybody could take, is
	// refused.
	if c.Execution.UsageLimitMaxPause < 0 {
		problems = append(problems, "usage_limit_max_pause cannot be negative")
	}
	if c.Execution.UsageLimitUnknownResetPause <= 0 {
		problems = append(problems, "execution.usage_limit_unknown_reset_pause must be positive")
	}
	if c.Execution.UsageLimitInProcessPause < 0 {
		problems = append(problems, "usage_limit_in_process_pause cannot be negative")
	}
	// Zero is not a deliberate choice here, unlike the pauses above: a check with
	// no budget at all holds a worktree, a claim, and a run open for as long as
	// it keeps running, and nothing else bounds it.
	if c.Execution.CheckTimeout <= 0 {
		problems = append(problems, "execution.check_timeout must be positive")
	}
	// An overload names no reset time, so this interval is the whole of the wait
	// rather than a bound on it. Zero would mean reissuing straight back into the
	// same overloaded server with nothing between the attempts.
	if c.Execution.ServerOverloadPause <= 0 {
		problems = append(problems, "execution.server_overload_pause must be positive")
	}

	approvalValues := []struct {
		name string
		mode domain.ApprovalMode
	}{
		{name: "brief", mode: c.Approvals.Brief},
		{name: "goals", mode: c.Approvals.Goals},
		{name: "designs", mode: c.Approvals.Designs},
		{name: "integration", mode: c.Approvals.Integration},
		{name: "publishing", mode: c.Approvals.Publishing},
	}
	for _, approval := range approvalValues {
		if !approval.mode.Valid() {
			problems = append(problems, fmt.Sprintf("approval %s must be %q or %q", approval.name, domain.ApprovalHuman, domain.ApprovalAutomatic))
		}
	}

	if len(c.Agents) == 0 {
		problems = append(problems, "at least one agent is required")
	}

	agentNames := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	developers := 0
	reviewers := 0
	for _, name := range agentNames {
		agent := c.Agents[name]
		if err := domain.ValidateIdentifier("agent name", name); err != nil {
			problems = append(problems, err.Error())
		}
		// The set of roles is fixed in the harness, so a name outside it is
		// refused here rather than reaching the backend that assembles the
		// invocation: everything downstream — the role's authority, its tool
		// posture, whether it counts as a developer — is derived from the name,
		// and a typo in an agents block would otherwise load and fail only once
		// work had been claimed. The known roles are named because the whole
		// point of the refusal is that the operator can see which one was meant.
		roleKnown := agent.Role.Valid()
		if strings.TrimSpace(string(agent.Role)) == "" {
			problems = append(problems, fmt.Sprintf("agent %q role is required", name))
		} else if !roleKnown {
			problems = append(problems, fmt.Sprintf("agent %q has unknown role %q; roles are %s", name, agent.Role, describeRoles()))
		}
		if !agent.Backend.Valid() {
			problems = append(problems, fmt.Sprintf("agent %q has unsupported backend %q", name, agent.Backend))
		} else if roleKnown && !agent.Backend.SupportsRole(agent.Role) {
			problems = append(problems, fmt.Sprintf("backend %q does not support role %q for agent %q", agent.Backend, agent.Role, name))
		}
		// Every executable agent declares its own selector; the harness never
		// falls back to a provider default nobody chose or recorded.
		if err := validateModelSelector(agent.Model); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
		if agent.Instances < 1 {
			problems = append(problems, fmt.Sprintf("agent %q instances must be at least 1", name))
		}
		problems = append(problems, agent.Persona.problems(name)...)
		if agent.Role == domain.RoleDeveloper {
			developers += agent.Instances
		}
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	if developers == 0 {
		problems = append(problems, "at least one developer agent is required")
	}
	if developers > 0 && c.Execution.MaxConcurrentDevelopers > developers {
		problems = append(problems, "max_concurrent_developers cannot exceed configured developer instances")
	}

	for index, check := range c.Checks {
		if strings.TrimSpace(check) == "" {
			problems = append(problems, fmt.Sprintf("check %d cannot be empty", index))
		}
	}
	if c.Approvals.Integration == domain.ApprovalAutomatic {
		if len(c.Checks) == 0 {
			problems = append(problems, "automatic integration requires at least one check")
		}
		if reviewers == 0 {
			problems = append(problems, "automatic integration requires at least one reviewer agent")
		}
	}

	problems = append(problems, c.Slack.problems()...)

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

// slackChannelPattern keeps a configured channel a channel: an id, or a name
// with or without its leading hash. Anything else names nothing the workspace
// has, and is refused here rather than becoming a posting failure every few
// seconds for as long as the sink runs.
var slackChannelPattern = regexp.MustCompile(`^#?[A-Za-z0-9][A-Za-z0-9._-]*$`)

// slackUserPattern is the shape of a Slack user id. Names are deliberately not
// accepted: a display name is not identity — two people can carry one, and one
// person can change theirs — and an allow-list keyed on something that moves is
// an allow-list that quietly stops matching the person it was written for.
var slackUserPattern = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)

// MaxSlackChannelBytes bounds a configured channel so it stays a channel rather
// than a request smuggled onto the Slack API.
const MaxSlackChannelBytes = 80

// problems reports what makes a Slack section unusable. Everything is checked
// whether or not reporting is enabled, so a project that has configured the
// section and not switched it on yet learns about a typo now rather than on the
// day it turns reporting on.
func (s Slack) problems() []string {
	var problems []string
	channel := strings.TrimSpace(s.Channel)
	switch {
	case channel == "":
		if s.Enabled {
			problems = append(problems, "slack.channel is required when slack is enabled")
		}
	case len(channel) > MaxSlackChannelBytes:
		problems = append(problems, fmt.Sprintf("slack.channel is %d bytes, limit is %d", len(channel), MaxSlackChannelBytes))
	case !slackChannelPattern.MatchString(channel):
		problems = append(problems, fmt.Sprintf("slack.channel %q must be a channel id or name", s.Channel))
	}
	for index, operator := range s.Operators {
		trimmed := strings.TrimSpace(operator)
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("slack.operators[%d] cannot be empty", index))
			continue
		}
		if !slackUserPattern.MatchString(trimmed) {
			problems = append(problems, fmt.Sprintf("slack.operators[%d] %q must be a Slack user id", index, operator))
		}
	}
	return problems
}

// problems reports what makes a declared persona unusable. A persona that
// resolved to nothing is a configuration error rather than a silent fallback:
// an agent whose configured guidance vanished is not the agent that was
// configured.
func (p Persona) problems(agentName string) []string {
	if !p.Defined() {
		return nil
	}
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona version is required", agentName))
	}
	if strings.TrimSpace(p.Path) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona path is required", agentName))
	}
	if strings.TrimSpace(p.Text) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is empty", agentName, p.Path))
	}
	if len(p.Text) > MaxPersonaBytes {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is %d bytes, limit is %d", agentName, p.Path, len(p.Text), MaxPersonaBytes))
	}
	return problems
}

// describeRoles lists the harness's roles the way an operator would read them
// back into the file they mistyped.
func describeRoles() string {
	names := make([]string, 0, len(domain.Roles()))
	for _, role := range domain.Roles() {
		names = append(names, strconv.Quote(string(role)))
	}
	return strings.Join(names, ", ")
}

// validateSpecificationsDirectory keeps the product manager's inputs inside the
// repository. The path is resolved against the repository root, so an absolute
// path or one that climbs out of it names something the repository does not
// contain and is refused rather than read.
func validateSpecificationsDirectory(directory string) error {
	return validateRepositoryDirectory("product specifications", directory)
}

// validateRepositoryDirectory holds one configured directory inside the
// repository. Every such setting names artifacts an agent is shown, so the rule
// is stated once here rather than once per setting: a path that climbs out of
// the repository names text nobody reviewed with the code.
func validateRepositoryDirectory(setting, directory string) error {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return fmt.Errorf("%s directory is required", setting)
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a directory inside the repository", setting, directory)
	}
	return nil
}

// remoteNamePattern keeps a configured remote a plain remote name. The harness
// puts it on a `git push` command line, so anything that could read as an
// option, a path, or a refspec is refused rather than passed along.
var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateRemoteName(remote string) error {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return errors.New("remote is required")
	}
	if !remoteNamePattern.MatchString(trimmed) {
		return fmt.Errorf("remote %q must be a plain Git remote name", remote)
	}
	return nil
}

// MaxModelSelectorBytes bounds a configured selector so it stays a model name
// rather than an argument smuggled onto a provider command line.
const MaxModelSelectorBytes = 128

// ValidateModelSelector reports whether a configured model selector is usable.
// It deliberately accepts both floating family aliases and pinned identifiers,
// and rejects only what cannot name a model.
func ValidateModelSelector(model string) error {
	return validateModelSelector(model)
}

func validateModelSelector(model string) error {
	trimmed := strings.TrimSpace(model)
	switch {
	case trimmed == "":
		return errors.New("model selector is required; there is no implicit harness default")
	case len(trimmed) > MaxModelSelectorBytes:
		return fmt.Errorf("model selector is %d bytes, limit is %d", len(trimmed), MaxModelSelectorBytes)
	case strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 || strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("model selector %q must be a single model name", model)
	}
	return nil
}

type ValidationError struct {
	Problems []string
}

func (e ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}
