package sidestream

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The ordinary life of a side thread: it is opened beside a main conversation,
// asked one question, answers it, and concludes — and concluding is what hands
// its substance to the merge, because a thread that ended without merging is one
// whose whole substance is on a disk nothing reads.
func TestASideThreadIsOpenedAskedAndConcludedIntoTheMerge(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{`The intake hold covers work the harness chose and not work the operator named.

` + Fence + `
{"side":{"concluded":true,"commitments":["say so in the next brief"]}}
` + "```" + `
`}}
	merge := &fakeMerge{}
	runner := testRunner(store, voice, merge)

	answer, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if answer.Prose != "The intake hold covers work the harness chose and not work the operator named." {
		t.Fatalf("Put().Prose = %q, want the prose with the block taken out of it", answer.Prose)
	}
	if want := []string{"say so in the next brief"}; !reflect.DeepEqual(answer.Commitments, want) {
		t.Fatalf("Put().Commitments = %v, want %v", answer.Commitments, want)
	}
	if !answer.Tentative() {
		t.Fatal("an answer that promised something reports itself as settled; the surface carrying it has to say it is tentative")
	}

	// The merge got the concluded thread itself, with what it worked out and what
	// it drafted, so the memory write cites the stream rather than restating it.
	if merge.calls != 1 {
		t.Fatalf("the merge was reached %d time(s), want once", merge.calls)
	}
	if merge.stream.ID != answer.Stream.ID {
		t.Fatalf("the merge was handed %s and the thread is %s", merge.stream.ID, answer.Stream.ID)
	}
	if merge.substance != answer.Prose {
		t.Fatalf("the merge was handed substance %q, want the thread's own answer", merge.substance)
	}
	if !reflect.DeepEqual(merge.commitments, answer.Commitments) {
		t.Fatalf("the merge was handed commitments %v, want %v", merge.commitments, answer.Commitments)
	}
	if merge.outcome != OutcomeConcluded {
		t.Fatalf("the merge was handed outcome %q, want %q", merge.outcome, OutcomeConcluded)
	}
	if answer.Stream.Open() {
		t.Fatal("the thread is still open after the merge concluded it")
	}
	if answer.Stream.Turns != 1 || answer.Stream.CostUSD != 0.25 {
		t.Fatalf("the concluded thread recorded %d turn(s) costing %v, want one costing 0.25", answer.Stream.Turns, answer.Stream.CostUSD)
	}
	// What served the turn is on the record, because a record naming a provider
	// session and nothing that outlives it is a thread nobody can reconstruct.
	if answer.Stream.Backend != domain.BackendClaudeCode || answer.Stream.ProviderModel != "opus" ||
		answer.Stream.ProviderResolvedModel != "claude-opus-5" || answer.Stream.AccountAlias != "research" ||
		answer.Stream.ConfigRevision != "cfg-01" || answer.Stream.Build != "build-01" ||
		answer.Stream.ProviderSessionID != "session-1" {
		t.Fatalf("the thread records %#v, want the invocation pinned to what served it", answer.Stream)
	}
}

