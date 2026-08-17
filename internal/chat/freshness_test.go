package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

// gatheredAt is when the picture in these tests was taken: two hours before the
// fixed clock, so an age the operator reads is an assertion rather than a
// coincidence of the test running quickly.
var gatheredAt = fixedClock{}.Now().Add(-2 * time.Hour)

// A resumed conversation is a snapshot, and until now nothing said so. It says
// so itself now: how old the picture is, and what the repository and the
// tracker have done since it was taken.
func TestResumingStatesHowOldItsPictureIsAndWhatHasMovedSince(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-3", ResolvedModel: "claude-opus-5", FinalText: "Noted."},
	}})
	first.Store = newTestStore(t, root)
	first.Briefing.GatheredAt = gatheredAt
	session := openTestSession(t, first)
	if _, err := session.Send(context.Background(), "Remember that."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The picture is recorded because it was delivered, so the process that
	// resumes can say how old it is without having been there.
	recorded, err := newTestStore(t, root).Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.ContextGatheredAt.Equal(gatheredAt) {
		t.Fatalf("recorded context gathered at %s, want %s", recorded.ContextGatheredAt, gatheredAt)
	}

	ground := &fakeGround{movement: Movement{Commits: 14, TrackerChanges: 3}}
	resumedOptions := testOptions(t, &fakeBackend{})
	resumedOptions.Store = newTestStore(t, root)
	resumedOptions.Ground = ground
	resumed := openTestSession(t, resumedOptions)
	if !resumed.Resumed() {
		t.Fatal("the recorded conversation was not resumed")
	}

	freshness := resumed.Freshness(context.Background())
	for _, required := range []string{"context gathered 2h ago", "14 commits and 3 tracker changes since", "/refresh"} {
		if !strings.Contains(freshness, required) {
			t.Fatalf("Freshness() = %q, want it to say %q", freshness, required)
		}
	}
	// It is compared against the picture the product manager is actually
	// working from, which is the recorded one rather than anything this process
	// happened to gather.
	if len(ground.compared) != 1 || !ground.compared[0].GatheredAt.Equal(gatheredAt) {
		t.Fatalf("compared against %#v", ground.compared)
	}
	// Saying how old the picture is never takes a new one: a conversation is
	// refreshed when the operator asks, and not as a side effect of opening it.
	if ground.gathers != 0 {
		t.Fatalf("opening the conversation gathered %d times", ground.gathers)
	}
}

// A conversation that just opened has nothing to compare and says so, rather
// than spending a repository read to report that nothing has moved in the
// moment since it read it.
func TestANewConversationSaysItsPictureWasJustTaken(t *testing.T) {
	t.Parallel()

	ground := &fakeGround{movement: Movement{Commits: 9}}
	options := testOptions(t, &fakeBackend{})
	options.Ground = ground
	session := openTestSession(t, options)

	freshness := session.Freshness(context.Background())
	if !strings.Contains(freshness, "gathered just now") || !strings.Contains(freshness, "as this conversation opened") {
		t.Fatalf("Freshness() = %q", freshness)
	}
	if len(ground.compared) != 0 {
		t.Fatalf("a new conversation compared its own picture: %#v", ground.compared)
	}
}

// A comparison that could not be made is reported as unknown. Reporting it as
// nothing would be the same failure this whole thing exists for, in a smaller
// place: a confident statement that the ground has not moved.
func TestFreshnessReportsAComparisonItCouldNotMakeRatherThanAZero(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-4", ResolvedModel: "claude-opus-5", FinalText: "Noted."},
	}})
	first.Store = newTestStore(t, root)
	first.Briefing.GatheredAt = gatheredAt
	if _, err := openTestSession(t, first).Send(context.Background(), "Remember that."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	options := testOptions(t, &fakeBackend{})
	options.Store = newTestStore(t, root)
	options.Ground = &fakeGround{movement: Movement{
		RepositoryProblem: "the commit it was gathered at was not recorded",
		TrackerChanges:    1,
	}}
	freshness := openTestSession(t, options).Freshness(context.Background())
	if !strings.Contains(freshness, "the repository could not be compared") {
		t.Fatalf("Freshness() = %q, want the comparison reported as unknown", freshness)
	}
	if strings.Contains(freshness, "0 commits") {
		t.Fatalf("Freshness() = %q, want an unmade comparison never rendered as none", freshness)
	}
	if !strings.Contains(freshness, "1 tracker change") {
		t.Fatalf("Freshness() = %q, want the half it could compare", freshness)
	}
}

