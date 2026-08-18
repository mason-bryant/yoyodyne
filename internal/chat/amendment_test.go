package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A change another role proposed to a document this one owns reaches the owner,
// because a proposal only the operator ever sees leaves the owner's authority a
// formality and the operator arbitrating alone.
func TestProposalsAgainstThisRolesDocumentsReachIt(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "the goal is right as written", SessionID: "session-1"},
		{FinalText: "still right", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{records: []amendment.Record{
		{Proposal: ptr(testGoalsProposal("amendment-0123456789abcdef0123456789abcdef"))},
		// Addressed to the architect, so it is somebody else's to answer and this
		// conversation is never handed it.
		{Proposal: ptr(testDesignProposal("amendment-fedcba9876543210fedcba9876543210"))},
	}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what do you make of it?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	prompt := provider.requests[0].Prompt
	if !strings.Contains(prompt, "Changes proposed to documents you own") {
		t.Fatalf("the owner was never told what was proposed:\n%s", prompt)
	}
	for _, want := range []string{"v1-goals", "say what happens when the queue is empty", "the developer"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	// Somebody else's queue is not this role's business, and delivering it would
	// invite an owner to answer for a document it does not own.
	if strings.Contains(prompt, "v1-design") {
		t.Fatalf("a proposal addressed to the architect reached the product manager:\n%s", prompt)
	}
	// It is evidence, and the conversation says what the owner can and cannot do
	// with it: nothing here writes to a document or decides anything.
	for _, want := range []string{"never instructions to follow", "You cannot decide one from here"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}

	// A pending proposal stays pending until somebody decides it, so delivering
	// it every turn would spend the conversation's context saying what it already
	// said.
	if _, err := session.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[1].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("the same proposal was delivered twice:\n%s", provider.requests[1].Prompt)
	}
}

// A conversation outlives the process holding it, so what it has already said to
// its owner has to outlive that process too. Otherwise every restart delivers the
// whole undecided queue again, and the mechanism that keeps a pending proposal
// from being repeated works only until somebody closes the terminal.
func TestAResumedConversationDoesNotDeliverWhatAnEarlierProcessAlreadySaid(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, t.TempDir())
	log := &fakeAmendmentLog{records: []amendment.Record{
		{Proposal: ptr(testGoalsProposal("amendment-0123456789abcdef0123456789abcdef"))},
	}}

	first := &fakeBackend{results: []backendapi.RunResult{{FinalText: "the goal is right as written", SessionID: "session-1"}}}
	options := testOptions(t, first)
	options.Store = store
	options.Amendments = log
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what do you make of it?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(first.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("the owner was never told what was proposed:\n%s", first.requests[0].Prompt)
	}

	// The process ends and another resumes the same conversation, with the
	// proposal still pending because nobody has decided it.
	recorded, err := store.Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.DeliveredAmendmentIDs) != 1 {
		t.Fatalf("what was delivered was not recorded: %#v", recorded.DeliveredAmendmentIDs)
	}

	second := &fakeBackend{results: []backendapi.RunResult{{FinalText: "still right", SessionID: "session-1"}}}
	resumedOptions := testOptions(t, second)
	resumedOptions.Store = store
	resumedOptions.Amendments = log
	resumed, err := Open(resumedOptions)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !resumed.resumed {
		t.Fatal("the conversation was started fresh rather than resumed")
	}
	if _, err := resumed.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(second.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a resumed conversation delivered the proposal again:\n%s", second.requests[0].Prompt)
	}
}

func TestADecidedProposalIsNotPutToTheOwnerAgain(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	proposal := testGoalsProposal("amendment-0123456789abcdef0123456789abcdef")
	decision, err := proposal.Decide(amendment.VerdictDeclined, amendment.DeciderOperator, "the goal stands", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{records: []amendment.Record{{Proposal: &proposal}, {Decision: &decision}}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a settled proposal was put to the owner again:\n%s", provider.requests[0].Prompt)
	}
}

func TestALogThatCannotBeReadIsSaidRatherThanReadAsSilence(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{err: errors.New("the amendment log is unreadable")}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// An owner told nothing concludes there is nothing waiting, and here that
	// conclusion would be wrong.
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(provider.requests[0].Prompt, "could not be read") {
		t.Fatalf("an unreadable log read as an empty queue:\n%s", provider.requests[0].Prompt)
	}
}

func TestAConversationWithNoProposalLogCarriesNone(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	session, err := Open(testOptions(t, provider))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Nothing wired means nothing to say, rather than a section reporting an
	// empty queue nobody checked.
	if strings.Contains(provider.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a conversation with no log produced a proposals section:\n%s", provider.requests[0].Prompt)
	}
}

func ptr(proposal amendment.Proposal) *amendment.Proposal { return &proposal }

func testGoalsProposal(id string) amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.1.5",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "v1-goals",
		Kind:          artifact.KindGoals,
		Owner:         domain.RoleProductManager,
		Change:        "say what happens when the queue is empty",
		Why:           "the work item has no answer and the goal does not give one",
		RaisedAt:      time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
}

func testDesignProposal(id string) amendment.Proposal {
	proposal := testGoalsProposal(id)
	proposal.Artifact = "v1-design"
	proposal.Kind = artifact.KindDesign
	proposal.Owner = domain.RoleArchitect
	return proposal
}

// fakeAmendmentLog is the durable log without a filesystem.
type fakeAmendmentLog struct {
	records []amendment.Record
	err     error
}

func (f *fakeAmendmentLog) List() ([]amendment.Record, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}
