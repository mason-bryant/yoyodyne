package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Recording a directive that pauses work is an enforcement rather than a note,
// and this is what proves the difference: the directive is durable where every
// run reads it, the work in flight is named and told why, and what that work
// leaves behind is settled the same way stopping settles it. None of it cancels
// anything.
func TestRecordingAPausingDirectiveNamesTheWorkItStopsAndSettlesWhatItLeaves(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	work := &fakeWork{
		survey: Survey{
			InFlight: []RunSnapshot{{RunID: "run-1", WorkItemID: "yoyodyne-1", Status: "running", StartedAt: fixedClock{}.Now()}},
			Claimed:  []WorkItemSummary{{ID: "yoyodyne-2", Title: "Also claimed", Status: "in_progress"}},
		},
		settlements: []Settlement{{RunID: "run-1", WorkItemID: "yoyodyne-1", Action: "resumable"}},
	}
	session := directiveSession(t, directives, work)

	recorded, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind:       directive.KindArtifact,
		Artifact:   "docs/product/goals/v1-goals.md",
		Text:       "the autonomy goal is being rewritten",
		Unresolved: "what the goal says about human approval",
	})
	if err != nil {
		t.Fatalf("RecordDirective() error = %v", err)
	}
	if !recorded.Directive.Pauses() {
		t.Fatalf("directive = %#v, want one that pauses work", recorded.Directive)
	}
	// The work already under way is what the directive has to reach; work that has
	// not been pulled yet is stopped by the record itself when it is.
	if got := strings.Join(recorded.Paused, ","); got != "yoyodyne-1,yoyodyne-2" {
		t.Fatalf("paused = %q, want the run in flight and the claimed item", got)
	}
	notes := work.takenNotes()
	if len(notes) != 2 {
		t.Fatalf("notes = %#v, want the pause written on both paused items", notes)
	}
	for _, note := range notes {
		// The pause names the directive and what is unresolved, because those are
		// the two things somebody needs in order to lift it.
		for _, wanted := range []string{recorded.Directive.ID, "what the goal says about human approval", "Nothing was cancelled"} {
			if !strings.Contains(note[1], wanted) {
				t.Fatalf("note on %s = %q, want it to mention %q", note[0], note[1], wanted)
			}
		}
	}
	if work.settleCount() != 1 {
		t.Fatalf("settles = %d, want what the paused work leaves behind settled once", work.settleCount())
	}
	if work.wasCancelled() {
		t.Fatal("recording a directive cancelled a run; it must pause rather than cancel")
	}
	if len(recorded.Settlements) != 1 || recorded.Settlements[0].Action != "resumable" {
		t.Fatalf("settlements = %#v, want the paused run reported as resumable", recorded.Settlements)
	}
	if len(recorded.Problems) != 0 {
		t.Fatalf("problems = %#v, want none", recorded.Problems)
	}
}

// An operational directive is in effect immediately and stops nothing, so
// recording one must not reach into work at all.
func TestRecordingAnOperationalDirectiveDisturbsNoWork(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	work := &fakeWork{survey: Survey{
		InFlight: []RunSnapshot{{RunID: "run-1", WorkItemID: "yoyodyne-1", Status: "running", StartedAt: fixedClock{}.Now()}},
	}}
	session := directiveSession(t, directives, work)

	recorded, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind: directive.KindOperational,
		Text: "prefer smaller pull requests",
	})
	if err != nil {
		t.Fatalf("RecordDirective() error = %v", err)
	}
	if len(recorded.Paused) != 0 || work.settleCount() != 0 || len(work.takenNotes()) != 0 {
		t.Fatalf("an operational directive disturbed work: paused=%#v settles=%d notes=%#v",
			recorded.Paused, work.settleCount(), work.takenNotes())
	}
}

// A scoped directive pauses what it names, whether or not that work is moving
// yet: an operator who knows which items a change of mind touches says so, and
// the pause applies to the items rather than to whatever happens to be running.
func TestAScopedDirectivePausesTheItemsItNames(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	work := &fakeWork{survey: Survey{
		InFlight: []RunSnapshot{{RunID: "run-1", WorkItemID: "yoyodyne-9", Status: "running", StartedAt: fixedClock{}.Now()}},
	}}
	session := directiveSession(t, directives, work)

	recorded, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind:       directive.KindAmbiguous,
		Text:       "do the publishing one differently",
		Unresolved: "which of the two publishing behaviours was meant",
		Scope:      []string{"yoyodyne-1"},
	})
	if err != nil {
		t.Fatalf("RecordDirective() error = %v", err)
	}
	if len(recorded.Paused) != 1 || recorded.Paused[0] != "yoyodyne-1" {
		t.Fatalf("paused = %#v, want only the item the directive named", recorded.Paused)
	}
}

