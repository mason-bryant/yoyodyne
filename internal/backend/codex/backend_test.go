package codex

import (
	"context"
	"encoding/json"
	"errors"
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

// usageRow is the terminal's usage object read back under the names the
// harness's own price reader uses, which is the point of reading it here: a
// backend that wrote the provider's spelling instead would price every cache
// read as a fresh one and nothing on the reading side would notice.
type usageRow struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_input_tokens"`
}

func TestCheckAvailability(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: "Logged in using ChatGPT\n"},
	}}
	availability, err := (Backend{Runner: runner}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated || availability.Version != "codex-cli 0.44.0" {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
	if availability.AuthMethod != "chatgpt" || availability.APIProvider != "openai" {
		t.Fatalf("CheckAvailability() credential = %#v", availability)
	}
	if runner.commands[0].Args[0] != "--version" || !reflect.DeepEqual(runner.commands[1].Args, []string{"login", "status"}) {
		t.Fatalf("availability asked %#v", runner.commands)
	}
}

// The login state is the command's exit status rather than its prose, because
// the status is the part of the answer that does not move between versions. A
// sentence this adapter cannot read leaves the credential unnamed instead of
// leaving the account misreported.
func TestCheckAvailabilityReadsTheLoginStateFromTheExitStatus(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		login         execution.ProcessResult
		authenticated bool
		method        string
	}{
		{
			name:          "an api key",
			login:         execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Logged in using an API key\n"},
			authenticated: true,
			method:        "api-key",
		},
		{
			name:  "nobody logged in",
			login: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: "Not logged in\n"},
		},
		{
			name:          "a sentence this version does not recognize",
			login:         execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Authenticated somehow\n"},
			authenticated: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{results: []execution.ProcessResult{
				{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
				test.login,
			}}
			availability, err := (Backend{Runner: runner}).CheckAvailability(context.Background())
			if err != nil {
				t.Fatalf("CheckAvailability() error = %v", err)
			}
			if availability.Authenticated != test.authenticated || availability.AuthMethod != test.method {
				t.Fatalf("CheckAvailability() = %#v, want authenticated %t by %q", availability, test.authenticated, test.method)
			}
		})
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

// A diagnosis asks one pooled account whether it is authenticated, so both
// questions have to be asked in that account's own provider home. Asking where
// the machine happens to be signed in would report an account as healthy on
// somebody else's login.
func TestCheckAvailabilityAsksTheHomeThisValueWasBuiltFor(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"},
	}}
	if _, err := (Backend{Runner: runner, ConfigDir: "/homes/two"}).CheckAvailability(context.Background()); err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	for _, command := range runner.commands {
		if !hasEnvironment(command.Env, providerHomeVariable+"=/homes/two") {
			t.Fatalf("%v was asked outside the account's own provider home", command.Args)
		}
	}

	// And naming no home asks where the machine is signed in, which is what a
	// single-account installation has always done and what the account plumbing
	// must not cost it.
	plain := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"},
	}}
	if _, err := (Backend{Runner: plain}).CheckAvailability(context.Background()); err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if plain.commands[0].Env != nil {
		t.Fatalf("an installation naming no account was given an environment: %v", plain.commands[0].Env)
	}
}