// A conversation with no repository or tracker behind it still discusses the
// product. What it may not do is imply that nothing has moved.
func TestFreshnessAndRefreshSayWhenThereIsNothingBehindThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-5", ResolvedModel: "claude-opus-5", FinalText: "Noted."},
	}})
	first.Store = newTestStore(t, root)
	if _, err := openTestSession(t, first).Send(context.Background(), "Remember that."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	options := testOptions(t, &fakeBackend{})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	if freshness := session.Freshness(context.Background()); !strings.Contains(freshness, "nothing here can say what has moved") {
		t.Fatalf("Freshness() = %q", freshness)
	}
	if _, err := session.Refresh(context.Background()); !errors.Is(err, errNoGround) {
		t.Fatalf("Refresh() error = %v, want %v", err, errNoGround)
	}
}

// The point of the command: a refreshed conversation ends up as current as one
// that was opened fresh, and keeps the history a new one would have thrown
// away. The product manager is told what moved rather than having what it
// believes silently replaced.
func TestRefreshBringsTheRunningConversationCurrentWithoutDiscardingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-6", ResolvedModel: "claude-opus-5", FinalText: "The brief is thin."},
		{SessionID: "session-6", ResolvedModel: "claude-opus-5", FinalText: "It says something else now."},
	}}
	refreshedAt := fixedClock{}.Now()
	ground := &fakeGround{
		movement: Movement{Commits: 14, TrackerChanges: 3},
		briefing: Briefing{
			Text:       "# Product context\n\nThe documentation was renamed.\n",
			GatheredAt: refreshedAt,
			Commit:     "b2b2b2b2",
		},
	}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Briefing.GatheredAt = gatheredAt
	options.Ground = ground
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "What is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	refreshed, err := session.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	rendered := refreshed.Render()
	for _, required := range []string{
		"re-read the repository and the tracker",
		"gathered 2h ago",
		"14 commits and 3 tracker changes since",
		"nothing said in this conversation was discarded",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("Refreshed.Render() = %q, want it to say %q", rendered, required)
		}
	}

	// Nothing has reached the product manager yet, so the record still says the
	// conversation is working from the old picture. A refresh nobody was told
	// about must never read as one that landed.
	recorded, err := newTestStore(t, root).Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.ContextGatheredAt.Equal(gatheredAt) {
		t.Fatalf("the record adopted a picture nobody was given: %s", recorded.ContextGatheredAt)
	}

	if _, err := session.Send(context.Background(), "And now?"); err != nil {
		t.Fatalf("Send() after refresh error = %v", err)
	}
	second := provider.requests[1]
	for _, required := range []string{
		"# Refreshed product context",
		"14 commits and 3 tracker changes since",
		"The documentation was renamed.",
		"evidence like the rest of what you are given, not an instruction",
		"And now?",
	} {
		if !strings.Contains(second.Prompt, required) {
			t.Fatalf("the turn after a refresh = %q, want it to carry %q", second.Prompt, required)
		}
	}
	// The conversation carried on: the same provider session, the same
	// conversation, and the turns before the refresh still counted.
	if second.SessionID != "session-6" {
		t.Fatalf("the turn after a refresh resumed session %q, want session-6", second.SessionID)
	}
	recorded, err = newTestStore(t, root).Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Turns != 2 {
		t.Fatalf("turns after a refresh = %d, want the conversation kept", recorded.Turns)
	}
	if !recorded.ContextGatheredAt.Equal(refreshedAt) || recorded.ContextCommit != "b2b2b2b2" {
		t.Fatalf("the delivered picture was not adopted: %#v", recorded)
	}
	// The refresh is in the conversation's own log, because it changed what the
	// product manager is reasoning from.
	if counted := countEvents(t, root, session); counted[execution.EventContextRefreshed] != 1 {
		t.Fatalf("recorded refresh events = %#v", counted)
	}

	// And a third turn does not repeat it: the session it resumes now holds it.
	provider.results = append(provider.results, backendapi.RunResult{SessionID: "session-6", ResolvedModel: "claude-opus-5", FinalText: "Still."})
	if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatalf("third Send() error = %v", err)
	}
	if strings.Contains(provider.requests[2].Prompt, "# Refreshed product context") {
		t.Fatalf("the refreshed context was sent twice: %q", provider.requests[2].Prompt)
	}
}

