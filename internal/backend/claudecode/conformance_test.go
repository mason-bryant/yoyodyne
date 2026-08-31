package claudecode

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
// An unknown option is not ignored: Claude Code refuses the whole invocation
// before it reaches the provider, so a flag wrong here -- or right but newer
// than the installed CLI -- fails every invocation of every role that receives
// it. For the flags on the read-only branch that is the reviewer, the product
// manager, the architect, and the development manager at once, and the first
// evidence of it would be a day of runs that cannot be reviewed.
//
// The arguments come from the backend rather than from a list repeated here, so
// a flag added later is covered without anybody remembering to cover it. Unlike
// the conformance run above this costs nothing -- `--help` makes no provider
// call and needs no account -- so it is gated on the CLI being installed rather
// than on opting in, and skips where it is not.
func TestTheInstalledCLIKnowsEveryFlagThisBackendPasses(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("Claude Code is not installed, so what it accepts cannot be asked here: %v", err)
	}
	help, err := exec.Command(binary, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("claude --help error = %v: %s", err, help)
	}
	known := make(map[string]bool)
	for _, flag := range longFlag.FindAllString(string(help), -1) {
		known[flag] = true
	}
	if !known["--print"] && !known["--output-format"] {
		t.Fatalf("claude --help listed no recognizable options, so this asserts nothing: %s", help)
	}

	// Every role this backend serves, and a request that sets each optional
	// field, so no branch of the argument assembly goes unasked.
	for _, role := range []domain.AgentRole{
		domain.RoleDeveloper, domain.RoleReviewer,
		domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager,
	} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"ok"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				WorkingDirectory: "/worktree",
				Prompt:           "do the work",
				SystemPrompt:     "the contract",
				SessionID:        "session-1",
				Model:            "claude-opus-5",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			for _, argument := range runner.commands[0].Args {
				// A flag is an argument that begins with one, so a value that
				// happens to contain a dash pair -- a settings document, a prompt --
				// is never mistaken for an option the CLI has to know.
				if !strings.HasPrefix(argument, "--") {
					continue
				}
				if !known[argument] {
					t.Fatalf("a %s invocation passes %q, which %s does not list; an unknown option makes the CLI refuse the whole invocation",
						role, argument, binary)
				}
			}
		})
	}
}

func TestLocalConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}
	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.SessionID == "" || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}

	// A read-only role is invoked with flags the developer's is not, and the one
	// that keeps its prompt prefix stable is the newest of them. An installed CLI
	// that does not know a flag refuses the whole invocation rather than ignoring
	// it, so this is where a version too old to carry it surfaces -- as one
	// skipped conformance run rather than as every review of the day failing.
	advisory, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() as a reviewer error = %v", err)
	}
	if advisory.IsError || advisory.FinalText == "" {
		t.Fatalf("Run() as a reviewer result = %#v", advisory)
	}
}
