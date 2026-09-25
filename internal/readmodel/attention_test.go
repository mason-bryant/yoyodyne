package readmodel

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// proposedChange is one undecided proposal, as the amendment store records it.
func proposedChange() amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            "amendment-0123456789abcdef0123456789abcdef",
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-9a8b7c6d5e4f30211203f4e5d6c7b8a9",
		WorkItemID:    "yoyodyne-ifd.402",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "observability-and-dashboard",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "The design should say the dashboard reads the attention entries as records rather than as sentences.",
		Why:           "A page cannot open a card on a sentence, and the read model now carries the record.",
		RaisedAt:      moment.Add(-3 * time.Hour),
	}
}

// attentionOfEveryKind is one entry per kind, built the way the reading builds
// them, with the sentence each is expected to print beside it. It is the
// fixture every test below reads, and a kind added to the vocabulary without an
// entry here fails the coverage check.
func attentionOfEveryKind(t *testing.T) map[AttentionKind]struct {
	entry Attention
	what  string
} {
	t.Helper()
	brake := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "yoyodyne", HeldAt: moment.Add(-2 * time.Hour),
		HeldBy: runstate.IntakeHolderBrake,
		Brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.300", Reason: "checks failed"}},
			CooldownEndsAt: moment.Add(time.Hour),
		},
	}
	stall := Stall{Reason: ReasonSessionIdle, Says: "a watch session is alive and has found nothing it can start", Since: moment.Add(-time.Hour)}
	pile := report.Pile{Collected: 12, Unhandled: 3, Oldest: moment.Add(-9 * 24 * time.Hour), OldestAge: 9 * 24 * time.Hour, Worst: report.SeverityWarning}
	child := runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildDegraded, Reason: "died 6 times in 10 minutes"}
	owed := runstate.State{RunID: "run-owed", WorkItemID: "yoyodyne-ifd.410", Status: runstate.StatusFailed, Phase: runstate.PhaseCleaningUp}
	published := runstate.State{
		RunID: "run-queued", WorkItemID: "yoyodyne-ifd.411", Branch: "yoyodyne/item/queued",
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 567, URL: "https://forge.example/pr/567", MergeQueued: true},
	}
	outage := runstate.ProviderOutage{Cause: domain.ProviderUnauthenticated, Provider: domain.BackendClaudeCode, AccountAlias: "default", Since: moment.Add(-time.Hour), LastSeen: moment}
	paused := directive.Directive{ID: "directive-4f2c", Kind: directive.KindAmbiguous, Unresolved: "which branch does this land on?", ReceivedAt: moment.Add(-time.Hour)}
	capacity := CapacityHold{Holding: true, Since: moment.Add(-time.Hour), ResetsAt: moment.Add(time.Hour)}
	capacityEntry, _ := capacity.Attention()
	stallEntry, _ := stall.Waiting()

	return map[AttentionKind]struct {
		entry Attention
		what  string
	}{
		AttentionAmendment: {amendmentAttention(proposedChange()),
			"a change to observability-and-dashboard is proposed and undecided (amendment-0123456789abcdef0123456789abcdef)"},
		AttentionCarriedItem: {carriedItemAttention("yoyodyne-ifd.212", domain.ConversationWith(domain.RoleArchitect)),
			`yoyodyne-ifd.212 is admitted for "conversation:architect" rather than a developer run`},
		AttentionReports: {reportsAttention(pile),
			"3 of 12 collected report(s) are unhandled, the oldest filed 9d ago, worst warning"},
		AttentionOwedStep: {owedStepAttention(owed),
			"run run-owed of yoyodyne-ifd.410 ended still owing a step"},
		AttentionPublication: {awaitingForgeAttention(published),
			"run run-queued promoted yoyodyne-ifd.411 into main and the forge has not published it: pull request #567 https://forge.example/pr/567"},
		AttentionDegradedService: {degradedServiceAttention(child),
			"the scheduler service is degraded: died 6 times in 10 minutes"},
		AttentionHold: {intakeHoldAttention(brake),
			"intake is held, since 2026-08-30T10:00:00Z: " + singleLine(brake.Account(), maxRefusalBytes)},
		AttentionDirective: {directiveAttention(paused),
			"directive directive-4f2c is unresolved: which branch does this land on?"},
		AttentionOutage: {outageAttention(outage), outage.Says()},
		AttentionStall: {stallEntry,
			"a watch session is alive and has found nothing it can start, since 2026-08-30T11:00:00Z"},
		AttentionHeldWork: {heldWorkAttention(HeldAwaitingCarryOut, 2),
			"2 admitted items await carry-out of a decision already recorded"},
		// The capacity hold is the third switch under the hold kind; it is
		// checked with the rest below, and named here so the map is one per kind.
		"": {capacityEntry,
			"every role is held by the provider's usage window, since 2026-08-30T11:00:00Z, until 2026-08-30T13:00:00Z"},
	}
}

