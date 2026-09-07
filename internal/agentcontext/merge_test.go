package agentcontext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// The merge is a memory write and carries everything the memory machinery asks
// of one: the invocation that produced it, the records it was drawn from, and a
// sequence the store assigned.
func TestAMergeLandsAsAnAuditedMemoryRevision(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	recorded, err := testConclusion().Merge(context.Background(), store)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if recorded.Sequence != 1 {
		t.Errorf("Recorded is revision %d, want the first", recorded.Sequence)
	}
	if recorded.Memory != testStreamID {
		t.Errorf("the memory is called %q, want the side stream it came from", recorded.Memory)
	}
	if recorded.Continuity != runstate.MemoryContinuitySubject {
		t.Errorf("continuity = %q, want the merge filed against its own topic", recorded.Continuity)
	}
	// The audit is the side thread's own invocations, pinned exactly as
	// durable-state-is-provider-independent requires: naming the main thread's turn
	// would say a turn wrote something no turn wrote.
	invocation := recorded.Invocation
	if invocation.Kind != runstate.MemoryInvocationSideStream || invocation.ID != testStreamID {
		t.Errorf("the invocation is %s %s, want the side stream", invocation.Kind, invocation.ID)
	}
	if invocation.Turn != 3 || invocation.Backend != "claude-code" || invocation.Model != "opus" {
		t.Errorf("the invocation is %+v, want the turns and the provider the stream recorded", invocation)
	}
	if invocation.ResolvedModel != "claude-opus-5-20260514" || invocation.AccountAlias != "research" {
		t.Errorf("the invocation is %+v, want what actually served the side thread", invocation)
	}
	// It references the stream rather than copying it, and says which main thread
	// it belongs to, which is what makes it findable from either end.
	want := []runstate.MemorySource{
		{Kind: runstate.MemorySourceSideStream, ID: testStreamID},
		{Kind: runstate.MemorySourceConversation, ID: testConversationID},
	}
	if len(recorded.Sources) != len(want) {
		t.Fatalf("Sources = %v, want %v", recorded.Sources, want)
	}
	for index, source := range want {
		if recorded.Sources[index] != source {
			t.Errorf("Sources[%d] = %v, want %v", index, recorded.Sources[index], source)
		}
	}

	// And it is on the disk, under the agent whose thread it was, as one revision
	// of one memory rather than as a pile of dialogue.
	memories, problems, err := store.Live("product-manager")
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Live() reported %v", problems)
	}
	if len(memories) != 1 || len(memories[0].Revisions) != 1 {
		t.Fatalf("Live() returned %d memories, want the one merge", len(memories))
	}
}

// What reaches the main thread is an account of the side thread with the stream
// named, and every commitment marked as the draft it is. A merge that read as a
// decision already taken is the one thing this must not produce: the design puts
// ratification on the main thread's own path.
func TestAMergeSaysWhereItCameFromAndKeepsItsCommitmentsTentative(t *testing.T) {
	t.Parallel()

	revision, err := testConclusion().Revision()
	if err != nil {
		t.Fatalf("Revision() error = %v", err)
	}
	for _, required := range []string{
		testStreamID,
		"whether the intake hold covers work an operator named",
		"nothing it decided has happened",
		"not its transcript",
		"tentatively",
		"Ratify or adjust each of these here",
		"tell the operator the hold does not cover work they named",
	} {
		if !strings.Contains(revision.Text, required) {
			t.Errorf("the merged text does not say %q:\n%s", required, revision.Text)
		}
	}
	if revision.Subject != "whether the intake hold covers work an operator named" {
		t.Errorf("Subject = %q, want the stream's topic", revision.Subject)
	}
}

// A thread cut off at its budget merges what it had reached, and says it was cut
// off. Losing it would be the pathological case the bound exists to make cheap
// turning into one that costs the whole thread.
func TestASpentThreadMergesAndSaysItWasSpent(t *testing.T) {
	t.Parallel()

	conclusion := testConclusion()
	conclusion.Stream.Outcome = sidestream.OutcomeSpent
	revision, err := conclusion.Revision()
	if err != nil {
		t.Fatalf("Revision() error = %v", err)
	}
	if !strings.Contains(revision.Text, "stopped when it reached its budget") {
		t.Errorf("a spent thread does not say so:\n%s", revision.Text)
	}
}

// The store redacts what an agent authored before it reaches the disk, and the
// merge is subject to it like every other write, because it goes through the same
// door rather than beside it.
func TestAMergeIsRedactedByTheStore(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewMemoryStore(t.TempDir(), "yoyodyne", "sk-live-secret")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	conclusion := testConclusion()
	conclusion.Substance = "the operator's key sk-live-secret was in the evidence"
	recorded, err := conclusion.Merge(context.Background(), store)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if strings.Contains(recorded.Text, "sk-live-secret") {
		t.Errorf("the merge carried a redacted value onto the disk:\n%s", recorded.Text)
	}
}

// A role that may not write its own memory may not write one through a side
// thread either. The merge adds composition and no authority, which is what
// stops the per-agent knob from being a way to widen anybody.
func TestAMergeByARoleThatKeepsNoMemoryIsRefused(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	conclusion := testConclusion()
	conclusion.Stream.Agent = "reviewer"
	conclusion.Stream.Role = domain.RoleReviewer
	if _, err := conclusion.Merge(context.Background(), store); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Merge() error = %v, want ErrUnauthorized", err)
	}
	memories, _, err := store.Memories("reviewer")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("a refused merge left %d memories behind", len(memories))
	}
}