// The pause is enforced from the durable record, not from the note on the item,
// so a tracker that would not take the note leaves the work paused all the same.
// Reporting it any other way would tell an operator the pause did not happen.
func TestAPauseHoldsEvenWhenTheTrackerRefusesTheNote(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	work := &fakeWork{
		survey:    Survey{Claimed: []WorkItemSummary{{ID: "yoyodyne-1", Status: "in_progress"}}},
		directErr: errors.New("the tracker is unreachable"),
	}
	session := directiveSession(t, directives, work)

	recorded, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind:       directive.KindAmbiguous,
		Text:       "hold this one",
		Unresolved: "what was meant",
	})
	if err != nil {
		t.Fatalf("RecordDirective() error = %v", err)
	}
	if len(directives.recorded) != 1 {
		t.Fatalf("recorded = %#v, want the directive durable regardless of the tracker", directives.recorded)
	}
	if len(recorded.Paused) != 1 || len(recorded.Noted) != 0 {
		t.Fatalf("paused = %#v noted = %#v, want the item paused and the note reported as missing", recorded.Paused, recorded.Noted)
	}
	rendered := recorded.Render()
	if !strings.Contains(rendered, "still paused") {
		t.Fatalf("render = %q, want it to say the item is paused even though the note failed", rendered)
	}
	if len(recorded.Problems) == 0 {
		t.Fatal("problems = none, want the refused note reported")
	}
}

// A directive that pauses work has to say what it is waiting for. Recording one
// that cannot is refused before anything durable is written, because a pause
// nobody can name a reason for is one nobody can lift.
func TestAPausingDirectiveWithNothingUnresolvedIsRefused(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	session := directiveSession(t, directives, &fakeWork{})

	if _, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind: directive.KindAmbiguous,
		Text: "do it the other way",
	}); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("RecordDirective() error = %v, want a refusal naming what is missing", err)
	}
	if len(directives.recorded) != 0 {
		t.Fatalf("recorded = %#v, want nothing written for a refused directive", directives.recorded)
	}
}

// Settling a directive is what releases the work it paused, and the release is
// the durable record changing rather than anything done to a run here.
func TestResolvingADirectiveSettlesItWhereEveryRunReadsIt(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	session := directiveSession(t, directives, &fakeWork{})
	recorded, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind:       directive.KindAmbiguous,
		Text:       "hold this one",
		Unresolved: "what was meant",
	})
	if err != nil {
		t.Fatalf("RecordDirective() error = %v", err)
	}

	resolved, err := session.ResolveDirective(context.Background(), recorded.Directive.ID, "the second reading was meant")
	if err != nil {
		t.Fatalf("ResolveDirective() error = %v", err)
	}
	if !resolved.Directive.Resolved() || resolved.Directive.Pauses() {
		t.Fatalf("resolved = %#v, want a settled directive that pauses nothing", resolved.Directive)
	}
	if _, err := session.ResolveDirective(context.Background(), recorded.Directive.ID, ""); err == nil {
		t.Fatal("ResolveDirective() with no resolution error = nil, want a refusal")
	}
}

// The kind decides whether work stops, so the operator states it. Inferring it
// would mean a classifier deciding to pause every run, which is a worse failure
// than one that pauses nothing.
func TestTheDirectiveCommandTakesItsKindFromTheOperatorRatherThanGuessing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		argument string
		want     DirectiveRequest
		wantErr  string
	}{
		{
			name:     "anything unqualified is operational and stops nothing",
			argument: "stop opening pull requests for docs",
			want:     DirectiveRequest{Kind: directive.KindOperational, Text: "stop opening pull requests for docs"},
		},
		{
			name:     "an ambiguous directive carries what is unresolved",
			argument: "ambiguous which publishing behaviour | do publishing differently",
			want: DirectiveRequest{
				Kind:       directive.KindAmbiguous,
				Text:       "do publishing differently",
				Unresolved: "which publishing behaviour",
			},
		},
		{
			name:     "an artifact directive names the artifact as well",
			argument: "artifact docs/product/brief.md whether autonomy is still the goal | the brief is changing",
			want: DirectiveRequest{
				Kind:       directive.KindArtifact,
				Artifact:   "docs/product/brief.md",
				Text:       "the brief is changing",
				Unresolved: "whether autonomy is still the goal",
			},
		},
		{
			name:     "an ambiguous directive with nothing unresolved is refused",
			argument: "ambiguous do it the other way",
			wantErr:  "what is unresolved",
		},
		{
			name:    "an empty directive is refused",
			wantErr: "say what the directive is",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request, err := parseDirectiveCommand(test.argument)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseDirectiveCommand() error = %v, want it to mention %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDirectiveCommand() error = %v", err)
			}
			if request.Kind != test.want.Kind || request.Text != test.want.Text ||
				request.Unresolved != test.want.Unresolved || request.Artifact != test.want.Artifact {
				t.Fatalf("request = %#v, want %#v", request, test.want)
			}
		})
	}
}