// Every kind carries its kind, a mover from the vocabulary, the identifier of
// the record it is about where there is one, and the record itself — and prints
// the sentence derived from them, opening with the mover's own possessive so a
// surface counting by mover and a reader of the line agree on who has to act.
func TestEveryAttentionKindCarriesItsRecordAndDerivesItsSentence(t *testing.T) {
	t.Parallel()
	fixtures := attentionOfEveryKind(t)
	for _, kind := range AttentionKinds() {
		if _, covered := fixtures[kind]; !covered {
			t.Fatalf("kind %q has no fixture here, so its shape is unpinned", kind)
		}
	}
	for kind, fixture := range fixtures {
		entry := fixture.entry
		if kind != "" && entry.Kind != kind {
			t.Errorf("%s: kind = %q", kind, entry.Kind)
		}
		if !entry.Mover.Valid() {
			t.Errorf("%s: mover %q is outside the vocabulary", kind, entry.Mover)
		}
		if got := entry.What(); got != fixture.what {
			t.Errorf("%s: what = %q, want %q", kind, got, fixture.what)
		}
		if whose := entry.Whose(); !strings.HasPrefix(whose, entry.Mover.Possessive()+" — ") {
			t.Errorf("%s: whose = %q does not open with the mover's possessive %q", kind, whose, entry.Mover.Possessive())
		}
		// Exactly one record is carried, the one the kind names. The carried
		// item is the exception: its record is the marker and the item id, both
		// values rather than records of their own.
		carried, want := 0, 1
		value := reflect.ValueOf(entry)
		for index := 0; index < value.NumField(); index++ {
			if value.Field(index).Kind() == reflect.Pointer && !value.Field(index).IsNil() {
				carried++
			}
		}
		if entry.Kind == AttentionCarriedItem {
			want = 0
			if entry.Executor == "" || entry.WorkItemID == "" {
				t.Errorf("%s: %+v, want the marker and the item carried", kind, entry)
			}
		}
		if carried != want {
			t.Errorf("%s: %d records carried, want %d: %+v", kind, carried, want, entry)
		}
	}

	// The identifiers, one kind at a time: each names the record a surface would
	// open or act on.
	for kind, want := range map[AttentionKind]string{
		AttentionAmendment:       "amendment-0123456789abcdef0123456789abcdef",
		AttentionCarriedItem:     "yoyodyne-ifd.212",
		AttentionOwedStep:        "run-owed",
		AttentionPublication:     "run-queued",
		AttentionDegradedService: "scheduler",
		AttentionHold:            HoldIntake,
		AttentionDirective:       "directive-4f2c",
		AttentionOutage:          string(domain.ProviderUnauthenticated),
		AttentionStall:           string(ReasonSessionIdle),
		AttentionReports:         "",
		AttentionHeldWork:        "",
	} {
		if got := fixtures[kind].entry.ID; got != want {
			t.Errorf("%s: id = %q, want %q", kind, got, want)
		}
	}
	if capacity := fixtures[""].entry; capacity.Kind != AttentionHold || capacity.ID != HoldCapacity || capacity.CapacityHold == nil {
		t.Errorf("capacity hold = %+v, want the hold kind, the capacity switch, and the hold carried", capacity)
	}
}