// Merging is what concluding does, so a thread still being held has nothing
// settled to write. Merging one would put a half-finished answer into the main
// thread's context as though the side thread had stood behind it.
func TestAnOpenStreamHasNothingToMerge(t *testing.T) {
	t.Parallel()

	conclusion := testConclusion()
	conclusion.Stream.Outcome = ""
	conclusion.Stream.ClosedAt = nil
	if _, err := conclusion.Merge(context.Background(), newStore(t)); !errors.Is(err, ErrNotConcluded) {
		t.Fatalf("Merge() error = %v, want ErrNotConcluded", err)
	}
}

// The bounds are the merge's own, so a refusal names the part that was too long
// rather than the assembled revision the store measured.
func TestAMergeIsBoundedBeforeItIsComposed(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*Conclusion){
		"nothing concluded": func(c *Conclusion) { c.Substance = "  " },
		"no turn taken":     func(c *Conclusion) { c.Stream.Turns = 0 },
		"a runaway account": func(c *Conclusion) { c.Substance = strings.Repeat("x", MaxSubstanceBytes+1) },
		"too many promises": func(c *Conclusion) {
			for range MaxMergedCommitments + 1 {
				c.Commitments = append(c.Commitments, "one more thing it would do")
			}
		},
		"a promise too long": func(c *Conclusion) { c.Commitments = []string{strings.Repeat("x", MaxCommitmentBytes+1)} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			conclusion := testConclusion()
			mutate(&conclusion)
			if _, err := conclusion.Merge(context.Background(), newStore(t)); err == nil {
				t.Fatalf("Merge() accepted %s", name)
			}
		})
	}
}

// The widest merge this composes still fits one revision's budget, so a merge is
// never refused for the size of a sentence this package wrote. Without this the
// bounds above and the store's could drift apart, and the failure would be a
// thread's whole substance lost to framing.
func TestTheWidestMergeFitsARevision(t *testing.T) {
	t.Parallel()

	conclusion := testConclusion()
	conclusion.Stream.Topic = strings.Repeat("t", sidestream.MaxTopicBytes)
	conclusion.Substance = strings.Repeat("s", MaxSubstanceBytes)
	conclusion.Commitments = nil
	for range MaxMergedCommitments {
		conclusion.Commitments = append(conclusion.Commitments, strings.Repeat("c", MaxCommitmentBytes))
	}
	revision, err := conclusion.Revision()
	if err != nil {
		t.Fatalf("Revision() error = %v", err)
	}
	if len(revision.Text) > runstate.MaxMemoryTextBytes {
		t.Errorf("the widest merge is %d bytes and one revision holds %d", len(revision.Text), runstate.MaxMemoryTextBytes)
	}
	// The sequence is the store's, so a revision composed here is unnumbered until
	// it is written; everything else about it has to stand on its own.
	revision.Sequence = 1
	if err := revision.Validate(); err != nil {
		t.Errorf("the widest merge is not a valid revision: %v", err)
	}
}

// A topic longer than a subject may be is cut rather than refused, and cut on a
// character boundary: the subject is a label an operator reads, and a merge lost
// over the length of its label would be the thread's substance thrown away.
func TestALongTopicIsCutRatherThanLosingTheMerge(t *testing.T) {
	t.Parallel()

	conclusion := testConclusion()
	conclusion.Stream.Topic = strings.Repeat("é", sidestream.MaxTopicBytes/2)
	revision, err := conclusion.Revision()
	if err != nil {
		t.Fatalf("Revision() error = %v", err)
	}
	if len(revision.Subject) > runstate.MaxMemorySubjectBytes {
		t.Errorf("Subject is %d bytes, limit is %d", len(revision.Subject), runstate.MaxMemorySubjectBytes)
	}
	if !strings.HasSuffix(revision.Subject, "...") {
		t.Errorf("Subject = %q, want a cut one to say it was cut", revision.Subject)
	}
	if !utf8.ValidString(revision.Subject) {
		t.Errorf("Subject = %q was cut through a character", revision.Subject)
	}
}

const (
	testStreamID       = "side-0123456789abcdef0123456789abcdef"
	testConversationID = "chat-0123456789abcdef0123456789abcdef"
)

// testConclusion is one side thread as it merges: concluded, with the provider
// that served it pinned, and with something it tentatively promised.
func testConclusion() Conclusion {
	opened := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	closed := opened.Add(20 * time.Minute)
	return Conclusion{
		Stream: sidestream.Stream{
			SchemaVersion:         sidestream.SchemaVersion,
			ID:                    testStreamID,
			ProductID:             "yoyodyne",
			RepositoryID:          "yoyodyne",
			Agent:                 "product-manager",
			Role:                  domain.RoleProductManager,
			Conversation:          testConversationID,
			Topic:                 "whether the intake hold covers work an operator named",
			Backend:               "claude-code",
			ProviderSessionID:     "session-1",
			ProviderModel:         "opus",
			ProviderResolvedModel: "claude-opus-5-20260514",
			AccountAlias:          "research",
			ConfigRevision:        "cfg-0123456789ab",
			Build:                 "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900",
			MaxTurns:              sidestream.DefaultMaxTurns,
			Turns:                 3,
			CostUSD:               0.14,
			LastSequence:          6,
			Outcome:               sidestream.OutcomeConcluded,
			OpenedAt:              opened,
			UpdatedAt:             closed,
			ClosedAt:              &closed,
		},
		Substance:   "The hold names work the harness chooses for itself, so an item the operator named is exempt from it.",
		Commitments: []string{"tell the operator the hold does not cover work they named"},
	}
}
