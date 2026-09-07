package codex

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// longFlag matches an option as the CLI's own help lists it, which is how the
// set of flags it knows is read rather than written down here a second time.
var longFlag = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9-]*`)

// Every flag this backend passes has to be one the installed CLI knows, and a
// test driving a fake process cannot tell a correct flag from a misspelled one.
// An unknown option is not ignored: the CLI refuses the whole invocation before
// it reaches the provider, so a flag wrong here — or right but newer than the
// installed CLI — fails every invocation of every role that receives it.
//
// The arguments come from the backend rather than from a list repeated here, so
// a flag added later is covered without anybody remembering to cover it. Unlike
// the conformance run below this costs nothing — `--help` makes no provider call
// and needs no account — so it is gated on the CLI being installed rather than
// on opting in, and skips where it is not.
func TestTheInstalledCLIKnowsEveryFlagThisBackendPasses(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("Codex is not installed, so what it accepts cannot be asked here: %v", err)
	}
	help, err := exec.Command(binary, "exec", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("codex exec --help error = %v: %s", err, help)
	}
	known := make(map[string]bool)
	for _, flag := range longFlag.FindAllString(string(help), -1) {
		known[flag] = true
	}
	if !known["--json"] && !known["--sandbox"] {
		t.Fatalf("codex exec --help listed no recognizable options, so this asserts nothing: %s", help)
	}

	// A request that sets each optional field, so no branch of the argument
	// assembly goes unasked.
	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		SystemPrompt:     "the contract",
		SessionID:        "session-1",
		Model:            "gpt-5",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, argument := range runner.commands[0].Args {
		// A flag is an argument that begins with one, so a value that happens to
		// contain a dash pair -- a model selector, a prompt -- is never mistaken
		// for an option the CLI has to know.
		if !strings.HasPrefix(argument, "--") {
			continue
		}
		if !known[argument] {
			t.Fatalf("an invocation passes %q, which %s does not list; an unknown option makes the CLI refuse the whole invocation",
				argument, binary)
		}
	}
}

// The stream vocabulary this adapter reads was taken from the provider's
// documented protocol rather than from a recorded run, so the one thing the unit
// tests cannot show is that a real CLI still speaks it. This is where that is
// checked, against whatever Codex is installed on the machine running it.
//
// It is opt-in for the reason the Claude Code conformance check is: it starts a
// real provider, spends real capacity, and depends on an account this repository
// does not own. What it is not is optional evidence — a change to the flags, the
// sandbox mapping, or the event names is a change nothing else here can catch.
func TestLocalConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CODEX_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CODEX_CONFORMANCE=1 to run against the installed Codex CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Codex unavailable or unauthenticated: %#v", availability)
	}

	// The developer, because it is the only role the built-in Codex description
	// can be held to a posture for: its sandbox scopes writes to a directory, and
	// has no setting for the read-only posture the advisory roles require.
	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		Timeout:          5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}
	// The session is what a later invocation resumes, and the resolved model is
	// the only durable evidence of what actually served this one. A stream whose
	// vocabulary has moved on still produces a terminal from the process exit,
	// so these two are what actually show the events were read.
	if result.SessionID == "" || result.ResolvedModel == "" {
		t.Fatalf("Run() read no session or model from the stream: %#v", result)
	}
}