// An amendment entry carries the proposal whole: the target document and what
// sort of document it is, who proposed it and from where, the change, and why —
// none of it cut to a line, because the card that shows it and the act that
// decides it both need the thing rather than a sentence about it.
func TestAnAmendmentEntryCarriesTheProposalInFull(t *testing.T) {
	t.Parallel()
	proposal := proposedChange()
	entry := amendmentAttention(proposal)
	if entry.Amendment == nil || !reflect.DeepEqual(*entry.Amendment, proposal) {
		t.Fatalf("amendment = %+v, want the proposal carried whole", entry.Amendment)
	}
	if entry.Mover != MoverArchitect || entry.WorkItemID != "yoyodyne-ifd.402" {
		t.Fatalf("entry = %+v, want the owner as mover and the proposer's item", entry)
	}
	if !strings.HasPrefix(entry.Whose(), "the architect's — ") {
		t.Fatalf("whose = %q, want the owner named", entry.Whose())
	}
	// A document the product manager owns waits on her, in the words a reader
	// uses for her rather than the role's identifier.
	proposal.Owner = domain.RoleProductManager
	if whose := amendmentAttention(proposal).Whose(); !strings.HasPrefix(whose, "the product manager's — ") {
		t.Fatalf("whose = %q, want the product manager named in plain words", whose)
	}

	// Through the reading itself, from the store's records.
	sources := quietSources()
	sources.Amendments = fakeAmendments{records: []amendment.Record{{Proposal: &proposal}}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 || standing.NeedsHuman[0].Kind != AttentionAmendment || standing.NeedsHuman[0].Amendment == nil {
		t.Fatalf("needs a human = %+v, want the one undecided proposal carried whole", standing.NeedsHuman)
	}
	if got := standing.NeedsHuman[0].Amendment; got.Change != proposal.Change || got.Why != proposal.Why || got.Artifact != proposal.Artifact {
		t.Fatalf("amendment = %+v, want the change, the reason, and the document in full", got)
	}
}

// The intake hold's mover is read off the same record its sentence is worded
// from, in every state the brake's record can be in, so the two cannot name
// different people.
func TestTheIntakeHoldsMoverMatchesTheHoldsOwnWording(t *testing.T) {
	t.Parallel()
	ended := moment
	blocked := []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.300", Reason: "checks failed"}}
	for name, hold := range map[string]runstate.IntakeHold{
		"the operator's own": {HeldAt: moment, HeldBy: runstate.IntakeHolderOperator},
		"a brake hold from before the brake worked its own": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake},
		"undecided":          {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, CooldownEndsAt: moment.Add(time.Hour)}},
		"released":           {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionRelease}},
		"probe decided":      {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionProbe}},
		"probing":            {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Probe: &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.300", StartedAt: moment}}},
		"probed and blocked": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, CooldownEndsAt: moment.Add(time.Hour), Probe: &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.300", StartedAt: moment, EndedAt: &ended, Blocked: true}}},
		"escalated":          {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionEscalate}},
		// The harness's own escalation at the bound on its loop, and the one state
		// where a decision of hers stands on top of it: a hold it escalated is
		// still hers to release, and the mover has to follow the release rather
		// than the escalation, because the release is what the session acts on.
		"escalated by the harness": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{
			Blocked: blocked, Cycles: 4, CycleBound: 4,
			Escalation: &runstate.BrakeEscalation{At: moment, Cycles: 4, Probe: "yoyodyne-ifd.300", Reason: "checks failed"},
		}},
		"escalated by the harness and released by her": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{
			Blocked: blocked, Cycles: 4, CycleBound: 4, Decision: runstate.BrakeDecisionRelease,
			Escalation: &runstate.BrakeEscalation{At: moment, Cycles: 4, Probe: "yoyodyne-ifd.300", Reason: "checks failed"},
		}},
	} {
		entry := intakeHoldAttention(hold)
		if entry.Whose() != hold.Whose() {
			t.Errorf("%s: whose = %q, want the hold's own %q", name, entry.Whose(), hold.Whose())
		}
		if !strings.HasPrefix(entry.Whose(), entry.Mover.Possessive()+" — ") {
			t.Errorf("%s: mover %q does not open the hold's wording %q", name, entry.Mover, entry.Whose())
		}
	}
}

