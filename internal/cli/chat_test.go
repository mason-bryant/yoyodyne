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
	if code := runChatMessage(context.Background(), session, "/reports", false, &stdout, &stderr); code != 0 {
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
	if code := runChatMessage(context.Background(), session, "/reports", true, &stdout, &stderr); code != 0 {
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
	code := runChatMessage(context.Background(), session, "/work yoyodyne-ifd.70", false, &stdout, &stderr)
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
	if code := runChatMessage(context.Background(), session, "what is in the backlog?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if provider.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want exactly one turn", provider.turns)
	}
	if !strings.Contains(stdout.String(), "The backlog holds four items.") {
		t.Fatalf("stdout = %q, want the answer", stdout.String())
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
