package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const testRunID = "run-0123456789abcdef0123456789abcdef"

func TestCheckAvailability(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: "2.1.222 (Claude Code)\n"},
		{Status: execution.ProcessFailed, ExitCode: 1, Stdout: `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`},
	}}
	availability, err := (Backend{Runner: runner}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || availability.Authenticated || availability.AuthMethod != "none" || availability.Version != "2.1.222 (Claude Code)" {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
}

func TestCheckAvailabilityMissingCLI(t *testing.T) {
	t.Parallel()

	availability, err := (Backend{Runner: &fakeRunner{errors: []error{exec.ErrNotFound}}}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if availability.Installed {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
}

func TestRunParsesStructuredSuccessAndToolActivity(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test","permissionMode":"acceptEdits","tools":["Read","Edit"],"capabilities":["msg_lifecycle_v1"]}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"working"},{"type":"tool_use","id":"tool-1","name":"Edit","input":{"file_path":"main.go"}}]}}`,
		`{"type":"user","session_id":"session-1","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"updated","is_error":false}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":0.01,"usage":{"input_tokens":10},"stop_reason":"end_turn"}`,
	}, "\n") + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	var events []execution.Event
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText != "done" || result.SessionID != "session-1" || result.LastEvent != 5 {
		t.Fatalf("Run() result = %#v", result)
	}
	// The model the provider resolved the requested selector to is first-class
	// result evidence, not something a caller has to dig out of an event.
	if result.ResolvedModel != "claude-test" {
		t.Fatalf("Run() resolved model = %q, want claude-test", result.ResolvedModel)
	}
	wantTypes := []execution.EventType{
		execution.EventRunStarted,
		execution.EventAgentMessage,
		execution.EventCommandStarted,
		execution.EventCommandCompleted,
		execution.EventRunCompleted,
	}
	gotTypes := make([]execution.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	for _, event := range events {
		payload := string(event.Payload)
		if strings.Contains(payload, "main.go") || strings.Contains(payload, "updated") {
			t.Fatalf("tool payload persisted raw input or result content: %s", payload)
		}
	}
	if !strings.Contains(string(events[2].Payload), `"input_bytes"`) || !strings.Contains(string(events[3].Payload), `"content_bytes"`) {
		t.Fatalf("tool events do not retain safe size metadata: started=%s completed=%s", events[2].Payload, events[3].Payload)
	}
	if runner.prompts[0] != "implement the task" {
		t.Fatalf("prompt = %q", runner.prompts[0])
	}
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--name", "yoyodyne-01234567", "--settings", developerSettings, "--allowedTools", "Bash", "Read", "Edit(/**)", "Write(/**)", "Glob", "Grep"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}
}

// An invocation's terminal is the only event carrying what it cost, and a run's
// log holds several invocations: the developer's attempts and the reviewer's
// beside them. So the terminal has to say whose invocation it ended, or the
// money on it can only be attributed by guessing from where in the log it sits
// -- which is a fact about the order the harness happened to do things in, and
// silently wrong for any invocation nobody anticipated. What reads this is the
// phase split in internal/runstate.
func TestATerminalNamesTheRoleTheInvocationWasMadeAs(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		role    domain.AgentRole
		tools   []string
		stream  string
		wanted  execution.EventType
		isError bool
	}{
		{
			name:   "a developer that finished",
			role:   domain.RoleDeveloper,
			stream: `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":12.5,"terminal_reason":"end_turn"}`,
			wanted: execution.EventRunCompleted,
		},
		{
			// A failed invocation is priced with the money it spent, so its terminal
			// has to be attributable for the same reason a successful one does.
			name:    "a developer the provider ended badly",
			role:    domain.RoleDeveloper,
			stream:  `{"type":"result","subtype":"error","session_id":"session-1","is_error":true,"result":"API Error","total_cost_usd":1.5,"terminal_reason":"api_error"}`,
			wanted:  execution.EventRunFailed,
			isError: true,
		},
		{
			name:   "a reviewer",
			role:   domain.RoleReviewer,
			tools:  []string{},
			stream: `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"{}","total_cost_usd":0.75,"terminal_reason":"end_turn"}`,
			wanted: execution.EventRunCompleted,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: testCase.stream + "\n"}}}
			var terminal *execution.Event
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             testCase.role,
				WorkingDirectory: "/worktree",
				Prompt:           "do the work",
				AllowedTools:     testCase.tools,
				EventSink: func(event execution.Event) error {
					if event.Type == execution.EventRunCompleted || event.Type == execution.EventRunFailed {
						recorded := event
						terminal = &recorded
					}
					return nil
				},
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if terminal == nil {
				t.Fatal("no terminal event was recorded")
			}
			if terminal.Type != testCase.wanted {
				t.Fatalf("terminal type = %q, want %q", terminal.Type, testCase.wanted)
			}
			var payload struct {
				Role    string `json:"role"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
				t.Fatalf("decode terminal payload: %v", err)
			}
			if payload.Role != string(testCase.role) || payload.IsError != testCase.isError {
				t.Fatalf("terminal payload = %s, want role %q", terminal.Payload, testCase.role)
			}
		})
	}
}

// The cache-read share `yoyo cost` reports is decoded off this terminal, by
// internal/runstate, from one object under "usage" on the payload and by the
// provider's own key names. Nothing else in the repository states that the two
// sides agree: a usage object recorded one level deeper, under another key, or
// flattened into the payload would leave every real run reporting no token usage
// at all, while every test on the reading side went on passing against a fixture
// it wrote itself. So the shape is asserted here, where it is written.
//
// The usage object is copied verbatim from a recorded invocation — run
// run-bd535e5ee0027b61fc5b190053699e0b, developing yoyodyne-ifd.84 on
// 2026-08-23, sequence 439 — so what is pinned is what a provider really sends
// rather than the four fields the reader happens to want. The nested breakdowns
// beside them matter: `cache_creation` and each entry of `iterations` repeat the
// same key names, and a reader that descended into either would count the same
// tokens two and three times over.
func TestATerminalCarriesTheProvidersUsageWhereThePriceReaderLooksForIt(t *testing.T) {
	t.Parallel()

	const usage = `{"cache_creation":{"ephemeral_1h_input_tokens":187181,"ephemeral_5m_input_tokens":0},` +
		`"cache_creation_input_tokens":187181,"cache_read_input_tokens":7796697,"inference_geo":"not_available",` +
		`"input_tokens":114,"iterations":[{"cache_creation_input_tokens":31672,"cache_read_input_tokens":118409,` +
		`"input_tokens":7,"output_tokens":1204}],"output_tokens":58231,"service_tier":"standard"}`
	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done",` +
		`"total_cost_usd":12.5,"usage":` + usage + `,"terminal_reason":"end_turn"}`

	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream + "\n"}}}
	var terminal *execution.Event
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		EventSink: func(event execution.Event) error {
			if event.Type == execution.EventRunCompleted {
				recorded := event
				terminal = &recorded
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if terminal == nil {
		t.Fatal("no terminal event was recorded")
	}
	// Decoded exactly as internal/runstate decodes it: a pointer, so that a
	// terminal carrying no usage at all stays distinguishable from one reporting
	// noughts, and by the provider's key names at the top level of the object.
	var payload struct {
		Usage *struct {
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	if payload.Usage == nil {
		t.Fatalf(`terminal payload = %s, want the provider's usage object under "usage"`, terminal.Payload)
	}
	if payload.Usage.CacheReadTokens != 7796697 || payload.Usage.CacheCreationTokens != 187181 ||
		payload.Usage.InputTokens != 114 || payload.Usage.OutputTokens != 58231 {
		t.Fatalf("usage = %#v, want the recorded invocation's own top-level figures", *payload.Usage)
	}

	// And a terminal the provider ended without reporting usage records none,
	// rather than an object of noughts. That is the whole reason the reader holds
	// a pointer: an invocation nobody measured must not read as one measured at
	// nothing.
	unpriced := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":12.5,"terminal_reason":"end_turn"}` + "\n"}}}
	terminal = nil
	if _, err := (Backend{Runner: unpriced, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		EventSink: func(event execution.Event) error {
			if event.Type == execution.EventRunCompleted {
				recorded := event
				terminal = &recorded
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if terminal == nil {
		t.Fatal("no terminal event was recorded")
	}
	payload.Usage = nil
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	if payload.Usage != nil {
		t.Fatalf("terminal payload = %s, want no usage object where the provider reported none", terminal.Payload)
	}
}

// What a developer run is given beyond its tools, asserted from the settings
// themselves rather than against the constant that produced them.
//
// The argument comparison above proves developerSettings is what reached the CLI
// and says nothing whatever about what is inside it: written against the same
// constant, it goes on passing on the day either clause is edited out. Both
// clauses are load-bearing and neither is visible in an argument list -- the
// sandbox is what confines Bash, and the PreToolUse hook is what stops
// `bd update --notes` in a developer run destroying a recorded attribution.
//
// Each is matched whole rather than by its words in any order, so what passes is
// the hook nested on the Bash matcher and not three strings that happen to be
// present. That makes this sensitive to how the constant is punctuated, which is
// the intended trade: it is a literal in the same file, and reformatting it is
// something somebody does deliberately and can re-read here.
//
// What it does not prove is that Claude Code accepts the block, that the hook
// fires, or that `yoyo` resolves on the run's PATH. That is an end-to-end fact
// about the provider, and this asserts only what the harness controls: the wiring
// it hands over. The path fails open by design, so a mistake there is silent --
// which is why the witness, not this, is what makes a destroyed attribution
// recoverable.
func TestADeveloperRunIsSandboxedAndCarriesTheAttributionGuard(t *testing.T) {
	t.Parallel()

	if !json.Valid([]byte(developerSettings)) {
		t.Fatalf("developer settings are not valid JSON, so Claude Code would refuse them: %s", developerSettings)
	}
	sandbox := `"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}`
	if !strings.Contains(developerSettings, sandbox) {
		t.Fatalf("a developer run's Bash is not confined: %s", developerSettings)
	}
	guard := `"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]`
	if !strings.Contains(developerSettings, guard) {
		t.Fatalf("a developer run carries no PreToolUse guard on Bash, so a `bd update <id> --notes` in one would destroy an attribution unremarked: %s", developerSettings)
	}
}

func TestRunPassesTheRequestedModelAndReportsWhatServedIt(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5-20260514"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","model":"ignored-late-model"}`,
	}, "\n") + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		Model:            "opus",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// A floating alias is requested; the model the run actually started on is
	// what gets recorded, and a later event cannot rewrite it.
	if result.ResolvedModel != "claude-opus-5-20260514" {
		t.Fatalf("Run() resolved model = %q", result.ResolvedModel)
	}
	var sawModel bool
	for index, arg := range runner.commands[0].Args {
		if arg == "--model" && index+1 < len(runner.commands[0].Args) && runner.commands[0].Args[index+1] == "opus" {
			sawModel = true
		}
	}
	if !sawModel {
		t.Fatalf("args did not pass the requested selector: %#v", runner.commands[0].Args)
	}
}

func TestRunKeepsTheDecidedResultWhenTheProviderKeepsWriting(t *testing.T) {
	t.Parallel()

	// The provider legitimately writes after its terminal result. The outcome is
	// already decided, so the trailing events are recorded as evidence and must
	// not rewrite the result, session, model, usage, or cost.
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":0.25,"usage":{"input_tokens":10},"terminal_reason":"end_turn"}`,
		`{"type":"rate_limit_event","session_id":"session-2","subtype":"upgrade"}`,
		`{"type":"assistant","session_id":"session-2","message":{"content":[{"type":"text","text":"trailing chatter"}]}}`,
		`{"type":"system","subtype":"init","session_id":"session-2","model":"late-model"}`,
		`{"type":"result","session_id":"session-2","result":"","terminal_reason":"","usage":{}}`,
	}, "\n") + "\n"
	var events []execution.Event
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText != "done" || result.StopReason != "end_turn" {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.SessionID != "session-1" || result.ResolvedModel != "claude-test" || result.CostUSD != 0.25 || string(result.Usage) != `{"input_tokens":10}` {
		t.Fatalf("trailing events rewrote the terminal envelope: %#v", result)
	}
	// The trailing events stay in the stream so a late notice such as a usage
	// limit is still diagnosable, and the sequence checkpoint covers them. The
	// last of them is a nested agent finishing after the parent did, which is
	// recorded like the rest and is not a second terminal.
	if len(events) != 6 || result.LastEvent != 6 {
		t.Fatalf("events = %d, last event = %d, want 6 events recorded", len(events), result.LastEvent)
	}
	if !strings.Contains(string(events[2].Payload), "rate_limit_event") {
		t.Fatalf("post-result event payload = %s", events[2].Payload)
	}
	if !strings.Contains(string(events[3].Payload), "trailing chatter") {
		t.Fatalf("post-result message payload = %s", events[3].Payload)
	}
}

// TestRunHonorsTheParentTerminalWhenANestedAgentFinishesFirst replays the
// stream that killed run-841f5ee1866addb533c02a30e67f001a. Its developer had
// spawned subagents, one finished, and its completion arrived in the parent's
// stream as a result envelope — which the parser of the day took for this
// invocation's terminal, so the real terminal was rejected as a second one and
// a finished piece of work was thrown away.
//
// The fixture is what the run recorded. The provider's raw stdout is
// deliberately not retained (see Backend.Run), so the envelopes are rebuilt from
// the normalized events: the system init at sequence 1185, the nested result at
// 1186 with its usage object byte for byte, and the second init at 1187, each
// carrying only fields the record actually holds. The parent's own terminal is
// the one thing that cannot be recovered — the guard fired before it was
// recorded — so it is written here in the shape every genuine terminal in that
// history has.
func TestRunHonorsTheParentTerminalWhenANestedAgentFinishesFirst(t *testing.T) {
	t.Parallel()

	stream, err := os.ReadFile("testdata/run-841f5ee1-nested-agent-result.jsonl")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var events []execution.Event
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: string(stream)}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "resolve the review findings",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The parent's terminal decides the invocation: its text, its reason, its
	// cost. None of that survives if the nested result is taken for a terminal,
	// because the first result seen is the one every later field defers to.
	if result.IsError || result.FinalText != "All three findings resolved." || result.StopReason != "completed" {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.CostUSD != 9.410398 || result.ResolvedModel != "claude-opus-5" {
		t.Fatalf("the nested result displaced the terminal envelope: %#v", result)
	}
	// A nested result is recorded rather than dropped — it is the only evidence
	// a subagent ran at all — but as stream noise, not as this run completing.
	wantTypes := []execution.EventType{
		execution.EventProcessOutput,
		execution.EventRunStarted,
		execution.EventProcessOutput,
		execution.EventRunStarted,
		execution.EventAgentMessage,
		execution.EventRunCompleted,
	}
	gotTypes := make([]execution.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	if !strings.Contains(string(events[2].Payload), `"provider_type":"result"`) {
		t.Fatalf("nested result payload = %s", events[2].Payload)
	}
}

// duplicateTerminalStream replays what killed run-e2b8d016, developing
// yoyodyne-ifd.117.1 on 2026-08-23: the provider ended one invocation twice, and
// the parser of the day rejected the second as a malformed stream, which failed
// a run whose change was all but finished. Recovering it took a triage rerun at
// triage-grant price.
//
// The envelopes are written rather than recorded. The provider's raw stdout is
// deliberately not retained (see Backend.Run), and the parse error ended the
// invocation before anything past the first terminal reached the event log, so
// what the record holds of the duplicate is its existence. It is therefore
// written in the shape a genuine terminal has.
//
// That is not the only shape that reaches this path. What the guard passes on is
// any result envelope after the first that carries a terminal reason or result
// text — one with neither is a nested agent's and is recorded as stream noise
// before the guard sees it — so a subagent whose completion carries result text
// reaches it too, and spends one relaunch on an invocation that had in fact
// finished. That is the same trade the nested-agent discrimination makes, with a
// consequence one relaunch cheaper than the one it replaces: the run used to end.
func duplicateTerminalStream() string {
	terminal := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"Reconciled the disposition table.","total_cost_usd":7.5,"usage":{"input_tokens":10},"terminal_reason":"completed"}`
	return strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5"}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"Reconciled the disposition table."}]}}`,
		terminal,
		terminal,
	}, "\n") + "\n"
}

// A provider that ends one invocation twice has drifted, and drift is a relaunch
// condition rather than a fatality. The invocation is reported as a transient
// death — the class the harness already relaunches within a durable budget, in
// the same worktree and the same session — instead of failing the whole run on a
// parse error.
func TestRunRelaunchesRatherThanFailingOnADuplicateTerminalResult(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: duplicateTerminalStream()}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "reconcile the disposition table",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	// The parse error is what threw the change away. Nothing about a duplicate
	// terminal is unreadable, so nothing about it may end the run.
	if err != nil {
		t.Fatalf("Run() error = %v, want a duplicate terminal to be reported rather than to fail the stream", err)
	}
	if result.TransientFailure == nil {
		t.Fatalf("Run() reported no transient failure, so nothing would relaunch: %#v", result)
	}
	// Both endings travel with the death, because which of them was this
	// invocation's is exactly what the duplicate makes unanswerable.
	wantDetail := `the provider ended this invocation twice, first with "completed" and again with "completed"`
	if result.TransientFailure.Detail != wantDetail {
		t.Fatalf("transient failure detail = %q, want %q", result.TransientFailure.Detail, wantDetail)
	}
	// Neither of the provider's own reasons is the recorded one: the harness
	// names the anomaly rather than picking an ending it cannot tell apart.
	if !result.IsError || result.StopReason != duplicateTerminalReason {
		t.Fatalf("Run() result = %#v, want a failed invocation stopped by %q", result, duplicateTerminalReason)
	}
	// The decided result still stands. The duplicate replaces nothing, so the
	// attempt's session — which is what the relaunch continues in — and the
	// evidence of what it cost are both intact.
	if result.SessionID != "session-1" || result.ResolvedModel != "claude-opus-5" || result.CostUSD != 7.5 {
		t.Fatalf("the duplicate terminal displaced the decided result: %#v", result)
	}
	if result.FinalText != "Reconciled the disposition table." {
		t.Fatalf("FinalText = %q, want the decided terminal's own text", result.FinalText)
	}
	// Nothing about this is a refusal, and reading it as one would park the run
	// waiting for a condition nobody named.
	if result.ServerOverload != nil || result.UsageLimit != nil {
		t.Fatalf("a duplicate terminal became a refusal: overload=%#v limit=%#v", result.ServerOverload, result.UsageLimit)
	}
	// The anomaly is recorded whole, because a dialect that drifted cannot be
	// diagnosed from a record that kept only the fact that it drifted.
	if len(events) != 4 {
		t.Fatalf("events = %d, want the duplicate recorded beside the three the invocation produced", len(events))
	}
	anomaly := string(events[3].Payload)
	if events[3].Type != execution.EventProcessOutput {
		t.Fatalf("duplicate terminal recorded as %q, want stream noise rather than a second completion", events[3].Type)
	}
	for _, want := range []string{`"anomaly":"duplicate_terminal_result"`, `"terminal_reason":"completed"`, `"total_cost_usd":7.5`} {
		if !strings.Contains(anomaly, want) {
			t.Fatalf("duplicate terminal payload is missing %s: %s", want, anomaly)
		}
	}
}

// An overload is the transient death the harness already has a wait for, and the
// two answers are never reported together. A duplicate arriving after one leaves
// that wait to answer the invocation rather than adding a second answer beside
// it.
func TestRunLeavesADuplicateTerminalAfterAnOverloadToTheWait(t *testing.T) {
	t.Parallel()

	duplicated, err := json.Marshal(map[string]any{
		"type":            "result",
		"subtype":         "error",
		"session_id":      "session-1",
		"is_error":        true,
		"terminal_reason": "api_error",
		"result":          overloadedMessage,
		"usage":           map[string]any{},
	})
	if err != nil {
		t.Fatalf("Marshal() duplicate terminal error = %v", err)
	}
	result, _ := runUsageLimitStream(t, terminalErrorStream("api_error", overloadedMessage)+string(duplicated)+"\n")
	if result.ServerOverload == nil {
		t.Fatalf("Run() lost the overload behind the duplicate: %#v", result)
	}
	if result.TransientFailure != nil {
		t.Fatalf("an overload was also reported as a transient death: %#v", result.TransientFailure)
	}
}

// An exhausted limit is the other refusal the harness answers with a wait, and
// unlike an overload it is still moving while the stream runs: the provider
// re-reports as the limit changes, and a serving report supersedes an exhausted
// one. Which answer the invocation carries is therefore decided from the whole
// stream rather than from the order two envelopes happened to arrive in.
func TestRunLeavesADuplicateTerminalBesideAnExhaustedLimitToTheWait(t *testing.T) {
	t.Parallel()

	const exhausted = `{"status":"rejected","resetsAt":1755300000,"rateLimitType":"five_hour"}`
	const duplicate = `{"type":"result","subtype":"error","session_id":"session-1","is_error":true,"terminal_reason":"usage_limit","result":"limit reached","usage":{}}` + "\n"
	const lifted = `{"type":"rate_limit_event","session_id":"session-1","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour"}}` + "\n"

	for _, testCase := range []struct {
		name     string
		stream   string
		wantWait bool
	}{
		{
			// The account will not serve another attempt, so relaunching into it
			// would spend the run's budget on invocations the provider has already
			// declined. The wait reissues into the same worktree and the same
			// session the relaunch would have continued, and costs no attempt.
			name:     "a limit still refusing when the stream ends",
			stream:   usageLimitStream(exhausted) + duplicate,
			wantWait: true,
		},
		{
			// Nothing is left to wait out, so the duplicate is answered the way it
			// is answered on its own. Deciding at the end is what makes this
			// independent of the order the two arrived in.
			name:     "a limit that lifted after the duplicate arrived",
			stream:   usageLimitStream(exhausted) + duplicate + lifted,
			wantWait: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runUsageLimitStream(t, testCase.stream)
			// Whichever answer it carries, the invocation is not trusted to have
			// produced one of its own.
			if !result.IsError || result.StopReason != duplicateTerminalReason {
				t.Fatalf("Run() result = %#v, want a failed invocation stopped by %q", result, duplicateTerminalReason)
			}
			if testCase.wantWait {
				if result.UsageLimit == nil {
					t.Fatalf("Run() lost the exhausted limit behind the duplicate: %#v", result)
				}
				// Two answers to one invocation is one answer too many: which one a
				// run took would depend on the order the caller read them.
				if result.TransientFailure != nil {
					t.Fatalf("a duplicate beside an exhausted limit was also reported as a relaunch: %#v", result.TransientFailure)
				}
				return
			}
			if result.UsageLimit != nil {
				t.Fatalf("a limit that had lifted was still reported as refusing: %#v", result.UsageLimit)
			}
			if result.TransientFailure == nil {
				t.Fatalf("Run() reported no transient failure, so nothing would relaunch: %#v", result)
			}
		})
	}
}

func TestRunClassifiesProviderError(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"Not logged in"}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":true,"terminal_reason":"api_error","result":"Not logged in","total_cost_usd":0,"usage":{}}`,
	}, "\n") + "\n"
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stdout: stream}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.IsError || result.StopReason != "api_error" || result.FinalText != "Not logged in" {
		t.Fatalf("Run() result = %#v", result)
	}
}

func TestRunRedactsDecodedProviderStringsContainingJSONEscapes(t *testing.T) {
	t.Parallel()

	secret := "quoted\"and\\slashed"
	assistant, err := json.Marshal(map[string]any{
		"type":       "assistant",
		"session_id": "session-1",
		"message": map[string]any{"content": []map[string]any{{
			"type": "text",
			"text": "working with " + secret,
		}}},
	})
	if err != nil {
		t.Fatalf("Marshal() assistant error = %v", err)
	}
	completed, err := json.Marshal(map[string]any{
		"type":       "result",
		"subtype":    "success",
		"session_id": "session-1",
		"is_error":   false,
		"result":     "done with " + secret,
		"usage":      map[string]any{"provider_note": secret},
	})
	if err != nil {
		t.Fatalf("Marshal() result error = %v", err)
	}
	stream := string(assistant) + "\n" + string(completed) + "\n"
	if strings.Contains(stream, secret) {
		t.Fatal("test stream did not JSON-escape the secret")
	}
	var events []execution.Event
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		RedactValues:     []string{secret},
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(result.FinalText, secret) || !strings.Contains(result.FinalText, "[REDACTED]") {
		t.Fatalf("FinalText = %q", result.FinalText)
	}
	if result.Process.Stdout != "" {
		t.Fatalf("raw provider JSON was retained: %q", result.Process.Stdout)
	}
	var usage any
	if err := json.Unmarshal(result.Usage, &usage); err != nil {
		t.Fatalf("Unmarshal() usage error = %v", err)
	}
	if strings.Contains(fmt.Sprint(usage), secret) {
		t.Fatalf("usage persisted decoded secret: %s", result.Usage)
	}
	for _, event := range events {
		var payload any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal() event payload error = %v", err)
		}
		if strings.Contains(fmt.Sprint(payload), secret) {
			t.Fatalf("event persisted decoded secret: %s", event.Payload)
		}
	}
}

func TestRunRejectsMalformedOrIncompleteStream(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stream string
		want   string
	}{
		{name: "malformed", stream: "{\n", want: "decode stream event"},
		{name: "missing type", stream: "{}\n", want: "type is required"},
		{name: "no result", stream: `{"type":"system","subtype":"init","session_id":"session-1"}` + "\n", want: "without a result"},
		// A second terminal result is deliberately not in this class. It is a
		// stream this parser can read perfectly well, saying something the
		// provider's dialect should not say, and it is answered by a relaunch —
		// TestRunRelaunchesRatherThanFailingOnADuplicateTerminalResult.
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: test.stream}}}}).Run(context.Background(), backendapi.RunRequest{
				RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunClassifiesCancellation(t *testing.T) {
	t.Parallel()

	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessCancelled, ExitCode: -1}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.IsError || result.StopReason != string(execution.ProcessCancelled) {
		t.Fatalf("Run() result = %#v", result)
	}
}

// The two ways the harness stops a provider invocation on time are reported
// apart, so nothing downstream has to guess whether the process was working.
func TestRunClassifiesAStallApartFromAnExhaustedBudget(t *testing.T) {
	t.Parallel()

	for _, status := range []execution.ProcessStatus{execution.ProcessStalled, execution.ProcessTimedOut} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: status, ExitCode: -1}}}}).Run(context.Background(), backendapi.RunRequest{
				RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !result.IsError || result.StopReason != string(status) {
				t.Fatalf("Run() result = %#v, want the stop named as %q", result, status)
			}
		})
	}
}

// A provider invocation is bounded by both questions: whether it is doing
// anything, which the idle bound answers far sooner, and whether it is worth
// continuing, which the total budget answers.
func TestRunBoundsAProviderInvocationByActivityAndByTotalBudget(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0,
		Stdout: `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"}}}
	if _, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	command := runner.commands[0]
	if command.IdleTimeout != defaultIdleTimeout || command.Timeout != defaultTimeout {
		t.Fatalf("command bounds = idle %s, total %s; want %s and %s", command.IdleTimeout, command.Timeout, defaultIdleTimeout, defaultTimeout)
	}
	// The bound on being stuck has to be far shorter than the bound on being
	// unproductive, or the first is answered by the second all over again.
	if command.IdleTimeout >= command.Timeout {
		t.Fatalf("idle bound %s is not shorter than the total budget %s", command.IdleTimeout, command.Timeout)
	}
}

func TestRunReportsEventSinkFailure(t *testing.T) {
	t.Parallel()

	stream := `{"type":"system","subtype":"init","session_id":"session-1"}` + "\n"
	_, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
		EventSink: func(execution.Event) error { return errors.New("disk full") },
	})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunKeepsReviewersReadOnly(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"{\"decision\":\"approve\",\"summary\":\"fine\"}"}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: "/worktree", Prompt: "review",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "manual", "--name", "yoyodyne-01234567", "--safe-mode", "--exclude-dynamic-system-prompt-sections", "--tools", ""}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}

	for _, test := range []struct {
		name    string
		request backendapi.RunRequest
		want    string
	}{
		{
			name:    "write tool",
			request: backendapi.RunRequest{AllowedTools: []string{"Read", "Edit"}},
			want:    "cannot be granted tools",
		},
		{
			name:    "command execution",
			request: backendapi.RunRequest{AllowedTools: []string{"Bash"}},
			want:    "cannot be granted tools",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := test.request
			request.RunID, request.Role, request.WorkingDirectory, request.Prompt = testRunID, domain.RoleReviewer, "/worktree", "review"
			blocked := &fakeRunner{}
			if _, err := (Backend{Runner: blocked}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want it to contain %q", err, test.want)
			}
			if len(blocked.commands) != 0 {
				t.Fatalf("a rejected reviewer run still started %d process(es)", len(blocked.commands))
			}
		})
	}
}

// A one-shot invocation's whole hope of reading anything back from the
// provider's cache is the prefix it shares with the last one, and Claude Code
// puts the working directory into the cached system prompt above whatever the
// harness appends. A review runs in the developer's worktree, so that one
// section is unique per review and takes the review contract, the persona, and
// every other identical byte behind it off the shared prefix -- which is exactly
// what the recorded runs showed: a cache read of nothing on every review while
// each wrote its whole prompt into the cache.
//
// The developer is deliberately not given it. Its session re-reads its own
// conversation on every turn and already reads nearly all of its input from the
// cache, so what the flag would buy there is a first turn, and what it would
// cost is a change to what a tool-using role is told about its own working
// directory.
func TestAOneShotInvocationSharesItsPrefixAndTheDevelopersDoesNot(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleReviewer, domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"answered"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: "/worktree", Prompt: "judge this",
				SystemPrompt: "the contract",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			args := runner.commands[0].Args
			if !slices.Contains(args, stableSystemPromptFlag) {
				t.Fatalf("a %s invocation carries no %s, so the working directory of the run it was made for decides its cache key: %#v",
					role, stableSystemPromptFlag, args)
			}
			// The flag is a modification of the provider's own default system
			// prompt and its documented behaviour is to be ignored where a caller
			// replaces that prompt outright. The harness appends to it, which is why
			// this works at all, so a later change from appending to replacing would
			// take the fix with it and leave the flag sitting in the arguments
			// looking as though it still did something.
			if !slices.Contains(args, "--append-system-prompt") || slices.Contains(args, "--system-prompt") {
				t.Fatalf("a %s invocation does not append to the default system prompt, so %s is ignored: %#v",
					role, stableSystemPromptFlag, args)
			}
		})
	}

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "make the change",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if slices.Contains(runner.commands[0].Args, stableSystemPromptFlag) {
		t.Fatalf("a developer invocation carries %s, which moves what it is told about its own worktree: %#v",
			stableSystemPromptFlag, runner.commands[0].Args)
	}
}

func TestRunKeepsTheProductManagerAdvisory(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"the brief is thin"}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleProductManager, WorkingDirectory: "/repository", Prompt: "what is missing?",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinalText != "the brief is thin" || result.SessionID != "session-1" {
		t.Fatalf("Run() result = %#v", result)
	}
	// No sandbox settings, because there is nothing to sandbox: the role has no
	// tools at all and cannot apply an edit it proposes.
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "manual", "--name", "yoyodyne-01234567", "--safe-mode", "--exclude-dynamic-system-prompt-sections", "--tools", ""}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}

	for _, test := range []struct {
		name    string
		request backendapi.RunRequest
		want    string
	}{
		{
			name:    "repository writes",
			request: backendapi.RunRequest{AllowedTools: []string{"Write(/**)"}},
			want:    "product-manager runs cannot be granted tools",
		},
		{
			name:    "tracker and git commands",
			request: backendapi.RunRequest{AllowedTools: []string{"Bash"}},
			want:    "product-manager runs cannot be granted tools",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := test.request
			request.RunID, request.Role, request.WorkingDirectory, request.Prompt = testRunID, domain.RoleProductManager, "/repository", "advise"
			blocked := &fakeRunner{}
			if _, err := (Backend{Runner: blocked}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want it to contain %q", err, test.want)
			}
			if len(blocked.commands) != 0 {
				t.Fatalf("a rejected product-manager run still started %d process(es)", len(blocked.commands))
			}
		})
	}
}

// Every role the harness holds a conversation with has to be able to take a
// turn through the default backend. The list is the one the conversation
// machinery keeps rather than a copy of it, because a role added there and left
// out here is the whole failure: the architect carried a contract for days
// before its first message was refused by this backend for a role it did not
// know.
func TestRunServesEveryConversationalRole(t *testing.T) {
	t.Parallel()

	roles := chat.ConversationalRoles()
	if len(roles) == 0 {
		t.Fatal("ConversationalRoles() is empty; the coverage below asserts nothing")
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"answered"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				WorkingDirectory: "/repository",
				Prompt:           "what do you make of this?",
				// The shape a conversation sends whichever role is answering: no
				// tools at all, and no session mode, because it does not get to name
				// one.
				AllowedTools: []string{},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.IsError || result.FinalText != "answered" || result.SessionID != "session-1" {
				t.Fatalf("Run() result = %#v", result)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("the turn started %d process(es), want 1", len(runner.commands))
			}
		})
	}
}

// The two management roles ifd.4 delivered are toolless for the same reason the
// product manager is. Each owns documents and decides what they say, and every
// change either authorizes is recorded by the harness on its behalf, so the
// authority never takes the form of a tool this process could be talked into
// using.
func TestRunKeepsTheManagementRolesToolless(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"the lease is the architect's"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			// No permission mode and no tool list, so the defaults this backend
			// chooses for the role are what get asserted.
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: "/repository", Prompt: "where is the promotion lease recorded?",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "manual", "--name", "yoyodyne-01234567", "--safe-mode", "--exclude-dynamic-system-prompt-sections", "--tools", ""}
			if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
				t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
			}

			for _, test := range []struct {
				name    string
				request backendapi.RunRequest
				want    string
			}{
				{
					name:    "repository writes",
					request: backendapi.RunRequest{AllowedTools: []string{"Write(/**)"}},
					want:    string(role) + " runs cannot be granted tools",
				},
				{
					name:    "tracker and git commands",
					request: backendapi.RunRequest{AllowedTools: []string{"Bash"}},
					want:    string(role) + " runs cannot be granted tools",
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()

					request := test.request
					request.RunID, request.Role, request.WorkingDirectory, request.Prompt = testRunID, role, "/repository", "advise"
					blocked := &fakeRunner{}
					if _, err := (Backend{Runner: blocked}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.want) {
						t.Fatalf("Run() error = %v, want it to contain %q", err, test.want)
					}
					if len(blocked.commands) != 0 {
						t.Fatalf("a rejected %s run still started %d process(es)", role, len(blocked.commands))
					}
				})
			}
		})
	}
}

// A role this backend has decided nothing for is refused rather than served on
// the developer's terms, which are the only terms it would otherwise have.
func TestRunRefusesARoleItHasNoPostureFor(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: "security-reviewer", WorkingDirectory: "/repository", Prompt: "advise",
	})
	if err == nil || !strings.Contains(err.Error(), `does not support role "security-reviewer"`) {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("a refused role still started %d process(es)", len(runner.commands))
	}
}

func TestRunRejectsAllowedToolListDelimiterInjection(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"Read,Edit", "Read Edit", "Read\nEdit"} {
		runner := &fakeRunner{}
		_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
			RunID:            testRunID,
			Role:             domain.RoleDeveloper,
			WorkingDirectory: "/worktree",
			Prompt:           "implement",
			AllowedTools:     []string{tool},
		})
		if err == nil || !strings.Contains(err.Error(), "list delimiters") {
			t.Fatalf("Run() with %q error = %v", tool, err)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("rejected tool rule still started %d process(es)", len(runner.commands))
		}
	}
}

func TestRunRequiresWorktreeScopedDeveloperWriteTools(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"Edit", "Write", "Edit(//tmp/**)", "Write(~/outside/**)"} {
		runner := &fakeRunner{}
		_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
			RunID:            testRunID,
			Role:             domain.RoleDeveloper,
			WorkingDirectory: "/worktree",
			Prompt:           "implement",
			AllowedTools:     []string{"Read", tool},
		})
		if err == nil || !strings.Contains(err.Error(), "must be scoped to the worktree") {
			t.Fatalf("Run() with %s error = %v", tool, err)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("rejected developer run still started %d process(es)", len(runner.commands))
		}
	}
}

func TestCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := (Backend{}).Capabilities()
	if !capabilities.StructuredEvents || !capabilities.SessionResumption || !capabilities.ToolControl || !capabilities.LocalAuth {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
}

type fakeRunner struct {
	results  []execution.ProcessResult
	errors   []error
	commands []execution.Command
	prompts  []string
}

func (f *fakeRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	index := len(f.commands)
	f.commands = append(f.commands, command)
	if command.Stdin != nil {
		data, _ := io.ReadAll(command.Stdin)
		f.prompts = append(f.prompts, string(data))
	}
	if index < len(f.errors) && f.errors[index] != nil {
		return execution.ProcessResult{}, f.errors[index]
	}
	if index >= len(f.results) {
		return execution.ProcessResult{}, errors.New("unexpected process call")
	}
	result := f.results[index]
	if observer != nil {
		for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
			if line != "" {
				observer(execution.Output{Stream: execution.StreamStdout, Text: line, Timestamp: time.Now()})
			}
		}
		for _, line := range strings.Split(strings.TrimSuffix(result.Stderr, "\n"), "\n") {
			if line != "" {
				observer(execution.Output{Stream: execution.StreamStderr, Text: line, Timestamp: time.Now()})
			}
		}
	}
	return result, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
}

// usageLimitStream is a provider stream that reports a rate limit and then ends
// the way the CLI ends when the limit refused the work: a non-zero exit with no
// terminal result of its own.
func usageLimitStream(rateLimitInfo string) string {
	return strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"rate_limit_event","session_id":"session-1","rate_limit_info":` + rateLimitInfo + `}`,
		`{"type":"result","subtype":"error","session_id":"session-1","is_error":true,"terminal_reason":"usage_limit","result":"limit reached","usage":{}}`,
	}, "\n") + "\n"
}

func runUsageLimitStream(t *testing.T, stream string) (backendapi.RunResult, []execution.Event) {
	t.Helper()
	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stdout: stream}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result, events
}

// The exact payload here is the shape the shipped Claude Code CLI builds: a
// required status, an optional whole-second resetsAt, and the provider's own
// name for the limit, alongside overage accounting the harness does not read.
func TestRunReportsAnExhaustedUsageLimitAndPreservesItsWholePayload(t *testing.T) {
	t.Parallel()

	result, events := runUsageLimitStream(t, usageLimitStream(
		`{"status":"rejected","resetsAt":1755300000,"rateLimitType":"five_hour","utilization":1.02,"overageStatus":"rejected","overageDisabledReason":"out_of_credits"}`))
	if result.UsageLimit == nil {
		t.Fatalf("Run() reported no usage limit: %#v", result)
	}
	if result.UsageLimit.Kind != "five_hour" {
		t.Fatalf("usage limit kind = %q, want five_hour", result.UsageLimit.Kind)
	}
	if want := time.Unix(1755300000, 0).UTC(); !result.UsageLimit.ResetsAt.Equal(want) {
		t.Fatalf("usage limit resets at %s, want %s", result.UsageLimit.ResetsAt, want)
	}
	// Stage one of this feature: the payload survives whole. Reducing it to the
	// fields this version reads is what made the shape unanswerable before.
	payload := string(events[1].Payload)
	for _, fragment := range []string{`"status":"rejected"`, `"resetsAt":1755300000`, `"rateLimitType":"five_hour"`, `"utilization":1.02`, `"overageStatus":"rejected"`, `"overageDisabledReason":"out_of_credits"`} {
		if !strings.Contains(payload, fragment) {
			t.Fatalf("rate limit event payload dropped %s: %s", fragment, payload)
		}
	}
}

func TestRunTreatsAServingRateLimitReportAsEvidenceRatherThanExhaustion(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		info string
	}{
		// Most reports arrive on a run with capacity to spare. Reading these as
		// exhaustion would stop runs that are being served.
		{name: "allowed", info: `{"status":"allowed","resetsAt":1755300000,"rateLimitType":"five_hour","utilization":0.4}`},
		{name: "warning", info: `{"status":"allowed_warning","resetsAt":1755300000,"rateLimitType":"five_hour","utilization":0.85}`},
		// A rejected primary limit with overage already in use is still being
		// served; this is the provider's own rule for a hard limit.
		{name: "rejected but served from overage", info: `{"status":"rejected","resetsAt":1755300000,"rateLimitType":"five_hour","isUsingOverage":true}`},
		// A payload the harness cannot read says nothing about capacity, and must
		// not fail the stream either.
		{name: "unreadable payload", info: `"not an object"`},
		{name: "empty payload", info: `{}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, events := runUsageLimitStream(t, usageLimitStream(testCase.info))
			if result.UsageLimit != nil {
				t.Fatalf("a serving rate limit report became an exhausted limit: %#v", result.UsageLimit)
			}
			// It is still recorded, because the payload is the only evidence of
			// what the provider said about capacity.
			if !strings.Contains(string(events[1].Payload), "rate_limit_info") {
				t.Fatalf("rate limit event payload = %s", events[1].Payload)
			}
		})
	}
}

// A limit reported without a usable reset time is still an exhausted limit. The
// harness refuses to guess the wait, not to notice the refusal.
func TestRunReportsAnExhaustedUsageLimitWithNoUsableResetTime(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		info string
	}{
		{name: "absent", info: `{"status":"rejected","rateLimitType":"seven_day"}`},
		{name: "not a number", info: `{"status":"rejected","rateLimitType":"seven_day","resetsAt":"soon"}`},
		{name: "not positive", info: `{"status":"rejected","rateLimitType":"seven_day","resetsAt":0}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runUsageLimitStream(t, usageLimitStream(testCase.info))
			if result.UsageLimit == nil {
				t.Fatalf("Run() reported no usage limit: %#v", result)
			}
			if !result.UsageLimit.ResetsAt.IsZero() {
				t.Fatalf("usage limit invented a reset time: %s", result.UsageLimit.ResetsAt)
			}
		})
	}
}

// The provider re-reports as its limits change, so a later report that the limit
// is serving again supersedes an earlier exhausted one.
func TestRunLetsALaterRateLimitReportSupersedeAnEarlierOne(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"rate_limit_event","session_id":"session-1","rate_limit_info":{"status":"rejected","resetsAt":1755300000,"rateLimitType":"five_hour"}}`,
		`{"type":"rate_limit_event","session_id":"session-1","rate_limit_info":{"status":"allowed","resetsAt":1755300000,"rateLimitType":"five_hour"}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","usage":{}}`,
	}, "\n") + "\n"
	result, _ := runUsageLimitStream(t, stream)
	if result.UsageLimit != nil {
		t.Fatalf("a superseded usage limit survived: %#v", result.UsageLimit)
	}
}

// A transient throttle is the provider CLI's own business: it retries those
// itself and reports them as api_retry. Mistaking one for a hard limit would
// make the harness duplicate a wait the CLI already took.
func TestRunDoesNotTreatATransientRetryAsAnExhaustedUsageLimit(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"system","subtype":"api_retry","session_id":"session-1","attempt":2,"max_retries":5,"error":"rate limited, retrying"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","usage":{}}`,
	}, "\n") + "\n"
	result, events := runUsageLimitStream(t, stream)
	if result.UsageLimit != nil {
		t.Fatalf("an api_retry became an exhausted usage limit: %#v", result.UsageLimit)
	}
	if !strings.Contains(string(events[1].Payload), `"attempt":2`) {
		t.Fatalf("api_retry event lost its retry accounting: %s", events[1].Payload)
	}
}

// overloadedMessage is what the provider CLI wrote, byte for byte, on the two
// runs that failed instantly to a 529 on 2026-08-18. The em dash and the trailing
// status-page sentence are its own; nothing here normalizes them, because a
// recognizer tested against a cleaned-up copy of the evidence is a recognizer
// tested against something the provider never sends.
const overloadedMessage = "API Error: 529 Overloaded. This is a server-side issue, usually temporary — try again in a moment. If it persists, check https://status.claude.com."

// terminalErrorStream ends the way the CLI ends when the API refused it: a
// terminal result carrying the reason and the message it wrote for the operator.
func terminalErrorStream(reason, message string) string {
	encoded, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	return strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"result","subtype":"error","session_id":"session-1","is_error":true,"terminal_reason":"` + reason + `","result":` + string(encoded) + `,"usage":{}}`,
	}, "\n") + "\n"
}

// A terminal 529 is the most retryable condition the provider has, and it is the
// one the harness used to fail a run on outright.
func TestRunReportsATransientServerOverload(t *testing.T) {
	t.Parallel()

	result, _ := runUsageLimitStream(t, terminalErrorStream("api_error", overloadedMessage))
	if result.ServerOverload == nil {
		t.Fatalf("Run() reported no server overload: %#v", result)
	}
	// The provider's own message is the evidence, so it is carried rather than
	// summarized into a category the harness invented.
	if result.ServerOverload.Detail != overloadedMessage {
		t.Fatalf("server overload detail = %q, want the provider's own message", result.ServerOverload.Detail)
	}
	// An overload is not an exhausted account, and describing it as one would send
	// the run into the wrong wait and the operator to the wrong question.
	if result.UsageLimit != nil {
		t.Fatalf("a server overload became an exhausted usage limit: %#v", result.UsageLimit)
	}
}

// Everything else fails the run exactly as it did before. Waiting is only ever
// justified by a refusal the harness actually recognized.
func TestRunTreatsAnUnrecognizedTerminalErrorAsAFailure(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		stream string
	}{
		{name: "another api error", stream: terminalErrorStream("api_error", "API Error: 400 Bad Request. Your request could not be read.")},
		{name: "an authentication failure", stream: terminalErrorStream("api_error", "Not logged in")},
		// The status alone is not the claim: it is a terminal API error saying it
		// that makes a refusal, and prose that merely mentions one is an agent
		// talking about an error rather than the provider reporting one.
		{name: "prose that mentions the status", stream: terminalErrorStream("error_during_execution", "the retry loop I wrote handles API Error: 529 Overloaded")},
		{name: "a refusal the agent itself reported", stream: terminalErrorStream("error_max_turns", overloadedMessage)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runUsageLimitStream(t, testCase.stream)
			if result.ServerOverload != nil {
				t.Fatalf("an unrecognized terminal error became a server overload: %#v", result.ServerOverload)
			}
			if !result.IsError {
				t.Fatalf("Run() lost the reported failure: %#v", result)
			}
		})
	}
}

// connectionClosedMessage is what the provider CLI wrote, byte for byte, on the
// run that died developing yoyodyne-ifd.68.2 on 2026-08-19 — the second time that
// week a person reconciled, reopened, and relaunched a run by hand, and the
// latest of the two. It quotes no HTTP status because nothing answered: the
// transport went away mid-reply.
const connectionClosedMessage = "API Error: Connection closed mid-response. The response above may be incomplete."

// A provider that dies without judging the work is a relaunch rather than the
// end of a run, and the shapes it dies in are what the harness has to recognize.
func TestRunReportsATransientProviderDeath(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		stream string
		detail string
	}{
		{
			name:   "a connection that dropped mid-response",
			stream: terminalErrorStream("api_error", connectionClosedMessage),
			detail: "api_error: " + connectionClosedMessage,
		},
		{
			// Every record of this shape in the local run history reads exactly like
			// this: the category and nothing else. That it says so little is not a
			// reason to read it as a judgement of the work.
			name:   "the bare category the older records carry",
			stream: terminalErrorStream("api_error", ""),
			detail: "api_error",
		},
		{
			name:   "a server-side status that named no wait",
			stream: terminalErrorStream("api_error", "API Error: 500 Internal Server Error"),
			detail: "api_error: API Error: 500 Internal Server Error",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runUsageLimitStream(t, testCase.stream)
			if result.TransientFailure == nil {
				t.Fatalf("Run() reported no transient failure: %#v", result)
			}
			// The provider's own account travels with the death, because the
			// category alone leaves whoever reads the blocker afterwards with no
			// idea which of these happened.
			if result.TransientFailure.Detail != testCase.detail {
				t.Fatalf("transient failure detail = %q, want %q", result.TransientFailure.Detail, testCase.detail)
			}
			// None of these is a refusal, and describing one as either would send
			// the run into a wait for a condition nobody named.
			if result.ServerOverload != nil || result.UsageLimit != nil {
				t.Fatalf("a transient death became a refusal: overload=%#v limit=%#v", result.ServerOverload, result.UsageLimit)
			}
		})
	}
}

// An overload is the transient death the harness already has a wait for. Two
// answers to the same terminal would leave which one a run took depending on the
// order the caller happened to read them.
func TestRunReportsAnOverloadOnlyAsAnOverload(t *testing.T) {
	t.Parallel()

	result, _ := runUsageLimitStream(t, terminalErrorStream("api_error", overloadedMessage))
	if result.ServerOverload == nil {
		t.Fatalf("Run() reported no server overload: %#v", result)
	}
	if result.TransientFailure != nil {
		t.Fatalf("an overload was also reported as a transient death: %#v", result.TransientFailure)
	}
}

// A refusal that stands is not weather. Relaunching would put the identical
// request in front of the provider and earn the identical answer, so these fail
// the run exactly as they always have.
func TestRunDoesNotRelaunchOnARefusalThatStands(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		stream string
	}{
		{name: "a request the provider could not read", stream: terminalErrorStream("api_error", "API Error: 400 Bad Request. Your request could not be read.")},
		{name: "a key that is not permitted", stream: terminalErrorStream("api_error", "API Error: 403 Forbidden")},
		{name: "a limit the provider is enforcing", stream: terminalErrorStream("api_error", "API Error: 429 Too Many Requests")},
		// The terminal reason is the claim. An agent that ended some other way is
		// reporting on its own work, however much its message looks like an API's.
		{name: "an ending that was not the API's", stream: terminalErrorStream("error_during_execution", connectionClosedMessage)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runUsageLimitStream(t, testCase.stream)
			if result.TransientFailure != nil {
				t.Fatalf("a refusal that stands became a relaunch: %#v", result.TransientFailure)
			}
			if !result.IsError {
				t.Fatalf("Run() lost the reported failure: %#v", result)
			}
		})
	}
}

// The CLI retries an overload ten times on its own before it gives up. Those
// retries are its business and stay its business: only the terminal result it
// ends on asks the harness for anything.
func TestRunDoesNotTreatATransientRetryAsAServerOverload(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"system","subtype":"api_retry","session_id":"session-1","attempt":1,"max_retries":10,"error":"overloaded"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","usage":{}}`,
	}, "\n") + "\n"
	result, _ := runUsageLimitStream(t, stream)
	if result.ServerOverload != nil {
		t.Fatalf("an api_retry became a server overload: %#v", result.ServerOverload)
	}
}

func TestRunRedactsSecretsInsideARateLimitPayload(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	_, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stdout: usageLimitStream(
			`{"status":"rejected","resetsAt":1755300000,"rateLimitType":"five_hour","overageDisabledReason":"super-secret-value"}`)}}},
		Clock: fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		RedactValues:     []string{"super-secret-value"},
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(string(events[1].Payload), "super-secret-value") {
		t.Fatalf("rate limit payload leaked a redacted value: %s", events[1].Payload)
	}
}

// TestTheReplyReachesAWatcherRedactedRecordedAndBeforeTheResult is the whole
// contract the streaming display rests on. What a watcher may be shown is
// bounded at both ends: it has been through the redactor, and the event that
// records it has already been persisted, so nothing can be put on a screen that
// the durable record does not hold.
func TestTheReplyReachesAWatcherRedactedRecordedAndBeforeTheResult(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-value"
	assistant, err := json.Marshal(map[string]any{
		"type":       "assistant",
		"session_id": "session-1",
		"message":    map[string]any{"content": []any{map[string]any{"type": "text", "text": "the token is " + secret}}},
	})
	if err != nil {
		t.Fatalf("Marshal() assistant error = %v", err)
	}
	stream := string(assistant) + "\n" +
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"the token is [REDACTED]"}` + "\n"

	// recorded counts the events persisted when each fragment arrived, so
	// "recorded first, shown second" is an assertion about order rather than a
	// claim about the code.
	var events []execution.Event
	var fragments []string
	var recordedWhenShown []int
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleProductManager,
		WorkingDirectory: "/repository",
		Prompt:           "what next?",
		AllowedTools:     []string{},
		RedactValues:     []string{secret},
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
		ReplySink: func(fragment string) {
			fragments = append(fragments, fragment)
			recordedWhenShown = append(recordedWhenShown, len(events))
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(fragments) != 1 {
		t.Fatalf("fragments = %#v, want the one assistant message", fragments)
	}
	if strings.Contains(fragments[0], secret) || fragments[0] != "the token is [REDACTED]" {
		t.Fatalf("a watcher was shown %q", fragments[0])
	}
	// The fragment arrived after the event that records it and before the
	// terminal result the turn is built from, which is the whole reason there is
	// anything to show early.
	if recordedWhenShown[0] != 1 {
		t.Fatalf("the fragment was shown with %d event(s) recorded, want the one that records it", recordedWhenShown[0])
	}
	if events[len(events)-1].Type != execution.EventRunCompleted {
		t.Fatalf("last event = %s, want the terminal result after the fragment", events[len(events)-1].Type)
	}
	if result.FinalText != "the token is [REDACTED]" {
		t.Fatalf("FinalText = %q", result.FinalText)
	}
}

// TestAWatchedInvocationRecordsAndReturnsWhatAnUnwatchedOneDoes is the promise
// that showing a reply as it forms is presentation and nothing else. The same
// stream is parsed with and without somebody watching, and the events and the
// result are compared.
func TestAWatchedInvocationRecordsAndReturnsWhatAnUnwatchedOneDoes(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test","permissionMode":"plan"}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"Two goals, then."}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"Two goals, then.","total_cost_usd":0.0125}`,
	}, "\n") + "\n"

	run := func(watch bool) ([]execution.Event, backendapi.RunResult) {
		t.Helper()

		var events []execution.Event
		request := backendapi.RunRequest{
			RunID:            testRunID,
			Role:             domain.RoleProductManager,
			WorkingDirectory: "/repository",
			Prompt:           "what next?",
			AllowedTools:     []string{},
			EventSink: func(event execution.Event) error {
				events = append(events, event)
				return nil
			},
		}
		if watch {
			request.ReplySink = func(string) {}
		}
		result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}}).Run(context.Background(), request)
		if err != nil {
			t.Fatalf("Run(watch=%v) error = %v", watch, err)
		}
		return events, result
	}

	watchedEvents, watchedResult := run(true)
	unwatchedEvents, unwatchedResult := run(false)
	if len(watchedEvents) != len(unwatchedEvents) {
		t.Fatalf("recorded %d event(s) watched and %d unwatched", len(watchedEvents), len(unwatchedEvents))
	}
	for index := range watchedEvents {
		if watchedEvents[index].Type != unwatchedEvents[index].Type ||
			string(watchedEvents[index].Payload) != string(unwatchedEvents[index].Payload) {
			t.Fatalf("event %d differs: watched %s %s, unwatched %s %s", index,
				watchedEvents[index].Type, watchedEvents[index].Payload,
				unwatchedEvents[index].Type, unwatchedEvents[index].Payload)
		}
	}
	if !reflect.DeepEqual(watchedResult, unwatchedResult) {
		t.Fatalf("watching changed the result:\nwatched   %#v\nunwatched %#v", watchedResult, unwatchedResult)
	}
}
