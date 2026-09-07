package sidestream

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestASideStreamIdentifierIsNeverAConversationIdentifier(t *testing.T) {
	t.Parallel()

	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if !ValidID(id) {
		t.Fatalf("ValidID(%q) = false, want the shape this package issues", id)
	}
	// The two shapes are what keep the records and the transcripts apart, so a
	// conversation identifier has to fail here and a stream identifier has to fail
	// the conversation's own pattern, which the store tests hold.
	for _, other := range []string{
		"chat-0123456789abcdef0123456789abcdef",
		"exchange-0123456789abcdef0123456789abcdef",
		"run-0123456789abcdef0123456789abcdef",
		"side-0123456789abcdef0123456789abcde",
		"side-../../escape",
		"",
	} {
		if ValidID(other) {
			t.Errorf("ValidID(%q) = true, want a side stream identifier alone", other)
		}
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if second == id {
		t.Fatal("two side streams were issued one identifier")
	}
}

func TestValidateRejectsIncoherentStreams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Stream)
		want   string
	}{
		{"unsupported schema", func(s *Stream) { s.SchemaVersion = 2 }, "schema version 2 is not supported"},
		{"invalid id", func(s *Stream) { s.ID = "side-nope" }, "is invalid"},
		{"no product", func(s *Stream) { s.ProductID = "" }, "product id"},
		{"no agent", func(s *Stream) { s.Agent = "" }, "agent"},
		{"unknown role", func(s *Stream) { s.Role = "auditor" }, "is not one of the harness's roles"},
		{"no main thread", func(s *Stream) { s.Conversation = "" }, "does not name a main thread"},
		{"main thread is another stream", func(s *Stream) { s.Conversation = "side-0123456789abcdef0123456789abcdef" }, "does not name a main thread"},
		{"no topic", func(s *Stream) { s.Topic = "" }, "topic is required"},
		{"no turns allowed", func(s *Stream) { s.MaxTurns = 0 }, "allowed at least one turn"},
		{"more turns than the cap", func(s *Stream) { s.Turns = s.MaxTurns + 1 }, "against a cap of"},
		{"negative cost", func(s *Stream) { s.CostUSD = -0.01 }, "cost cannot be negative"},
		{"unnameable backend", func(s *Stream) { s.Backend = "Smoke Signal" }, "is not a backend identifier"},
		{"unknown outcome", func(s *Stream) { s.Outcome = "abandoned"; s.ClosedAt = &closed }, "is not one a side stream ends with"},
		{"ended with no moment", func(s *Stream) { s.Outcome = OutcomeConcluded }, "recorded together"},
		{"a moment with no ending", func(s *Stream) { s.ClosedAt = &closed }, "recorded together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stream := testStream(t)
			test.mutate(&stream)
			err := stream.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want it to name %q", err, test.want)
			}
		})
	}
}

func TestAConcludedStreamIsNoLongerOpenAndSpendsNoFurtherTurns(t *testing.T) {
	t.Parallel()

	stream := testStream(t)
	if !stream.Open() || stream.TurnsRemaining() != stream.MaxTurns {
		t.Fatalf("a fresh stream is open = %v with %d turns remaining", stream.Open(), stream.TurnsRemaining())
	}
	stream.Turns = stream.MaxTurns
	stream.Outcome = OutcomeSpent
	stream.ClosedAt = &closed
	if stream.Open() {
		t.Fatal("a stream that spent its budget still reports itself open")
	}
	if stream.TurnsRemaining() != 0 {
		t.Fatalf("TurnsRemaining() = %d, want none rather than a debt", stream.TurnsRemaining())
	}
	if err := stream.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	// A stream cut off half way is still a stream that ended, so the record holds
	// what it reached rather than reading as one that said nothing.
	stream.Turns = 1
	if err := stream.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The action-authority answer, held to the design: a side thread judges and reads
// and takes nothing. This is the list itself; the narrowing a conversation
// applies with it is `internal/chat`, and the two are one statement because that
// one reads this one.
func TestASideThreadMayReadAndJudgeAndNothingElse(t *testing.T) {
	t.Parallel()

	permitted := Permitted()
	if len(permitted) == 0 {
		t.Fatal("Permitted() names nothing; a side thread that may ask for nothing at all cannot judge either")
	}
	for _, allowed := range permitted {
		if !allowed.Known() {
			t.Errorf("Permitted() names %q, which this repository does not declare", allowed)
		}
		if !Permits(allowed) {
			t.Errorf("Permits(%q) = false for something Permitted() lists", allowed)
		}
	}
	// Every act the design reserves to the main thread, named one at a time rather
	// than as "the rest": a capability added later is refused by default, and this
	// says which refusals were decided rather than inherited.
	for _, refused := range []capability.Capability{
		capability.WorkItemMutate,
		capability.BacklogAdmit,
		capability.BacklogOrder,
		capability.WorkDecompose,
		capability.WorkTriage,
		capability.ProposalRaise,
		capability.ConcernRaise,
		capability.ResearchCommission,
		capability.EvaluationRecord,
		capability.ExchangeAsk,
		capability.ArtifactProductMutate,
		capability.ArtifactDesignMutate,
		capability.InvariantMutate,
		capability.AgentContextMutate,
		capability.ReviewVerdict,
	} {
		if Permits(refused) {
			t.Errorf("Permits(%q) = true; the design reserves that to the main thread", refused)
		}
	}
	if Permits("") || Permits("anything.at-all") {
		t.Error("a capability this repository does not declare is permitted on a side thread")
	}
	// The list a caller is handed is a copy: a caller that appended to it would be
	// widening what every side thread may do.
	Permitted()[0] = "anything.at-all"
	if !Permits(capability.WorkItemRead) {
		t.Fatal("Permitted() handed out the list itself, so a caller can rewrite what a side thread may ask for")
	}
}

var closed = time.Date(2026, 9, 7, 13, 0, 0, 0, time.UTC)

func testStream(t *testing.T) Stream {
	t.Helper()

	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	opened := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	return Stream{
		SchemaVersion: SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Conversation:  "chat-0123456789abcdef0123456789abcdef",
		Topic:         "whether the intake hold covers work an operator named",
		MaxTurns:      DefaultMaxTurns,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
}