// A thread that has not finished stays open and takes another turn, against the
// same stream rather than a second one.
func TestASideThreadKeptOpenTakesAnotherTurn(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{
		"I need to read the two items before I can say.",
		"Neither of them contradicts the hold.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}}\n" + "```" + "\n",
	}}
	merge := &fakeMerge{}
	runner := testRunner(store, voice, merge)

	first, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !first.Stream.Open() || first.Stream.Turns != 1 {
		t.Fatalf("after one unfinished turn the thread is open=%v with %d turn(s), want open with one", first.Stream.Open(), first.Stream.Turns)
	}
	if merge.calls != 0 {
		t.Fatal("a thread that has not concluded was merged; the merge is what concluding does")
	}

	second, err := runner.Put(context.Background(), Ask{Stream: first.Stream.ID, Question: "and the other one?"})
	if err != nil {
		t.Fatalf("Put() continuing error = %v", err)
	}
	if second.Stream.ID != first.Stream.ID {
		t.Fatalf("continuing opened %s beside %s rather than continuing it", second.Stream.ID, first.Stream.ID)
	}
	if second.Stream.Turns != 2 {
		t.Fatalf("the continued thread recorded %d turn(s), want two", second.Stream.Turns)
	}
	if second.Stream.Open() || merge.calls != 1 {
		t.Fatalf("the second turn concluded the thread open=%v with %d merge(s), want closed and merged once", second.Stream.Open(), merge.calls)
	}
	// The second turn continued the provider session the first opened rather than
	// starting a thread the record could not reconstruct.
	if voice.asked[1].SessionID != "session-1" {
		t.Fatalf("the second turn was taken in session %q, want the one the first turn opened", voice.asked[1].SessionID)
	}
	if voice.asked[1].Turn != 2 || voice.asked[1].MaxTurns != first.Stream.MaxTurns {
		t.Fatalf("the second turn was told it was turn %d of %d, want 2 of %d", voice.asked[1].Turn, voice.asked[1].MaxTurns, first.Stream.MaxTurns)
	}
}

// A thread that spends its last turn without saying it has finished ends anyway,
// and what it had reached still merges: a thread cut off half way is legible as
// one rather than as a thread that said nothing.
func TestASideThreadThatSpendsItsLastTurnStillMerges(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{"As far as I got: the hold is read before the item is chosen."}}
	merge := &fakeMerge{}
	runner := testRunner(store, voice, merge)
	runner.MaxTurns = 1

	answer, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if answer.Stream.Open() {
		t.Fatal("a thread with no turns left is still open")
	}
	if merge.outcome != OutcomeSpent {
		t.Fatalf("the merge was handed outcome %q, want %q", merge.outcome, OutcomeSpent)
	}
	if merge.substance != answer.Prose {
		t.Fatalf("the merge was handed %q, want what the thread had reached", merge.substance)
	}

	// And a thread left open past its cap by a process that died is refused rather
	// than asked again, because the turn it would take was already spent.
	spent := answer.Stream
	spent.Outcome = ""
	spent.ClosedAt = nil
	store.streams[spent.ID] = spent
	_, err = runner.Put(context.Background(), Ask{Stream: spent.ID, Question: "anything more?"})
	if !errors.Is(err, ErrTurnsSpent) {
		t.Fatalf("Put() past the cap error = %v, want ErrTurnsSpent", err)
	}
}

// The turn is counted before the provider is invoked, so a process that dies
// between the two has spent a turn it did not take rather than taken one it did
// not count. A turn the provider failed is recorded exactly as one that answered.
func TestATurnTheProviderFailedIsStillSpentAndStillPinned(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{failWith: errors.New("the provider gave up")}
	merge := &fakeMerge{}
	runner := testRunner(store, voice, merge)

	answer, err := runner.Put(context.Background(), testAsk())
	if err == nil || !strings.Contains(err.Error(), "the provider gave up") {
		t.Fatalf("Put() error = %v, want the provider's failure", err)
	}
	recorded := store.streams[answer.Stream.ID]
	if recorded.Turns != 1 {
		t.Fatalf("a failed turn recorded %d turn(s), want one: a cap a crash could reset is not a cap", recorded.Turns)
	}
	if recorded.CostUSD != 0.25 || recorded.Backend != domain.BackendClaudeCode {
		t.Fatalf("a failed turn recorded cost %v on backend %q, want what the invocation actually cost and what served it", recorded.CostUSD, recorded.Backend)
	}
	if !recorded.Open() || merge.calls != 0 {
		t.Fatal("a failed turn concluded the thread; nothing was worked out to merge")
	}
}