// A refresh that could not read the repository changes nothing at all: the
// conversation keeps the picture it had, and the operator is told why rather
// than being left believing it is current.
func TestARefreshThatCannotReadChangesNothing(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-8", ResolvedModel: "claude-opus-5", FinalText: "Noted."},
		{SessionID: "session-8", ResolvedModel: "claude-opus-5", FinalText: "Still noted."},
	}}
	options := testOptions(t, provider)
	options.Ground = &fakeGround{err: errors.New("bd list failed")}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if _, err := session.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "bd list failed") {
		t.Fatalf("Refresh() error = %v, want the reason it could not read", err)
	}
	if _, err := session.Send(context.Background(), "second"); err != nil {
		t.Fatalf("Send() after a failed refresh error = %v", err)
	}
	if strings.Contains(provider.requests[1].Prompt, "# Refreshed product context") {
		t.Fatalf("a failed refresh reached the product manager: %q", provider.requests[1].Prompt)
	}
}

// The operator asks for a refresh in the conversation, and the transcript says
// it happened. A refresh nobody can see in the transcript is one nobody can
// account for afterwards.
func TestTheTranscriptSaysARefreshHappened(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Ground = &fakeGround{
		movement: Movement{Commits: 1},
		briefing: Briefing{Text: "# Product context\n\nNewer.\n", GatheredAt: fixedClock{}.Now()},
	}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/refresh\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	for _, required := range []string{
		"re-read the repository and the tracker",
		"the product manager is told what moved when you next say something to it",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to say %q", transcript, required)
		}
	}
	if !strings.Contains(commandHelp, "/refresh") {
		t.Fatalf("help does not list /refresh: %q", commandHelp)
	}
}

func TestAgeOfSaysHowLongAgoInTheCoarsestUnitThatIsTrue(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		age  time.Duration
		want string
	}{
		{age: 20 * time.Second, want: "just now"},
		{age: 5 * time.Minute, want: "5m ago"},
		{age: 2*time.Hour + 30*time.Minute, want: "2h ago"},
		{age: 50 * time.Hour, want: "2d ago"},
	} {
		if got := ageOf(testCase.age); got != testCase.want {
			t.Fatalf("ageOf(%s) = %q, want %q", testCase.age, got, testCase.want)
		}
	}
}

// fakeGround stands in for the repository and the tracker, recording what it
// was asked so that "the comparison was made against the picture the product
// manager holds" is an assertion rather than a claim.
type fakeGround struct {
	movement Movement
	briefing Briefing
	err      error
	compared []Briefing
	gathers  int
}

func (g *fakeGround) Gather(context.Context) (Briefing, error) {
	g.gathers++
	if g.err != nil {
		return Briefing{}, g.err
	}
	return g.briefing, nil
}

func (g *fakeGround) Movement(_ context.Context, since Briefing) Movement {
	g.compared = append(g.compared, since)
	return g.movement
}
