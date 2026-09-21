package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// moverNamedBy reads the mover off the possessive an attention entry's prose
// opens with — "the operator's — ...", "the development manager's, in
// conversation — ...". It is the reading a surface would have had to make of
// the prose, done here once so the typed mover can be held to the words beside
// it: an entry whose value says the harness and whose words say the operator
// is the disagreement the value exists to rule out.
func moverNamedBy(t *testing.T, whose string) Mover {
	t.Helper()
	opening, _, found := strings.Cut(whose, " — ")
	if !found || !strings.HasPrefix(opening, "the ") {
		t.Fatalf("whose = %q does not open with whose move it is", whose)
	}
	named := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(opening, "the "), ", in conversation"), "'s")
	switch strings.ReplaceAll(named, "-", " ") {
	case "operator":
		return MoverOperator
	case "architect":
		return MoverArchitect
	case "development manager":
		return MoverDevelopmentManager
	case "product manager":
		return MoverProductManager
	case "harness":
		return MoverHarness
	case "forge":
		return MoverForge
	case "role it names", "developer", "reviewer":
		return MoverUnnamed
	default:
		t.Fatalf("whose = %q opens on a mover the vocabulary does not name", whose)
		return ""
	}
}

// Every entry the standing puts on the attention line carries its mover as a
// value, and the value is the one its own words open with. The reading here
// is a mix — the operator's hold, an unresolved directive, proposals for two
// roles, a run that owes a step, held work on both sides of a decision, a
// stale pile, and items handed to a conversation with and without a role —
// because a surface counting by mover is only right if every producer sets
// one, and a producer that left it empty would be counted nowhere.
func TestEveryAttentionEntryCarriesTheMoverItsWordsName(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	raised := moment.Add(-time.Hour)
	sources := quietSources()
	sources.OperatorHolds = fakeOperatorHolds{hold: runstate.OperatorHold{HeldAt: moment.Add(-time.Hour)}, held: true}
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID: "dir-1", Kind: directive.KindAmbiguous, Text: "which branch", Unresolved: "which branch", Scope: []string{"yoyodyne-ifd.150"},
	}}}
	sources.Amendments = fakeAmendments{records: []amendment.Record{
		{Proposal: &amendment.Proposal{ID: "amendment-1", Artifact: "v1-harness-design", Kind: artifact.KindDesign, Owner: domain.RoleArchitect, RaisedAt: raised}},
		{Proposal: &amendment.Proposal{ID: "amendment-2", Artifact: "brief", Kind: artifact.KindBrief, Owner: domain.RoleProductManager, RaisedAt: raised}},
		{Proposal: &amendment.Proposal{ID: "amendment-3", Artifact: "v1-harness-design", Kind: artifact.KindDesign, Owner: domain.RoleArchitect, RaisedAt: raised}},
	}}
	sources.Runs = fakeRuns{
		outstanding: []runstate.State{{RunID: "run-o", WorkItemID: "yoyodyne-ifd.160"}},
		prices:      map[string]runstate.ItemPrice{},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"blocked": {
				{ID: "yoyodyne-ifd.150", Title: "Decided days ago", Status: "blocked"},
				{ID: "yoyodyne-ifd.151", Title: "Nobody has decided", Status: "blocked"},
			},
			"open": {
				{ID: "yoyodyne-ifd.212", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)},
				{ID: "yoyodyne-ifd.213", Status: "open", Executor: domain.WorkItemExecutorConversation},
			},
		},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.212"}, {ID: "yoyodyne-ifd.213"}},
	}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{
		heldRun("run-a", "yoyodyne-ifd.150", stopped),
		heldRun("run-b", "yoyodyne-ifd.151", stopped),
	}}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.150": {Decisions: []runstate.TriageDecision{{Decision: runstate.TriageDecisionRerun, RunID: "run-a"}}},
	}
	sources.Reports = fakeReports{reports: []report.Report{
		filedReport("report-1", report.SeverityWarning, moment.Add(-8*24*time.Hour)),
	}}

	standing := ReadStanding(context.Background(), sources)
	if standing.NeedsHumanProblem != "" {
		t.Fatalf("needs a human problem = %q, want every source read", standing.NeedsHumanProblem)
	}
	counted := map[Mover]int{}
	for _, attention := range standing.NeedsHuman {
		if attention.Mover == "" {
			t.Errorf("%q carries no mover, so a surface counting by mover would count it nowhere", attention.What)
			continue
		}
		if named := moverNamedBy(t, attention.Whose); named != attention.Mover {
			t.Errorf("%q says its mover is %q and its words say %q", attention.What, attention.Mover, named)
		}
		counted[attention.Mover]++
	}
	want := map[Mover]int{
		// The hold, the directive, and the run that owes a step.
		MoverOperator: 3,
		// Two proposals and the handed-off item that names the architect.
		MoverArchitect: 3,
		// The item nobody has decided about.
		MoverDevelopmentManager: 1,
		// The proposal on the brief, and the pile nobody is working.
		MoverProductManager: 2,
		// The decision recorded and not carried out.
		MoverHarness: 1,
		// The item handed to a conversation that names no role.
		MoverUnnamed: 1,
	}
	for _, mover := range Movers() {
		if counted[mover] != want[mover] {
			t.Errorf("%d entries wait on %s, want %d: %+v", counted[mover], mover, want[mover], standing.NeedsHuman)
		}
	}
	if len(standing.NeedsHuman) != 11 {
		t.Errorf("needs a human = %d entries, want 11: %+v", len(standing.NeedsHuman), standing.NeedsHuman)
	}
}

