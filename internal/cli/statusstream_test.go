package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The surface this absorbed was the operator's daily one, and it shipped only to
// people working from a checkout. So what matters first is that the verb the
// install carries answers the same three questions: what has been running, what
// it spent, and what one of them is doing right now.
func TestStatusListsEveryKindOfStream(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing recorded is an answer rather than an empty listing.
	stdout, stderr, code := runCLI(t, "status", "--list", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	// An empty answer names the directory it read, because the state root comes
	// from the environment and a true answer about the wrong directory is the one
	// failure this surface cannot afford.
	if !strings.Contains(stdout, "no runs, conversations, or branch reviews are recorded under "+filepath.Join(stateRoot, "products", "yoyodyne")) {
		t.Fatalf("stdout = %q", stdout)
	}

	runID := recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	chatID := recordStreamConversation(t, stateRoot, streamStart, nil)
	reviewID := recordStreamReview(t, stateRoot, streamStart, 0)

	stdout, stderr, code = runCLI(t, "status", "--list", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{runID, "run", "running", chatID, "conversation", reviewID, "review", "reviewing"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// Narrowing to one kind is what an operator who only cares about runs asks
	// for, and it leaves the other two out rather than merely ordering them
	// lower.
	stdout, stderr, code = runCLI(t, "status", "--list", "--kind", "chats", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, chatID) || strings.Contains(stdout, runID) {
		t.Fatalf("chats only listed %q", stdout)
	}

	// A machine-readable listing carries the same facts, so a script never has to
	// parse the columns.
	stdout, stderr, code = runCLI(t, "status", "--list", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var listed streamListOutput
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if len(listed.Streams) != 3 {
		t.Fatalf("JSON listed %d stream(s), want three", len(listed.Streams))
	}
}

// The spend rendering is the one the operator has been reading and asked to
// keep: grouped by the local day the money was spent on, each day's group closed
// by that day's spend, the total under a rule, and the split saying how much of
// it was each kind of work.
func TestStatusSpendGroupsByTheDayItWasSpent(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// A machine that has recorded nothing says so, rather than reporting an empty
	// week and inviting a wider window that would be just as empty.
	stdout, _, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no runs, conversations, or branch reviews are recorded under "+stateRoot) {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	// A machine that has recorded something but spent nothing in the days asked
	// about says which window it is empty about, because a wider one would answer
	// differently.
	recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	stdout, _, code = runCLI(t, "status", "--spend", "1", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no completed provider invocations since") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}

	today := time.Now()
	runID := recordStreamRun(t, stateRoot, runstate.StatusSucceeded, today.Add(-2*time.Hour), 12.5)
	chatID := recordStreamConversation(t, stateRoot, today.AddDate(0, 0, -14), []time.Time{today.AddDate(0, 0, -14), today})
	reviewID := recordStreamReview(t, stateRoot, today.Add(-time.Hour), 2)

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		runstate.LocalDay(today) + "  (today)",
		runID, reviewID, chatID,
		runstate.LocalDay(today) + " total",
		"TOTAL (last 7 days)",
		"cache reads",
		"runs: $12.50 from 1 invocation(s)",
		"conversations: $1.00 from 1 turn(s)",
		"branch reviews: $2.00 from 1 invocation(s)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// The fortnight-old turn of that conversation is outside a week, and only the
	// turn is: the same conversation is still reported for what it spent today.
	if strings.Count(stdout, chatID) != 1 {
		t.Fatalf("stdout = %q, want the conversation's out-of-window turn left out", stdout)
	}

	// A number asks for a different count of days, and it reaches the older turn
	// the default window left out.
	stdout, stderr, code = runCLI(t, "status", "--spend", "30", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, chatID) != 2 || !strings.Contains(stdout, "TOTAL (last 30 days)") {
		t.Fatalf("a thirty day window = %q", stdout)
	}

	// Naming a stream prices that one whatever day it ran on, because an id has
	// already chosen what to show.
	stdout, stderr, code = runCLI(t, "status", "--spend", chatID[:12], "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, chatID) != 2 || strings.Contains(stdout, runID) {
		t.Fatalf("naming a conversation reported %q", stdout)
	}
	if strings.Contains(stdout, "last") {
		t.Fatalf("a named stream was still windowed: %q", stdout)
	}

	// A machine-readable report is the same figures, and it says which window it
	// covered so a reader can tell an empty week from an empty machine.
	stdout, stderr, code = runCLI(t, "status", "--spend", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var priced spendOutput
	if err := json.Unmarshal([]byte(stdout), &priced); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if priced.Report.Days != 7 || priced.Report.Oldest == "" || len(priced.Report.Rows) != 3 {
		t.Fatalf("JSON report = %+v", priced.Report)
	}

	// A stream named but not recorded is a question that could not be asked,
	// rather than a machine that spent nothing.
	_, stderr, code = runCLI(t, "status", "--spend", "chat-nothing", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "no run, conversation, or branch review matching") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

// Following is the closest thing there is to watching an agent work, and what it
// emits is the recorded events themselves: the shaping drops the envelope every
// line repeats and the thinking-token pings that drown everything else, and
// --raw gives back exactly what was written.
func TestStatusFollowsAStreamAsItsEventsArrive(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	runID := recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	appendStreamEvent(t, store, runID, 2, execution.EventProcessOutput, streamStart, map[string]any{
		"provider_subtype": "thinking_tokens",
	})

	stdout, stderr, code := runCLI(t, "status", "--events", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "==> "+runID+" [running]") {
		t.Fatalf("stderr = %q, want it to name the stream being read", stderr)
	}
	if !strings.Contains(stdout, `"type":"run.started"`) || strings.Contains(stdout, "thinking_tokens") {
		t.Fatalf("stdout = %q, want the shaped events without the thinking-token noise", stdout)
	}
	if strings.Contains(stdout, `"schema_version"`) {
		t.Fatalf("stdout = %q, want the envelope every line repeats left out", stdout)
	}
	if stdout, _, _ = runCLI(t, "status", "--events", "--all", "--config", configPath); !strings.Contains(stdout, "thinking_tokens") {
		t.Fatalf("--all = %q, want the noise included when it was asked for", stdout)
	}
	if stdout, _, _ = runCLI(t, "status", "--events", "--raw", "--config", configPath); !strings.Contains(stdout, `"schema_version"`) {
		t.Fatalf("--raw = %q, want each event exactly as it was recorded", stdout)
	}

	// Following keeps emitting what arrives, and stops when the operator does.
	// The events are appended after the follow is under way, so what is asserted
	// is that they were picked up rather than replayed.
	ctx, stop := context.WithCancel(context.Background())
	// The follow writes from its own goroutine while this one watches what has
	// arrived, which is two goroutines on one buffer: they are separated here
	// rather than in the command, because a command writing to whatever io.Writer
	// it was handed is exactly what every other one does.
	out, errs := &syncBuffer{}, &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- reportRunStatus(ctx, []string{"--follow", "--config", configPath}, out, errs)
	}()
	appendStreamEvent(t, store, runID, 3, execution.EventAgentMessage, streamStart, map[string]any{"text": "still working"})
	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), "still working") {
		select {
		case <-deadline:
			t.Fatalf("the appended event never arrived; stdout = %q, stderr = %q", out.String(), errs.String())
		case <-time.After(streamPollInterval):
		}
	}
	stop()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("an interrupted follow exited %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the follow did not stop when it was interrupted")
	}
}

// A machine somebody paused and a machine that died look identical, and this is
// the one place an operator is already looking, so every mode of the verb says
// so first — on the stream a banner belongs on, so machine-readable output stays
// machine-readable.
func TestStatusAnnouncesWhatTheOperatorHasStopped(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	if _, err := holds.Hold(time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold("waiting on the release", time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	for _, mode := range [][]string{{"--list"}, {"--spend"}, {}} {
		args := append([]string{"status"}, mode...)
		stdout, stderr, code := runCLI(t, append(args, "--config", configPath)...)
		if code != 0 {
			t.Fatalf("%v code = %d, stderr = %q", mode, code, stderr)
		}
		for _, want := range []string{"PAUSED", "yoyo resume", "INTAKE HELD", "waiting on the release", "yoyo release"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v stderr = %q, want it to contain %q", mode, stderr, want)
			}
		}
		if strings.Contains(stdout, "PAUSED") {
			t.Fatalf("%v put a banner on the output stream: %q", mode, stdout)
		}
	}

	// The same two facts are carried in the machine-readable answer, because a
	// script reading only stdout would otherwise never learn them.
	stdout, _, code := runCLI(t, "status", "--list", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var listed streamListOutput
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if listed.Paused == nil || listed.Intake == nil {
		t.Fatalf("JSON listing = %+v, want both holds carried", listed)
	}
}

// The modes are different questions rather than different amounts of one, so
// asking two of them is refused instead of getting a precedence nobody chose.
func TestStatusRefusesStreamOptionsItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--list", "--spend"}, "different questions"},
		{[]string{"status", "--latest"}, "needs --follow"},
		{[]string{"status", "--raw"}, "need --follow or --events"},
		{[]string{"status", "--kind", "runs"}, "--kind narrows which event streams are read"},
		{[]string{"status", "--list", "--failed"}, "cannot narrow a stream"},
		{[]string{"status", "--follow", "--json"}, "--raw is what asks for them untouched"},
		{[]string{"status", "--kind", "sideways"}, "unknown kind"},
		{[]string{"status", "--events", "--lines", "-1"}, "lines cannot be negative"},
		{[]string{"status", "--spend", "0"}, "neither a positive number of days nor a stream id"},
	} {
		_, stderr, code := runCLI(t, refusal.args...)
		if code != 2 {
			t.Fatalf("%v code = %d, want 2; stderr = %q", refusal.args, code, stderr)
		}
		if !strings.Contains(stderr, refusal.want) {
			t.Fatalf("%v stderr = %q, want it to contain %q", refusal.args, stderr, refusal.want)
		}
	}
}

// streamStart is when the fabricated streams here opened, fixed so nothing
// asserted about them depends on when the test ran.
var streamStart = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// syncBuffer is what a still-running command writes to while the test reads
// what it has written so far. Nothing about following needs it — a command
// writes to the io.Writer it was handed and never reads it back — but a test
// that watches output arrive is by construction on a second goroutine.
type syncBuffer struct {
	mu      sync.Mutex
	written bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.String()
}

func recordStreamRun(t *testing.T, stateRoot string, status runstate.Status, startedAt time.Time, cost float64) string {
	t.Helper()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	state := recordedRun(t, store, status, "yoyodyne-ifd.63", startedAt)
	appendStreamEvent(t, store, state.RunID, 1, execution.EventRunStarted, startedAt, map[string]any{"session_id": "session-developer"})
	if cost > 0 {
		appendStreamEvent(t, store, state.RunID, 2, execution.EventRunCompleted, startedAt, streamCost(cost))
	}
	return state.RunID
}

// recordStreamConversation writes a conversation the role is still in, with one
// completed turn at each of the moments given.
func recordStreamConversation(t *testing.T, stateRoot string, startedAt time.Time, turns []time.Time) string {
	t.Helper()
	store, err := runstate.NewConversationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	id, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      domain.ProductID("yoyodyne"),
		RepositoryID:   "yoyodyne",
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		ProviderModel:  "opus",
		Turns:          len(turns),
		StartedAt:      startedAt,
		UpdatedAt:      startedAt,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sequence := uint64(1)
	appendConversationEvent(t, store, id, sequence, execution.EventRunStarted, startedAt, map[string]any{"session_id": "session-chat"})
	for _, turn := range turns {
		sequence++
		appendConversationEvent(t, store, id, sequence, execution.EventRunCompleted, turn, streamCost(1))
	}
	return id
}

func recordStreamReview(t *testing.T, stateRoot string, startedAt time.Time, cost float64) string {
	t.Helper()
	store, err := runstate.NewBranchReviewStore(stateRoot, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	id, err := runstate.NewBranchReviewID()
	if err != nil {
		t.Fatalf("NewBranchReviewID() error = %v", err)
	}
	appendReviewEvent(t, store, id, 1, execution.EventReviewStarted, startedAt, map[string]any{"session_id": "session-review"})
	if cost > 0 {
		appendReviewEvent(t, store, id, 2, execution.EventRunCompleted, startedAt, streamCost(cost))
	}
	return id
}

// streamCost is what the provider reports when an invocation ends, which is the
// only place the cost and the token counts are ever written down.
func streamCost(cost float64) map[string]any {
	return map[string]any{
		"session_id":     "session-stream",
		"total_cost_usd": cost,
		"usage": map[string]any{
			"input_tokens":                100,
			"output_tokens":               200,
			"cache_creation_input_tokens": 300,
			"cache_read_input_tokens":     4000,
		},
	}
}

func appendStreamEvent(t *testing.T, store *runstate.Store, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendConversationEvent(t *testing.T, store *runstate.ConversationStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendReviewEvent(t *testing.T, store *runstate.BranchReviewStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func newStreamEvent(t *testing.T, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) execution.Event {
	t.Helper()
	event, err := execution.NewEvent(id, sequence, at, eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

// A count of tokens nobody can read at a glance defeats the line it appears on.
func TestGroupThousandsSeparatesWhatItCounts(t *testing.T) {
	t.Parallel()

	for value, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1067970: "1,067,970", -12345: "-12,345"} {
		if got := groupThousands(value); got != want {
			t.Fatalf("groupThousands(%d) = %q, want %q", value, got, want)
		}
	}
}

// A log the harness is halfway through writing holds no event yet, and one that
// was replaced underneath is not read from an offset that now means something
// else. Both are what following a live stream actually runs into.
func TestLogTailEmitsOnlyWholeLinesAndSurvivesReplacement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "stream.events.jsonl")
	if err := os.WriteFile(path, []byte("first\nsec"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	tail := &logTail{path: path}
	lines, err := tail.read()
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if len(lines) != 1 || string(lines[0]) != "first" {
		t.Fatalf("read() = %q, want only the whole line", lines)
	}
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if lines, err = tail.read(); err != nil || len(lines) != 1 || string(lines[0]) != "second" {
		t.Fatalf("read() = %q, %v, want the line completed and nothing repeated", lines, err)
	}
	if err := os.WriteFile(path, []byte("replaced\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if lines, err = tail.read(); err != nil || len(lines) != 1 || string(lines[0]) != "replaced" {
		t.Fatalf("read() after truncation = %q, %v", lines, err)
	}
}