// A reply asking for anything at all is refused whole. This is where "it takes
// no action" stops being a description: the block decides nothing, the turn is
// spent, and whoever asked is told its question went unanswered.
func TestAReplyAskingForAnActionIsRefusedWhole(t *testing.T) {
	t.Parallel()

	for _, refused := range []string{"yoyodyne-ask", "yoyodyne-report", "yoyodyne-amendment", "yoyodyne-tracker"} {
		t.Run(refused, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			voice := &fakeVoice{answers: []string{"Here is what I would do.\n\n```" + refused + "\n{}\n" + "```" + "\n"}}
			merge := &fakeMerge{}
			runner := testRunner(store, voice, merge)

			answer, err := runner.Put(context.Background(), testAsk())
			if err == nil || !strings.Contains(err.Error(), "takes no action") {
				t.Fatalf("Put() error = %v, want the block refused", err)
			}
			if merge.calls != 0 {
				t.Fatal("a refused reply reached the merge; nothing it asked for happens anywhere")
			}
			if store.streams[answer.Stream.ID].Turns != 1 {
				t.Fatal("a refused reply cost no turn; the invocation was made and charged for either way")
			}
		})
	}
}

// The turn is taken under the side stream's own lease, named for its own
// identifier. That is the whole of the concurrency answer: the main thread's
// lease is not asked for here, so holding it stops nothing and this stops
// nothing of it.
func TestASideTurnIsTakenUnderTheStreamsOwnLease(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	leases := &fakeLeases{}
	runner := testRunner(store, &fakeVoice{answers: []string{"Yes.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}}\n" + "```" + "\n"}}, &fakeMerge{})
	runner.Leases = leases

	answer, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if want := []string{answer.Stream.ID}; !reflect.DeepEqual(leases.held, want) {
		t.Fatalf("the turn held %v, want %v — a side thread never takes the main thread's lease", leases.held, want)
	}
	if leases.outstanding != 0 {
		t.Fatalf("%d lease(s) were never given back", leases.outstanding)
	}

	// A stream a live process is carrying is refused rather than written over.
	leases.refuse = true
	if _, err := runner.Put(context.Background(), Ask{Stream: answer.Stream.ID, Question: "again?"}); err == nil ||
		!strings.Contains(err.Error(), "one at a time") {
		t.Fatalf("Put() on a carried stream error = %v, want it refused", err)
	}
}

// The exclusion and the merge are the two guarantees this package holds, and a
// runner missing either refuses rather than working without it: one held only
// where somebody remembered to wire it is not held.
func TestARunnerMissingItsExclusionTakesNoTurn(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{"Yes."}}
	runner := testRunner(store, voice, &fakeMerge{})
	runner.Leases = nil

	if _, err := runner.Put(context.Background(), testAsk()); err == nil ||
		!strings.Contains(err.Error(), "would exclude nothing") {
		t.Fatalf("Put() with no leases error = %v, want it refused", err)
	}
	if len(voice.asked) != 0 {
		t.Fatal("a turn was taken with nothing excluding a second process from it")
	}
	if len(store.streams) != 0 {
		t.Fatal("a stream was opened for a turn that could not be taken")
	}
}

// Concluding is the merge, so a runner with no merge wired refuses rather than
// closing a thread whose substance would then reach nobody.
func TestAThreadWhoseSubstanceWouldReachNobodyIsNotClosedQuietly(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{"Settled.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}}\n" + "```" + "\n"}}
	runner := testRunner(store, voice, nil)

	answer, err := runner.Put(context.Background(), testAsk())
	if err == nil || !strings.Contains(err.Error(), "no merge is wired") {
		t.Fatalf("Put() with no merge error = %v, want it refused", err)
	}
	if !answer.Stream.Open() {
		t.Fatal("the thread was closed with nothing to carry its substance")
	}
	if answer.Prose != "Settled." {
		t.Fatalf("Put().Prose = %q, want what the thread said; a merge nobody wired does not cost the answer", answer.Prose)
	}
}

