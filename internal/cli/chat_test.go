package cli

// What `yoyo chat --message` does with what it is given. The conversation half
// of this is tested where the conversation lives; what is here is the command
// line's own behavior: which of the two paths a message takes, and what each
// path writes to stdout, to stderr, and to a document a script reads.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The defect this work item exists for: `yoyo chat --message "/reports"` used to
// be said to the product manager, who cannot carry out a command, so it bought
// a confused answer with a turn the operator paid for. The provider here has no
// turn to give, which is what makes "nothing was said to her" an assertion
// rather than a claim.
func TestASlashMessageIsCarriedOutByTheHarnessRatherThanSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider, collectedTestReport(t))

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/reports", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bd lint could not run") {
		t.Fatalf("stdout = %q, want the collected pile", stdout.String())
	}
	if provider.turns != 0 {
		t.Fatalf("the product manager was asked %d time(s) to carry out a command", provider.turns)
	}
}

// A script reads the command's output from a field of its own. It is not the
// reply, because nothing replied: putting it there would make a harness listing
// indistinguishable from something the product manager said.
func TestASlashMessageIsReportedAsHarnessOutputRatherThanAsAReply(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider, collectedTestReport(t))

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/reports", true, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout.String())
	}
	if !strings.Contains(decoded.Harness, "bd lint could not run") {
		t.Fatalf("harness = %q, want the collected pile", decoded.Harness)
	}
	if decoded.Reply != "" || decoded.Error != "" {
		t.Fatalf("output = %#v, want nothing to read as something the product manager said", decoded)
	}
	if decoded.Evidence == nil || decoded.Evidence.ConversationID == "" {
		t.Fatalf("evidence = %#v, want the conversation the command was carried out in", decoded.Evidence)
	}
	if provider.turns != 0 {
		t.Fatalf("the product manager was asked %d time(s) to carry out a command", provider.turns)
	}
}

// The commands that only mean something inside a conversation are refused here
// rather than half carried out by a process that is about to exit, and the
// refusal says what does the same job from a command line.
func TestAConversationOnlyCommandIsRefusedInASingleMessage(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider)

	var stdout, stderr bytes.Buffer
	code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/work yoyodyne-ifd.70", false, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing to have been carried out", stdout.String())
	}
	if !strings.Contains(stderr.String(), "yoyo run <beads-id>") {
		t.Fatalf("stderr = %q, want it to name what runs a work item from a command line", stderr.String())
	}
	if provider.turns != 0 {
		t.Fatalf("a refused command was said to the product manager %d time(s)", provider.turns)
	}
}

// Everything that is not a command is still said to the product manager, which
// is the other half of the rule: the interception has to be a slash and not a
// mood.
func TestAMessageThatIsNotACommandIsStillSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-1", FinalText: "The backlog holds four items."},
	}
	session := newTestChatSession(t, provider)

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "what is in the backlog?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if provider.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want exactly one turn", provider.turns)
	}
	if !strings.Contains(stdout.String(), "The backlog holds four items.") {
		t.Fatalf("stdout = %q, want the answer", stdout.String())
	}
}

// The defect this work item exists for: an operator approving a proposal with
// `yoyo chat --message "y"` had their approval said to the product manager as
// ordinary speech. It has no way to create the item under this project's gate,
// so the approval was spent, nothing reached the queue, and nothing said so.
//
// The approval is taken here by a second conversation reading the first one's
// record, because that is what every `--message` approval actually is: the
// process that carried the proposal exited before the operator answered it.
func TestAnApprovalSentAsASingleMessageCreatesTheProposedWorkItem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200", Title: "Pause on a usage limit"}}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}

	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what should we do about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("%d item(s) were created before anybody approved anything", len(tracker.creations))
	}
	if !strings.Contains(stdout.String(), "approve 1.1") {
		t.Fatalf("stdout = %q, want it to say how the proposal is decided", stdout.String())
	}

	// A second process. Nothing of the first one survives but the record, which
	// is exactly the situation the approval failed in.
	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	if !resumed.Resumed() {
		t.Fatalf("the second conversation did not resume the recorded one")
	}
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "y", false, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if deciding.turns != 0 {
		t.Fatalf("the approval was said to the product manager %d time(s)", deciding.turns)
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the one the operator approved", len(tracker.creations))
	}
	if tracker.creations[0].Title != "Pause on a usage limit" {
		t.Fatalf("created %#v, want the proposed work", tracker.creations[0])
	}
	if !strings.Contains(decided.String(), "created yoyodyne-ifd.200") {
		t.Fatalf("stdout = %q, want the item the approval created", decided.String())
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) are still awaiting a decision after being decided", len(resumed.Proposals()))
	}

	// And a third process reads the same record: a decided proposal is decided
	// for everybody, so the same approval sent twice does not create the item
	// twice.
	again := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "It is already in the queue."}}
	third := openTestChatSession(t, root, again, tracker)
	if len(third.Proposals()) != 0 {
		t.Fatalf("a decided proposal came back as pending: %#v", third.Proposals())
	}
	var repeated, repeatedAside bytes.Buffer
	if code := runChatMessage(context.Background(), third, domain.RoleProductManager, "y", false, &repeated, &repeatedAside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, repeatedAside.String())
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the approval to have been spent once", len(tracker.creations))
	}
}