func TestRunNormalizesTheProviderStream(t *testing.T) {
	t.Parallel()

	stream := lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-test"}}`,
		`{"id":"1","msg":{"type":"task_started"}}`,
		`{"id":"2","msg":{"type":"agent_message_delta","delta":"wor"}}`,
		`{"id":"3","msg":{"type":"agent_message","message":"working"}}`,
		`{"id":"4","msg":{"type":"exec_command_begin","call_id":"call-1","command":["bash","-lc","cat secrets.txt"],"cwd":"/worktree"}}`,
		`{"id":"5","msg":{"type":"exec_command_end","call_id":"call-1","exit_code":0,"stdout":"contents of the file","stderr":""}}`,
		`{"id":"6","msg":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":7}}}}`,
		`{"id":"7","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	)
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
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
	if result.IsError || result.FinalText != "done" || result.SessionID != "session-1" || result.ResolvedModel != "gpt-test" {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.Backend != domain.BackendCodex {
		t.Fatalf("Run() recorded backend %q", result.Backend)
	}
	// A record has to be able to tell two harness builds reading one provider
	// apart, which is what the adapter version beside the provider's name is for.
	if result.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("Run() recorded adapter version %q, want %q", result.AdapterVersion, backendapi.CodexAdapterVersion)
	}
	wantTypes := []execution.EventType{
		execution.EventRunStarted,
		execution.EventProcessOutput, // task_started
		execution.EventAgentMessage,
		execution.EventCommandStarted,
		execution.EventCommandCompleted,
		execution.EventProcessOutput, // token_count
		execution.EventRunCompleted,
	}
	gotTypes := make([]execution.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	// The delta is dropped rather than recorded: it is the same text again in
	// pieces, and a log holding both says nothing extra while being harder to
	// read.
	for _, event := range events {
		if strings.Contains(string(event.Payload), `"wor"`) {
			t.Fatalf("a streamed fragment reached the event log: %s", event.Payload)
		}
	}
	// A shell line and its output are provider-authored text that may quote
	// anything in the worktree, so the record keeps their size and not them.
	for _, event := range events {
		payload := string(event.Payload)
		if strings.Contains(payload, "secrets.txt") || strings.Contains(payload, "contents of the file") {
			t.Fatalf("a command payload persisted the command or its output: %s", payload)
		}
	}
	if runner.prompts[0] != "implement the task" {
		t.Fatalf("prompt = %q", runner.prompts[0])
	}
	wantArgs := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", sandboxWorkspaceWrite, "-"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}
	if runner.commands[0].Dir != "/worktree" {
		t.Fatalf("the invocation ran in %q", runner.commands[0].Dir)
	}
}

// Codex reports what an invocation read and wrote and never what it cost. The
// two are separate facts: an invocation nobody priced and one priced at nothing
// are opposite answers to anything adding costs up, so the result says the cost
// was never reported rather than saying it was zero.
func TestATerminalCarriesTokensAndNoPrice(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-test"}}`,
		`{"id":"1","msg":{"type":"token_count","input_tokens":11,"cached_input_tokens":5,"output_tokens":3}}`,
		`{"id":"2","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if result.CostReported || result.CostUSD != 0 {
		t.Fatalf("Run() priced an invocation the provider never priced: %#v", result)
	}
	terminal := events[len(events)-1]
	var payload struct {
		Role         string    `json:"role"`
		Usage        *usageRow `json:"usage"`
		TotalCostUSD *float64  `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	// The role is what makes the tokens beside it attributable: a run's log holds
	// the developer's invocations and the reviewer's, and where a terminal sits in
	// it is a fact about the order the harness happened to do things in.
	if payload.Role != string(domain.RoleDeveloper) {
		t.Fatalf("terminal role = %q, want the role the invocation was made as", payload.Role)
	}
	if payload.TotalCostUSD != nil {
		t.Fatalf("terminal carries a price the provider never stated: %v", *payload.TotalCostUSD)
	}
	// Codex calls its cached reads `cached_input_tokens`. Renaming it to the name
	// the harness's price reader looks for is what stops a run counting every
	// cache read as a fresh one.
	if payload.Usage == nil || payload.Usage.InputTokens != 11 || payload.Usage.OutputTokens != 3 || payload.Usage.CacheReadTokens != 5 {
		t.Fatalf("terminal usage = %+v, want the provider's counts under the names the price reader uses", payload.Usage)
	}
}

// A terminal carrying no token counts leaves the usage object out rather than
// writing an empty one: an invocation nobody has a measurement for and one
// measured at nothing are different facts.
func TestATerminalWithNoCountsCarriesNoUsage(t *testing.T) {
	t.Parallel()

	_, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
		`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if payload := string(events[len(events)-1].Payload); strings.Contains(payload, `"usage"`) {
		t.Fatalf("terminal payload = %s, want no usage object at all", payload)
	}
}

// The sandbox an invocation runs under is decided by what the role's tool
// posture requires and by nothing else. There is no permission mode on a request
// for a caller to name one with, so this is the whole of the mapping: a
// worktree-write role gets a sandbox it can edit in, and a read-only one does
// not.
func TestTheSandboxIsWhatTheRolesPostureRequires(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		role domain.AgentRole
		want string
	}{
		{name: "a developer", role: domain.RoleDeveloper, want: sandboxWorkspaceWrite},
		// The built-in Codex descriptor claims only the worktree-write posture, so
		// no built-in invocation is a reviewer. This is the branch a provider a
		// project declared on this adapter reaches when it claims the read-only
		// posture, and it must not be a writable worktree.
		{name: "a role whose posture is read-only", role: domain.RoleReviewer, want: sandboxReadOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{results: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"{}"}}`),
			}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             test.role,
				WorkingDirectory: "/worktree",
				Prompt:           "do the work",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := sandboxArgument(t, runner.commands[0].Args); got != test.want {
				t.Fatalf("sandbox = %q, want %q", got, test.want)
			}
		})
	}
}