// Conclude ends a thread the runner did not end itself — the one a dead process
// left open with its turns spent — through the same merge and under the same
// lease.
func TestConcludeEndsAThreadTheRunnerDidNotEnd(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	leases := &fakeLeases{}
	merge := &fakeMerge{}
	runner := testRunner(store, &fakeVoice{answers: []string{"Half of it."}}, merge)
	runner.Leases = leases
	runner.MaxTurns = 2

	answer, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	concluded, err := runner.Conclude(context.Background(), answer.Stream.ID, "nobody came back for the rest", nil, OutcomeSpent)
	if err != nil {
		t.Fatalf("Conclude() error = %v", err)
	}
	if concluded.Open() || merge.substance != "nobody came back for the rest" || merge.outcome != OutcomeSpent {
		t.Fatalf("Conclude() left %#v with substance %q, want the thread ended as spent through the merge", concluded, merge.substance)
	}
	if _, err := runner.Conclude(context.Background(), answer.Stream.ID, "again", nil, OutcomeSpent); err == nil ||
		!strings.Contains(err.Error(), "already ended") {
		t.Fatalf("Conclude() twice error = %v, want the second refused", err)
	}
	if _, err := runner.Conclude(context.Background(), answer.Stream.ID, "x", nil, "abandoned"); err == nil ||
		!strings.Contains(err.Error(), "not one a side stream ends with") {
		t.Fatalf("Conclude() with an invented outcome error = %v, want it refused", err)
	}
}

// A thread that ended cannot be continued, and the per-agent bound the store
// enforces is reported as the ordinary answer at a busy moment rather than as a
// fault.
func TestAnEndedThreadAndABusyAgentAreBothRefusedPlainly(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	voice := &fakeVoice{answers: []string{"Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}}\n" + "```" + "\n"}}
	runner := testRunner(store, voice, &fakeMerge{})

	answer, err := runner.Put(context.Background(), testAsk())
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := runner.Put(context.Background(), Ask{Stream: answer.Stream.ID, Question: "more?"}); err == nil ||
		!strings.Contains(err.Error(), "cannot be continued") {
		t.Fatalf("Put() on an ended thread error = %v, want it refused", err)
	}

	store.refuseOpen = fmt.Errorf("product-manager is %w: 3 of 3", ErrTooManyStreams)
	if _, err := runner.Put(context.Background(), testAsk()); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("Put() past the per-agent bound error = %v, want ErrTooManyStreams", err)
	}
}

func TestAskValidateRejectsIncoherentQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ask  Ask
		want string
	}{
		{"no question", Ask{Agent: "product-manager", Role: domain.RoleProductManager, Conversation: testConversation, Topic: "a topic"}, "question is required"},
		{"no agent", Ask{Role: domain.RoleProductManager, Conversation: testConversation, Topic: "a topic", Question: "why?"}, "agent"},
		{"unknown role", Ask{Agent: "product-manager", Role: "auditor", Conversation: testConversation, Topic: "a topic", Question: "why?"}, "is not one of the harness's roles"},
		{"no main thread", Ask{Agent: "product-manager", Role: domain.RoleProductManager, Topic: "a topic", Question: "why?"}, "does not name a main thread"},
		{"a stream is not a main thread", Ask{Agent: "product-manager", Role: domain.RoleProductManager, Conversation: "side-0123456789abcdef0123456789abcdef", Topic: "a topic", Question: "why?"}, "does not name a main thread"},
		{"no topic", Ask{Agent: "product-manager", Role: domain.RoleProductManager, Conversation: testConversation, Question: "why?"}, "topic is required"},
		{"a stream that is not one", Ask{Stream: "chat-0123456789abcdef0123456789abcdef", Question: "why?"}, "is invalid"},
		{"continuing and redirecting", Ask{Stream: "side-0123456789abcdef0123456789abcdef", Role: domain.RoleArchitect, Question: "why?"}, "names the stream alone"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.ask.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want it to name %q", err, test.want)
			}
		})
	}
}

const testConversation = "chat-0123456789abcdef0123456789abcdef"

func testAsk() Ask {
	return Ask{
		Agent:        "product-manager",
		Role:         domain.RoleProductManager,
		Conversation: testConversation,
		Topic:        "whether the intake hold covers work an operator named",
		Question:     "does the intake hold cover work the operator named?",
	}
}