// A decline sent as a single message decides the proposal too, and keeps the
// operator's own words as the reason. Nothing is created either way, which is
// why this is the half of the rule that must not silently do nothing: a
// proposal nobody decided is one that comes back, and a declined one does not.
func TestADeclineSentAsASingleMessageDecidesTheProposal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "decline 1.1 we already handle this", true, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(decided.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, decided.String())
	}
	if len(decoded.Decisions) != 1 || decoded.Decisions[0].Approved {
		t.Fatalf("decisions = %#v, want the proposal declined", decoded.Decisions)
	}
	if !strings.Contains(decoded.Decisions[0].Reason, "we already handle this") {
		t.Fatalf("reason = %q, want the operator's own words", decoded.Decisions[0].Reason)
	}
	if len(decoded.Pending) != 0 {
		t.Fatalf("pending = %#v, want nothing still waiting", decoded.Pending)
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("a decline created %d item(s)", len(tracker.creations))
	}
	if deciding.turns != 0 {
		t.Fatalf("the decline was said to the product manager %d time(s)", deciding.turns)
	}
}

// The other half of the rule. A prompt may read anything it cannot understand
// as a decline, because it asked; a single message was never asked anything, so
// speech is speech and the proposal stays on the table.
func TestAMessageThatIsNotADecisionIsStillSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	answering := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "Two of them are about capacity."}}
	resumed := openTestChatSession(t, root, answering, tracker)
	var said, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "what else is open?", false, &said, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if answering.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want the message said to her", answering.turns)
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one left exactly where it was", len(resumed.Proposals()))
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("speech created %d item(s)", len(tracker.creations))
	}
}

// An approval naming a proposal this conversation no longer holds says so. The
// failure this guards is the quiet one: an identifier that has expired, or that
// was mistyped, being passed on to the product manager as a sentence to
// interpret, which is how an approval goes missing without anybody being told.
func TestADecisionNamingAProposalThatIsNotThereSaysSoRatherThanBeingSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	var decided, aside bytes.Buffer
	code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "approve 9.9", false, &decided, &aside)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, aside.String())
	}
	if !strings.Contains(aside.String(), "9.9") {
		t.Fatalf("stderr = %q, want it to name what could not be decided", aside.String())
	}
	if deciding.turns != 0 {
		t.Fatalf("a decision the harness could not carry out was said to the product manager %d time(s)", deciding.turns)
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("a refused decision created %d item(s)", len(tracker.creations))
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one still waiting", len(resumed.Proposals()))
	}
}

// The same holds when nothing is awaiting a decision at all, which is the case
// it most needs to hold in: an operator approving a proposal that a second
// process decided while they were away is exactly the operator whose approval
// went missing before. Their message names a proposal, so it is answered here
// rather than bought as a turn from a product manager who cannot act on it.
func TestAnApprovalOfAnAlreadyDecidedProposalIsRefusedRatherThanSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200"}}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	// Somebody else decides it, and the operator does not see that happen.
	deciding := openTestChatSession(t, root, &recordingChatBackend{}, tracker)
	var swallowed, quiet bytes.Buffer
	if code := runChatMessage(context.Background(), deciding, domain.RoleProductManager, "y", false, &swallowed, &quiet); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, quiet.String())
	}

	late := &recordingChatBackend{}
	stale := openTestChatSession(t, root, late, tracker)
	var out, aside bytes.Buffer
	code := runChatMessage(context.Background(), stale, domain.RoleProductManager, "approve 1.1", false, &out, &aside)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, aside.String())
	}
	if late.turns != 0 {
		t.Fatalf("an approval of a decided proposal was said to the product manager %d time(s)", late.turns)
	}
	if !strings.Contains(aside.String(), "1.1") {
		t.Fatalf("stderr = %q, want it to name the proposal that is no longer awaiting a decision", aside.String())
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the approval spent exactly once", len(tracker.creations))
	}
}

// An approval the tracker will not carry out reports that nothing was created
// and that the proposal is still waiting, which is what tells the operator to
// try again rather than that the item exists.
func TestAnApprovalTheTrackerRefusesIsReportedAsCreatingNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, &recordingChatTracker{})
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	unreachable := &recordingChatTracker{createErr: errors.New("bd is unreachable")}
	resumed := openTestChatSession(t, root, &recordingChatBackend{}, unreachable)
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "y", true, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(decided.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, decided.String())
	}
	if len(decoded.Decisions) != 1 || decoded.Decisions[0].WorkItemID != "" || !decoded.Decisions[0].Undecided {
		t.Fatalf("decisions = %#v, want an approval that created nothing", decoded.Decisions)
	}
	if !strings.Contains(decoded.Decisions[0].Problem, "bd is unreachable") {
		t.Fatalf("problem = %q, want what the tracker said", decoded.Decisions[0].Problem)
	}
	// And it is still on the table, so a script reading this knows what to ask
	// for again rather than having to work out whether the item exists.
	if len(decoded.Pending) != 1 || decoded.Pending[0].ID != "1.1" {
		t.Fatalf("pending = %#v, want the refused proposal still awaiting a decision", decoded.Pending)
	}
}