// Every role the harness has is decided here, one way or the other: mapped onto
// a sandbox, or refused because this provider has nothing to hold it to. A role
// this adapter neither maps nor refuses is one an invocation meets only after
// work has been claimed, which is the opposite of a policy refused where the
// configuration is validated.
func TestEveryRoleTheHarnessHasIsDecided(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		sandbox, err := sandboxFor(role)
		switch {
		case err != nil && sandbox != "":
			t.Errorf("role %q was refused and given sandbox %q", role, sandbox)
		case err == nil && sandbox != sandboxReadOnly && sandbox != sandboxWorkspaceWrite:
			t.Errorf("role %q mapped to sandbox %q, which is not one this adapter asks for", role, sandbox)
		}
	}
	// And nothing may reach the setting that would let an agent write anywhere on
	// the machine: it is unreachable from here rather than merely unused.
	for _, role := range domain.Roles() {
		if sandbox, _ := sandboxFor(role); sandbox == "danger-full-access" {
			t.Errorf("role %q reached the sandbox that is not a bound", role)
		}
	}
}

// A role or a policy this adapter cannot hold is refused before the provider is
// ever started, which is the same claim the configuration makes when it refuses
// the combination before work is assigned.
func TestRunRefusesRolesAndPoliciesItCannotHold(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request backendapi.RunRequest
		wanted  string
	}{
		{
			// Which roles this backend serves is the registry's to say and the
			// configuration's to refuse, before any work is assigned; what this
			// adapter refuses is a role it could not assemble an invocation for at
			// all, because the only sandbox left to default to would be the
			// developer's.
			name:    "a name that is not a role at all",
			request: backendapi.RunRequest{Role: "security-reviewer"},
			wanted:  `does not support role "security-reviewer"`,
		},
		{
			// Codex has no per-tool control, so a caller that asked for a narrower
			// set than the sandbox gives would otherwise get a wider one and be
			// told nothing.
			name:    "a tool list this provider cannot scope",
			request: backendapi.RunRequest{Role: domain.RoleDeveloper, AllowedTools: []string{"Read"}},
			wanted:  "cannot be granted a tool list",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{}
			request := test.request
			request.RunID = testRunID
			request.WorkingDirectory = "/worktree"
			request.Prompt = "do the work"
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("Run() error = %v, want it to contain %q", err, test.wanted)
			}
			if len(runner.commands) != 0 {
				t.Fatalf("the provider was started for a request that should have been refused: %#v", runner.commands)
			}
		})
	}
}