func testRunner(store *fakeStore, voice Voice, merge *fakeMerge) Runner {
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	runner := Runner{
		Store: store,
		// The lease is required, so every runner here has one: a turn that excluded
		// nothing is refused rather than taken.
		Leases:    &fakeLeases{},
		Voice:     voice,
		ProductID: "yoyodyne",
		Now: func() time.Time {
			at = at.Add(time.Second)
			return at
		},
	}
	// A nil interface value is not a nil interface, and a runner handed a typed
	// nil merge would take the wired path into it.
	if merge != nil {
		merge.store = store
		runner.Merge = merge
	}
	return runner
}

// fakeStore is the durable record in memory. It validates what it is handed, so
// a test that recorded something a real store would refuse fails here rather
// than passing on a record nothing could load.
type fakeStore struct {
	streams    map[string]Stream
	events     []execution.Event
	refuseOpen error
}

func newFakeStore() *fakeStore { return &fakeStore{streams: map[string]Stream{}} }

func (s *fakeStore) Open(stream Stream, bound int) error {
	if s.refuseOpen != nil {
		return s.refuseOpen
	}
	if err := stream.Validate(); err != nil {
		return err
	}
	if bound < 1 {
		return errors.New("a side stream bound of nothing")
	}
	if _, already := s.streams[stream.ID]; already {
		return fmt.Errorf("side stream %s is already recorded", stream.ID)
	}
	s.streams[stream.ID] = stream
	return nil
}

func (s *fakeStore) Save(stream Stream) error {
	if err := stream.Validate(); err != nil {
		return err
	}
	s.streams[stream.ID] = stream
	return nil
}

func (s *fakeStore) Load(id string) (Stream, error) {
	stream, recorded := s.streams[id]
	if !recorded {
		return Stream{}, fmt.Errorf("%w: %s", ErrNoStream, id)
	}
	return stream, nil
}

func (s *fakeStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

// fakeVoice answers in order, recording what it was asked.
type fakeVoice struct {
	answers  []string
	failWith error
	asked    []Question
}

func (v *fakeVoice) Answer(_ context.Context, question Question) (Spoken, error) {
	v.asked = append(v.asked, question)
	// What served the invocation travels back whether or not there was an answer,
	// because it is a fact about the invocation rather than about what came back.
	spoken := Spoken{
		SessionID:      "session-1",
		CostUSD:        0.25,
		Backend:        domain.BackendClaudeCode,
		Model:          "opus",
		ResolvedModel:  "claude-opus-5",
		AccountAlias:   "research",
		ConfigRevision: "cfg-01",
		Build:          "build-01",
	}
	if v.failWith != nil {
		return spoken, v.failWith
	}
	at := len(v.asked) - 1
	if at >= len(v.answers) {
		return spoken, fmt.Errorf("the voice was asked %d time(s) and has %d answer(s)", len(v.asked), len(v.answers))
	}
	spoken.Answer = v.answers[at]
	return spoken, nil
}

// fakeMerge stands in for the memory write, recording what concluding handed it
// and doing to the stream's own record what the merge does: stamping the outcome
// and the moment, and saving it as ended.
type fakeMerge struct {
	store       *fakeStore
	calls       int
	stream      Stream
	substance   string
	commitments []string
	outcome     Outcome
}

func (m *fakeMerge) Conclude(_ context.Context, stream Stream, substance string, commitments []string, outcome Outcome, at time.Time) (Stream, error) {
	m.calls++
	m.stream = stream
	m.substance = substance
	m.commitments = commitments
	m.outcome = outcome
	stream.Outcome = outcome
	closed := at
	stream.ClosedAt = &closed
	stream.UpdatedAt = at
	if m.store != nil {
		if err := m.store.Save(stream); err != nil {
			return Stream{}, err
		}
	}
	return stream, nil
}

// fakeLeases records which identifiers a turn asked to hold, which is how a test
// says the main thread's lease was never among them.
type fakeLeases struct {
	held        []string
	outstanding int
	refuse      bool
}

func (l *fakeLeases) Hold(id string) (Release, bool, error) {
	if l.refuse {
		return nil, false, nil
	}
	l.held = append(l.held, id)
	l.outstanding++
	return releaseFunc(func() error { l.outstanding--; return nil }), true, nil
}

type releaseFunc func() error

func (r releaseFunc) Release() error { return r() }