// The four movers of an unpublished promotion are read off the record's own
// fields, in the order the sentence reads them.
func TestAPublicationEntryNamesItsMoverFromTheRecord(t *testing.T) {
	t.Parallel()
	dropped := runstate.MergeDrop{At: moment, Reason: "the remote target moved"}
	for name, want := range map[string]struct {
		state runstate.State
		mover Mover
	}{
		"unrecorded":            {runstate.State{RunID: "run-1", Branch: "b"}, MoverHarness},
		"queued":                {runstate.State{RunID: "run-2", PullRequest: &runstate.PullRequest{Number: 1, MergeQueued: true}}, MoverForge},
		"re-armed after a drop": {runstate.State{RunID: "run-3", PullRequest: &runstate.PullRequest{Number: 1, MergeQueued: true}, MergeDrop: &dropped}, MoverForge},
		"dropped":               {runstate.State{RunID: "run-4", PullRequest: &runstate.PullRequest{Number: 1}, MergeDrop: &dropped}, MoverDevelopmentManager},
		"unasked":               {runstate.State{RunID: "run-5", PullRequest: &runstate.PullRequest{Number: 1}}, MoverOperator},
	} {
		entry := awaitingForgeAttention(want.state)
		if entry.Mover != want.mover {
			t.Errorf("%s: mover = %q, want %q", name, entry.Mover, want.mover)
		}
		if !strings.HasPrefix(entry.Whose(), want.mover.Possessive()+" — ") || !strings.Contains(entry.Whose(), "yoyo reconcile") {
			t.Errorf("%s: whose = %q, want %s and the sweep that settles it", name, entry.Whose(), want.mover.Possessive())
		}
		if entry.Publication == nil || (want.state.MergeDrop != nil) != (entry.Publication.MergeDrop != nil) {
			t.Errorf("%s: publication = %+v, want the drop carried exactly where the record has one", name, entry.Publication)
		}
	}
	// A record with no integration leaves the target field empty and says so
	// in the sentence: a placeholder is a sentence, and a field a surface acts
	// on carries none.
	unrecorded := awaitingForgeAttention(runstate.State{RunID: "run-1", WorkItemID: "item-1", Branch: "b"})
	if unrecorded.Publication.TargetBranch != "" || !strings.Contains(unrecorded.What(), "into an unrecorded target") {
		t.Fatalf("publication = %+v, what = %q; want an empty target and the sentence saying so", unrecorded.Publication, unrecorded.What())
	}
	if encoded := string(mustMarshal(t, unrecorded)); strings.Contains(encoded, `"target_branch"`) {
		t.Fatalf("JSON = %s, want no target field where the record holds none", encoded)
	}
}