// A conversation with no durable directive record says so rather than appearing
// to enforce something. Recording a directive that reaches nothing is the exact
// failure this was built to end.
func TestAConversationWithNoDirectiveRecordSaysSo(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	session := openTestSession(t, options)
	if _, err := session.RecordDirective(context.Background(), DirectiveRequest{
		Kind: directive.KindOperational, Text: "anything",
	}); !errors.Is(err, errNoDirectives) {
		t.Fatalf("RecordDirective() error = %v, want errNoDirectives", err)
	}
	if _, err := session.ReadDirectives(context.Background()); !errors.Is(err, errNoDirectives) {
		t.Fatalf("ReadDirectives() error = %v, want errNoDirectives", err)
	}
}

// The operator gives and settles a directive without leaving the conversation
// they are already in, which is the only path most directives will ever take.
func TestAnOperatorRecordsAndSettlesADirectiveFromTheConversation(t *testing.T) {
	t.Parallel()

	directives := &fakeDirectives{}
	work := &fakeWork{survey: Survey{
		InFlight: []RunSnapshot{{RunID: "run-1", WorkItemID: "yoyodyne-1", Status: "running", StartedAt: fixedClock{}.Now()}},
	}}
	session := directiveSession(t, directives, work)

	var out strings.Builder
	input := strings.NewReader(strings.Join([]string{
		"/directive ambiguous which publishing behaviour | do publishing differently",
		"/directives",
		"/exit",
		"",
	}, "\n"))
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if len(directives.recorded) != 1 {
		t.Fatalf("recorded = %#v, want the one directive the operator gave", directives.recorded)
	}
	recorded := directives.recorded[0]
	for _, want := range []string{recorded.ID, "which publishing behaviour", "paused 1 work item(s): yoyodyne-1", "unresolved"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript = %q, want it to mention %q", transcript, want)
		}
	}

	var settled strings.Builder
	resolve := strings.NewReader(strings.Join([]string{
		"/resolve " + recorded.ID + " the second behaviour was meant",
		"/exit",
		"",
	}, "\n"))
	if err := session.Converse(context.Background(), testConsole(resolve, &settled)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !strings.Contains(settled.String(), "can carry on") {
		t.Fatalf("transcript = %q, want the settled directive to say the work is released", settled.String())
	}
	if !directives.recorded[0].Resolved() {
		t.Fatalf("directive = %#v, want it settled", directives.recorded[0])
	}
}

func directiveSession(t *testing.T, directives Directives, work Work) *Session {
	t.Helper()
	options := testOptions(t, &fakeBackend{})
	options.Directives = directives
	options.Work = work
	return openTestSession(t, options)
}

// fakeDirectives is the durable directive record as a conversation writes it,
// stamping what the harness knows exactly as the real one does.
type fakeDirectives struct {
	mu       sync.Mutex
	recorded []directive.Directive
}

func (f *fakeDirectives) Record(_ context.Context, request DirectiveRequest) (directive.Directive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, err := directive.NewID()
	if err != nil {
		return directive.Directive{}, err
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Kind:          request.Kind,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
		Text:          request.Text,
		Artifact:      request.Artifact,
		Unresolved:    request.Unresolved,
		Scope:         request.Scope,
	}
	if err := recorded.Validate(); err != nil {
		return directive.Directive{}, err
	}
	f.recorded = append(f.recorded, recorded)
	return recorded, nil
}

func (f *fakeDirectives) List(context.Context) ([]directive.Directive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]directive.Directive(nil), f.recorded...), nil
}

func (f *fakeDirectives) Resolve(_ context.Context, reference, resolution string) (directive.Directive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index, candidate := range f.recorded {
		if !strings.HasPrefix(candidate.ID, reference) {
			continue
		}
		resolved, err := candidate.Resolve(resolution, time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC))
		if err != nil {
			return directive.Directive{}, err
		}
		f.recorded[index] = resolved
		return resolved, nil
	}
	return directive.Directive{}, errors.New("no directive is recorded under that reference")
}

var _ Directives = (*fakeDirectives)(nil)