// Resuming continues the provider's own session. It is an acceleration and never
// the record: what the harness knows about this work is in its own durable
// state, so a session the provider has forgotten costs context rather than work.
func TestRunResumesTheProvidersSession(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
			`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "carry on",
		SessionID:        "session-1",
		Model:            "gpt-test",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantArgs := []string{"exec", "resume", "session-1", "--json", "--skip-git-repo-check", "--sandbox", sandboxWorkspaceWrite, "--model", "gpt-test", "-"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}
}

// The account an invocation is made under is the request's, falling back to the
// one this backend value was built for. Beside it the invocation carries the Go
// build cache pointed somewhere the run may write, because a developer's first
// act is to execute the project's checks and the default cache is under a home
// the run's sandbox does not grant.
func TestAnInvocationIsMadeUnderTheAccountItWasGiven(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		onValue   string
		onRequest string
		want      string
	}{
		{name: "the request's account", onValue: "/homes/one", onRequest: "/homes/two", want: "/homes/two"},
		{name: "the value's account where the request names none", onValue: "/homes/one", want: "/homes/one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{results: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
			}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: test.onValue}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             domain.RoleDeveloper,
				WorkingDirectory: "/worktree",
				Prompt:           "implement",
				AccountConfigDir: test.onRequest,
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !hasEnvironment(runner.commands[0].Env, providerHomeVariable+"="+test.want) {
				t.Fatalf("the invocation was not made in %q: %v", test.want, runner.commands[0].Env)
			}
			if !hasEnvironmentName(runner.commands[0].Env, "GOCACHE") {
				t.Fatalf("the invocation carried no build cache the run may write: %v", runner.commands[0].Env)
			}
		})
	}
}

// Codex has no separate channel for a system prompt, so the role's contract is
// prepended to the prompt rather than dropped: a role invoked without the
// contract it was given is a role doing some other job.
func TestTheRolesContractReachesTheProvider(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		SystemPrompt:     "you are the developer",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.prompts[0] != "you are the developer\n\nimplement the task" {
		t.Fatalf("prompt = %q, want the contract in front of the work", runner.prompts[0])
	}
}

// An invocation the provider ended badly is a failure carrying the provider's
// own words, and what kind of failure it was is the dialect's answer rather than
// this adapter's.
func TestRunRecordsAProviderError(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
		`{"id":"1","msg":{"type":"error","message":"stream failed: 500 internal server error"}}`,
	))
	if !result.IsError || result.StopReason != eventError {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.ServerOverload == nil {
		t.Fatalf("a transiently unavailable server was not read as one: %#v", result)
	}
	if events[len(events)-1].Type != execution.EventRunFailed {
		t.Fatalf("terminal event = %q, want a failure", events[len(events)-1].Type)
	}
}

// A provider that stopped without saying how the invocation ended has produced
// no outcome, and the run fails with exactly that rather than with an answer
// nobody gave.
func TestRunFailsWhenNoTerminalArrives(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`),
	}}}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err == nil || !strings.Contains(err.Error(), "without a terminal event") {
		t.Fatalf("Run() error = %v", err)
	}
}

