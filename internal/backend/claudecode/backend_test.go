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

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
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
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--name", "yoyodyne-01234567", "--settings", developerSandboxSettings, "--allowedTools", "Bash,Read,Edit(/**),Write(/**),Glob,Grep"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
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
	wantArgs := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan", "--name", "yoyodyne-01234567", "--safe-mode", "--allowedTools", "Glob,Grep,Read"}
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
			want:    `write-capable tool "Edit"`,
		},
		{
			name:    "command execution",
			request: backendapi.RunRequest{AllowedTools: []string{"Bash"}},
			want:    `write-capable tool "Bash"`,
		},
		{
			name:    "editing permission mode",
			request: backendapi.RunRequest{AllowedTools: []string{"Read"}, PermissionMode: "acceptEdits"},
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