// The producers the standing reaches only under one condition each — a held
// intake in each of the brake's states, a promotion the forge holds in each
// of its, the capacity hold, a stalled session, a degraded service — carry
// the mover their words name too.
func TestTheConditionalProducersCarryTheMoverTheirWordsName(t *testing.T) {
	t.Parallel()

	trippedAt := moment.Add(-20 * time.Minute)
	brake := func(revise func(*runstate.IntakeBrake)) runstate.IntakeHold {
		trip := runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{RunID: "run-1", WorkItemID: "yoyodyne-ifd.398", Reason: "review required repair"}},
			CooldownEndsAt: trippedAt.Add(30 * time.Minute),
		}
		revise(&trip)
		return runstate.IntakeHold{HeldAt: trippedAt, HeldBy: runstate.IntakeHolderBrake, Brake: &trip}
	}
	for name, hold := range map[string]runstate.IntakeHold{
		"the operator's own": {HeldAt: trippedAt, HeldBy: runstate.IntakeHolderOperator},
		"deciding":           brake(func(*runstate.IntakeBrake) {}),
		"released":           brake(func(b *runstate.IntakeBrake) { b.Decision = runstate.BrakeDecisionRelease }),
		"probe decided":      brake(func(b *runstate.IntakeBrake) { b.Decision = runstate.BrakeDecisionProbe }),
		"probing": brake(func(b *runstate.IntakeBrake) {
			b.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.398", StartedAt: moment}
		}),
		"escalated": brake(func(b *runstate.IntakeBrake) { b.Decision = runstate.BrakeDecisionEscalate }),
	} {
		if got, named := intakeMover(hold), moverNamedBy(t, hold.Whose()); got != named {
			t.Errorf("intake hold %s: mover %q, and its words say %q", name, got, named)
		}
	}

	promoted := runstate.State{RunID: "run-p", WorkItemID: "yoyodyne-ifd.400", Branch: "yoyodyne/yoyodyne-ifd-400/run-p"}
	queued, dropped, unmerged := promoted, promoted, promoted
	queued.PullRequest = &runstate.PullRequest{Number: 1, URL: "https://forge/pull/1", MergeQueued: true}
	dropped.PullRequest = &runstate.PullRequest{Number: 2, URL: "https://forge/pull/2"}
	dropped.MergeDrop = &runstate.MergeDrop{}
	unmerged.PullRequest = &runstate.PullRequest{Number: 3, URL: "https://forge/pull/3"}

	hold := CapacityHold{Holding: true, Since: moment.Add(-time.Hour)}
	capacity, _ := hold.Attention()
	idle, _ := Stall{Reason: ReasonSessionIdle, Says: "the session is choosing nothing"}.Waiting()
	stoppedSession, _ := Stall{Reason: ReasonNoWatchSession, Says: "no session is watching"}.Waiting()
	away, _ := Stall{Reason: ReasonProviderAway, Says: "the provider is answering nobody"}.Waiting()
	services := &Services{Recorded: true, Record: runstate.Supervision{Children: []runstate.SupervisedChild{{
		Service: "slack", State: runstate.ChildDegraded, Reason: "crashed 3 times in a minute",
	}}}}
	handed := HandedOff(backlog.Queue{Entries: []backlog.Entry{
		{ID: "yoyodyne-ifd.1", Executor: domain.ConversationWith(domain.RoleProductManager)},
		{ID: "yoyodyne-ifd.2", Executor: domain.ConversationWith(domain.RoleDevelopmentManager)},
		{ID: "yoyodyne-ifd.3", Executor: domain.ConversationWith(domain.RoleDeveloper)},
	}})

	entries := []Attention{
		awaitingForgeAttention(promoted),
		awaitingForgeAttention(queued),
		awaitingForgeAttention(dropped),
		awaitingForgeAttention(unmerged),
		capacity, idle, stoppedSession, away,
	}
	entries = append(entries, services.Attention()...)
	entries = append(entries, Held(2, 3)...)
	entries = append(entries, handed...)
	want := []Mover{
		MoverHarness, MoverForge, MoverDevelopmentManager, MoverOperator,
		MoverOperator, MoverOperator, MoverOperator, MoverOperator,
		MoverOperator,
		MoverDevelopmentManager, MoverHarness,
		MoverProductManager, MoverDevelopmentManager, MoverUnnamed,
	}
	if len(entries) != len(want) {
		t.Fatalf("%d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, attention := range entries {
		if attention.Mover != want[i] {
			t.Errorf("entry %d %q: mover %q, want %q", i, attention.What, attention.Mover, want[i])
		}
		if named := moverNamedBy(t, attention.Whose); named != attention.Mover {
			t.Errorf("entry %d %q: mover %q, and its words say %q", i, attention.What, attention.Mover, named)
		}
	}
}

// The vocabulary is the operator first and then the roles, and a role that
// holds no conversation of its own is unnamed rather than guessed at.
func TestTheMoverVocabularyPutsTheOperatorFirst(t *testing.T) {
	t.Parallel()
	if movers := Movers(); movers[0] != MoverOperator || len(movers) != 7 {
		t.Fatalf("movers = %v, want the operator first among seven", movers)
	}
	for role, want := range map[domain.AgentRole]Mover{
		domain.RoleArchitect:          MoverArchitect,
		domain.RoleDevelopmentManager: MoverDevelopmentManager,
		domain.RoleProductManager:     MoverProductManager,
		domain.RoleDeveloper:          MoverUnnamed,
		domain.RoleReviewer:           MoverUnnamed,
		"":                            MoverUnnamed,
	} {
		if got := MoverOfRole(role); got != want {
			t.Errorf("MoverOfRole(%q) = %q, want %q", role, got, want)
		}
	}
}