// A line this adapter cannot read is recorded rather than fatal: Codex writes to
// standard output and nothing guarantees every line there is one of its events,
// so a banner must not fail a run whose work was fine. Nothing is lost by being
// lenient, because a stream that says nothing readable still produces no
// terminal and still fails.
func TestAnUnreadableLineIsRecordedRatherThanFatal(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`Reading prompt from stdin...`,
		`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if result.IsError || result.FinalText != "done" {
		t.Fatalf("Run() result = %#v", result)
	}
	if events[0].Type != execution.EventProcessOutput || !strings.Contains(string(events[0].Payload), "Reading prompt") {
		t.Fatalf("the unreadable line was not recorded: %s", events[0].Payload)
	}
}

// A line the runner had to cut is not an envelope any more, and failing the
// invocation over one line the harness could not hold would be a self-inflicted
// death. It is recorded as the anomaly it is, named so a reader can tell it from
// a line the provider wrote badly, and the stream carries on.
func TestACutLineIsRecordedAsAnAnomalyAndTheStreamCarriesOn(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	result, err := (Backend{
		Runner: &cuttingRunner{
			cut:   `{"id":"0","msg":{"type":"agent_message","message":"a very long`,
			after: `{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`,
		},
		Clock: fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText != "done" {
		t.Fatalf("Run() result = %#v, want the invocation the rest of the stream finished", result)
	}
	if payload := string(events[0].Payload); !strings.Contains(payload, truncatedStreamLine) {
		t.Fatalf("the cut line was recorded as ordinary output: %s", payload)
	}
}

// The provider keeps writing after the terminal. Those events are recorded so
// their payload stays diagnosable, and none of them may replace the outcome that
// was already decided.
func TestNothingAfterTheTerminalReplacesTheOutcome(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`,
		`{"id":"1","msg":{"type":"error","message":"something afterwards"}}`,
		`{"id":"2","msg":{"type":"shutdown_complete"}}`,
	))
	if result.IsError || result.FinalText != "done" || result.StopReason != eventTaskComplete {
		t.Fatalf("Run() result = %#v, want the first terminal standing", result)
	}
	if payload := string(events[1].Payload); !strings.Contains(payload, "terminal_after_terminal") {
		t.Fatalf("a second terminal was recorded as ordinary output: %s", payload)
	}
}

// Nothing the harness was told to keep out of a durable record may reach one
// through the provider's own stream.
func TestRunRedactsWhatTheProviderEchoesBack(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{
			Status: execution.ProcessSucceeded,
			Stdout: lines(`{"id":"0","msg":{"type":"agent_message","message":"the token is sk-secret-value"}}`,
				`{"id":"1","msg":{"type":"task_complete","last_agent_message":"used sk-secret-value"}}`),
		}}},
		Clock: fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		RedactValues:     []string{"sk-secret-value"},
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(result.FinalText, "sk-secret-value") {
		t.Fatalf("the result kept a redacted value: %q", result.FinalText)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "sk-secret-value") {
			t.Fatalf("an event kept a redacted value: %s", event.Payload)
		}
	}
}

// The adapter reads its own description from the same place a configuration is
// validated against it, so the two cannot drift apart.
func TestCapabilitiesAreTheOnesTheHarnessValidatesAgainst(t *testing.T) {
	t.Parallel()

	descriptor, known := backendapi.BuiltInDescriptor(domain.BackendCodex)
	if !known {
		t.Fatal("this build ships no description of the Codex backend")
	}
	if (Backend{}).Capabilities() != descriptor.Capabilities {
		t.Fatalf("Capabilities() = %#v, want %#v", (Backend{}).Capabilities(), descriptor.Capabilities)
	}
	// Codex enforces no schema on what an agent finally says, so the description
	// must not claim it does: a capability nobody has is how a role comes to be
	// given work it cannot do.
	if descriptor.Capabilities.StructuredOutput {
		t.Fatal("the Codex description claims structured output, which this provider does not enforce")
	}
	// And the description says this build can launch it, which is what makes a
	// Codex endpoint runnable rather than expressed and refused.
	if !descriptor.Runnable() || descriptor.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("the Codex description = %#v, want the adapter this build ships", descriptor)
	}
}

// A project that declared a provider running on this adapter is a different
// backend from the one that ships here, and every run, conversation, and line of
// spend has to say which one it was.
func TestADeclaredProviderIsRecordedUnderItsOwnName(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}, Provider: "my-harness", Binary: "my-harness"}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Backend != "my-harness" {
		t.Fatalf("Run() recorded backend %q, want the backend the agent named", result.Backend)
	}
	// The provider is the declaration's and the adapter version is this build's:
	// they are separate facts, and a record carrying only the first cannot say
	// which harness code read what the provider said.
	if result.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("Run() recorded adapter version %q, want this build's", result.AdapterVersion)
	}
	if runner.commands[0].Name != "my-harness" {
		t.Fatalf("the invocation ran %q, want the executable the declaration named", runner.commands[0].Name)
	}
}

func runStream(t *testing.T, role domain.AgentRole, stream string) (backendapi.RunResult, []execution.Event) {
	t.Helper()

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             role,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result, events
}

func sandboxArgument(t *testing.T, args []string) string {
	t.Helper()

	for index, arg := range args {
		if arg == "--sandbox" && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("no sandbox in %#v", args)
	return ""
}

func hasEnvironment(environment []string, entry string) bool {
	for _, value := range environment {
		if value == entry {
			return true
		}
	}
	return false
}

func hasEnvironmentName(environment []string, name string) bool {
	for _, value := range environment {
		if strings.HasPrefix(value, name+"=") {
			return true
		}
	}
	return false
}

func lines(values ...string) string {
	return strings.Join(values, "\n") + "\n"
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

// cuttingRunner is a process runner that reports its first line as one it had to
// cut at the per-line bound, which is the one thing the fake above cannot say.
type cuttingRunner struct {
	cut   string
	after string
}

func (c *cuttingRunner) Run(_ context.Context, _ execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	observer(execution.Output{Stream: execution.StreamStdout, Text: c.cut, LineTruncated: true, Timestamp: time.Now()})
	observer(execution.Output{Stream: execution.StreamStdout, Text: c.after, Timestamp: time.Now()})
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
}