// A command that recorded something and then failed to report it recorded it
// all the same, so what it printed is written before the failure rather than
// lost behind it — and the failure is still the command's, which is what the
// exit code says.
func TestAFailedCommandStillWritesWhatItDid(t *testing.T) {
	t.Parallel()

	evidence := chat.Evidence{ConversationID: "chat-0123456789abcdef0123456789abcdef"}
	rendered := "recorded your direction on yoyodyne-ifd.70; the next attempt at it reads it.\n"
	failure := errors.New("record direction on yoyodyne-ifd.70 where the work is tracked: bd is unreachable")

	var stdout, stderr bytes.Buffer
	if code := reportChatCommand(&stdout, &stderr, false, evidence, rendered, failure); code != 1 {
		t.Fatalf("reportChatCommand() code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "recorded your direction") {
		t.Fatalf("stdout = %q, want what the command did", stdout.String())
	}
	if !strings.Contains(stderr.String(), "bd is unreachable") {
		t.Fatalf("stderr = %q, want the failure", stderr.String())
	}

	// The document says both too, and a caller reading it can tell a command that
	// half-succeeded from one that did nothing.
	stdout.Reset()
	stderr.Reset()
	if code := reportChatCommand(&stdout, &stderr, true, evidence, rendered, failure); code != 1 {
		t.Fatalf("reportChatCommand() code = %d, want 1", code)
	}
	var decoded chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout.String())
	}
	if !strings.Contains(decoded.Harness, "recorded your direction") || !strings.Contains(decoded.Error, "bd is unreachable") {
		t.Fatalf("output = %#v, want both what it did and what failed", decoded)
	}
}

// newTestChatSession builds a conversation over a real conversation store and a
// provider that has to be asked before it answers, so what is tested is the
// command line's own behavior rather than a stand-in for the conversation.
func newTestChatSession(t *testing.T, provider chat.Backend, reports ...report.Report) *chat.Session {
	t.Helper()

	root := t.TempDir()
	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	collected, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	for _, reported := range reports {
		if err := collected.Append(reported); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleProductManager,
		Agent:        "product-manager",
		Backend:      provider,
		Store:        store,
		Reports:      collected,
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// proposalReply is one answer that proposes a single work item and creates
// nothing, which is what this project's gate makes of every proposal: the
// operator decides it.
const proposalReply = `We could wait out the limit rather than failing the run.

` + "```yoyodyne-proposal" + `
{"items":[{"title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// openTestChatSession builds a conversation over a state root the caller owns,
// so two of them are two processes talking to one recorded conversation — which
// is what a proposal made by one `--message` and decided by the next actually
// is.
func openTestChatSession(t *testing.T, root string, provider chat.Backend, tracker chat.Tracker) *chat.Session {
	t.Helper()

	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleProductManager,
		Agent:        "product-manager",
		Backend:      provider,
		Store:        store,
		Tracker:      tracker,
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// recordingChatTracker is a work tracker that remembers what it was asked to
// create. An item it never recorded is what proves nothing reached the queue.
type recordingChatTracker struct {
	created   beads.WorkItem
	creations []beads.NewWorkItem
	// createErr is a tracker that will not create what it was asked to, which is
	// what makes "nothing was created and the proposal is still waiting" an
	// assertion rather than a claim.
	createErr error
}

func (t *recordingChatTracker) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test asked the tracker for an item it does not hold")
}

func (t *recordingChatTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, nil
}

func (t *recordingChatTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	if t.createErr != nil {
		return beads.WorkItem{}, t.createErr
	}
	t.creations = append(t.creations, item)
	created := t.created
	if created.Title == "" {
		created.Title = item.Title
	}
	return created, nil
}

func (t *recordingChatTracker) Update(context.Context, string, beads.WorkItemChange) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect an update")
}

func (t *recordingChatTracker) Block(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a block")
}

func (t *recordingChatTracker) AddBlocker(context.Context, string, string) error    { return nil }
func (t *recordingChatTracker) RemoveBlocker(context.Context, string, string) error { return nil }

func (t *recordingChatTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a completion")
}

func collectedTestReport(t *testing.T) report.Report {
	t.Helper()

	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.70",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityCritical,
		Message:       "bd lint could not run in its sandbox, so nothing linted the item",
		RecordedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
}

// recordingChatBackend counts what the product manager was asked. A turn it was
// never given is what proves a command never reached her, so the zero value
// answers nothing at all.
type recordingChatBackend struct {
	result backendapi.RunResult
	turns  int
}

func (b *recordingChatBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	b.turns++
	if b.result.SessionID == "" {
		return backendapi.RunResult{}, errors.New("the product manager was asked something this test did not expect")
	}
	result := b.result
	result.Backend = domain.BackendClaudeCode
	return result, nil
}
