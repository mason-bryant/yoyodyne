package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
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
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--name", "yoyodyne-01234567", "--settings", developerSandboxSettings, "--allowedTools", "Bash", "Read", "Edit(/**)", "Write(/**)", "Glob", "Grep"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
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
	// limit is still diagnosable, and the sequence checkpoint covers them.
	if len(events) != 5 || result.LastEvent != 5 {
		t.Fatalf("events = %d, last event = %d, want 5 events recorded", len(events), result.LastEvent)
	}
	if !strings.Contains(string(events[2].Payload), "rate_limit_event") {
		t.Fatalf("post-result event payload = %s", events[2].Payload)
	}
	if !strings.Contains(string(events[3].Payload), "trailing chatter") {
		t.Fatalf("post-result message payload = %s", events[3].Payload)
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
		{name: "second result", stream: `{"type":"result","subtype":"success","session_id":"session-1","result":"done","usage":{}}` + "\n" + `{"type":"result","subtype":"success","session_id":"session-2","is_error":true,"result":"replaced","usage":{}}` + "\n", want: "second provider terminal result"},
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
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan", "--name", "yoyodyne-01234567", "--safe-mode", "--tools", ""}
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
		{
			name:    "editing permission mode",
			request: backendapi.RunRequest{AllowedTools: []string{}, PermissionMode: "acceptEdits"},
			want:    "reviewer runs require the read-only",
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
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan", "--name", "yoyodyne-01234567", "--safe-mode", "--tools", ""}
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
		{
			name:    "editing permission mode",
			request: backendapi.RunRequest{AllowedTools: []string{}, PermissionMode: "acceptEdits"},
			want:    "product-manager runs require the read-only",
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
		PermissionMode:   "plan",
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
			PermissionMode:   "plan",
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