// The JSON carries the fields and, beside them, the two sentences computed
// from the fields; reading it back yields the same entry, and a document whose
// sentence disagrees with its fields is refused rather than believed.
func TestAttentionJSONCarriesTheRecordAndTheDerivedSentences(t *testing.T) {
	t.Parallel()
	for kind, fixture := range attentionOfEveryKind(t) {
		encoded, err := json.Marshal(fixture.entry)
		if err != nil {
			t.Fatalf("%s: marshal: %v", kind, err)
		}
		var wire map[string]any
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if wire["what"] != fixture.entry.What() || wire["whose"] != fixture.entry.Whose() || wire["kind"] != string(fixture.entry.Kind) || wire["mover"] != string(fixture.entry.Mover) {
			t.Errorf("%s: JSON = %s, want the kind, the mover, and both sentences", kind, encoded)
		}
		var decoded Attention
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%s: unmarshal: %v", kind, err)
		}
		if decoded.What() != fixture.entry.What() || decoded.Whose() != fixture.entry.Whose() || decoded.Kind != fixture.entry.Kind || decoded.ID != fixture.entry.ID {
			t.Errorf("%s: round trip = %+v, want %+v", kind, decoded, fixture.entry)
		}
	}

	// A sentence the fields do not derive is refused; a document that leaves
	// the sentences out is read from its fields alone.
	entry := directiveAttention(directive.Directive{ID: "directive-1", Unresolved: "which?"})
	var decoded Attention
	if err := json.Unmarshal([]byte(`{"kind":"directive","id":"directive-1","mover":"operator","directive":{"schema_version":0,"id":"directive-1","product_id":"","kind":"","received_by":"","received_at":"0001-01-01T00:00:00Z","text":"","unresolved":"which?"}}`), &decoded); err != nil {
		t.Fatalf("a document without the sentences: %v", err)
	}
	if decoded.What() != entry.What() {
		t.Fatalf("what = %q, want %q derived from the fields", decoded.What(), entry.What())
	}
	disagreeing := strings.Replace(string(mustMarshal(t, entry)), `"what":"directive directive-1 is unresolved: which?"`, `"what":"directive-2 is unresolved: which?"`, 1)
	if err := json.Unmarshal([]byte(disagreeing), &decoded); err == nil || !strings.Contains(err.Error(), "disagrees with its record") {
		t.Fatalf("a sentence disagreeing with its fields was accepted: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"kind":"directive","mover":"operator","surprise":1}`), &decoded); err == nil {
		t.Fatal("a field the model does not carry was accepted")
	}
	// A kind or a mover outside its vocabulary is refused rather than printed
	// as nobody's move in particular.
	for name, document := range map[string]string{
		"an unknown kind":  `{"kind":"surprise","id":"x","mover":"operator"}`,
		"a missing kind":   `{"id":"x","mover":"operator"}`,
		"an unknown mover": `{"kind":"directive","id":"x","mover":"somebody"}`,
		"a missing mover":  `{"kind":"directive","id":"x"}`,
	} {
		if err := json.Unmarshal([]byte(document), &decoded); err == nil || !strings.Contains(err.Error(), "is not one of") {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// The mover vocabulary is closed and every member has a possessive a sentence
// can open with.
func TestEveryMoverHasAPossessive(t *testing.T) {
	t.Parallel()
	for _, mover := range Movers() {
		if possessive := mover.Possessive(); !strings.HasSuffix(possessive, "'s") && mover != MoverUnnamed {
			t.Errorf("%q: possessive = %q", mover, possessive)
		}
	}
	if MoverOf(domain.RoleDevelopmentManager) != MoverDevelopmentManager || MoverOf("") != MoverUnnamed || MoverOf("nobody-in-particular") != MoverUnnamed {
		t.Fatal("MoverOf does not fold roles onto the vocabulary")
	}
	if carriedItemAttention("item-1", domain.WorkItemExecutor("conversation")).Whose() != "the role it names — in conversation; no run will ever be started for it" {
		t.Fatal("a bare conversation marker does not name the unnamed role")
	}
}

// A lane report's blocker names what it waits on in this vocabulary. The durable
// schema keeps its own copy of the tokens a blocker may name, because this
// package reads that record rather than the other way round, so every one of
// them is held here to being a mover this vocabulary has.
func TestEveryMoverALaneReportMayNameIsAMover(t *testing.T) {
	t.Parallel()
	for _, token := range runstate.LaneReportMovers() {
		if !Mover(token).Valid() {
			t.Errorf("a lane report may wait on %q, which is not a mover the read model has", token)
		}
	}
}
