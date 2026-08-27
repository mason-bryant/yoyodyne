package orchestrator

// A run whose two roles are configured for Codex, driven through the real
// adapter over a CLI that is not there.
//
// This is where the Codex backend's acceptance criterion is actually held. The
// adapter's own tests build a backend.RunRequest by hand and choose the
// permission mode they pass, so they can only show that the adapter honours what
// they asked for; what they cannot show is that the mode the harness itself
// emits is one the adapter accepts. That gap is not academic: the adapter turns a
// permission mode into a Codex sandbox from a fixed table and refuses anything
// outside it, and a refusal there lands inside Run — after the item is claimed,
// the worktree is created, and the operator is waiting — which is the opposite of
// the policy refusal the item asked for. Letting the pipeline choose the mode is
// the only way to see that.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The sandboxes each role's tool posture requires by the time the invocation
// reaches the provider. They are spelled out here rather than imported from the
// adapter on purpose: this test is the other side of that statement, and two
// sides reading one constant would agree with each other whatever the adapter
// did.
const (
	codexDeveloperSandbox = "workspace-write"
	codexReviewerSandbox  = "read-only"
)

func TestARunOnCodexReachesTheProviderWithThePostureItsRolesRequire(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, store := newAutomaticPipeline(t, repository, tracker,
		roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), []string{"exit 0"})

	// Selecting Codex in configuration, which is what the criterion is about.
	// Both roles inside a run name it, and the configuration the pipeline
	// validates before it claims anything has to accept that.
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: testDeveloperModel, Instances: 1,
	}
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{
		Role: domain.RoleReviewer, Backend: domain.BackendCodex, Model: testReviewerModel, Instances: 1,
	}
	// The real adapter over a CLI that is not there. What is under test is how the
	// invocation is launched, so this double's answers are the least interesting
	// part of it and its command lines are the whole point.
	cli := &scriptedCodexCLI{}
	pipeline.Backend = codex.Backend{Runner: cli}
	pipeline.Reviewer = review.Reviewer{Backend: codex.Backend{Runner: cli}, Model: testReviewerModel}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v, want a run that completed on Codex", outcome)
	}
	// The session comes off the provider's own stream, so a run carrying it is one
	// whose events this adapter really parsed rather than one a double answered.
	if outcome.ProviderSessionID != codexSessionID {
		t.Fatalf("recorded provider session = %q, want the one the Codex stream named", outcome.ProviderSessionID)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ProviderResolvedModel != codexResolvedModel {
		t.Fatalf("recorded resolved model = %q, want the model the Codex stream named", state.ProviderResolvedModel)
	}

	// Both invocations were launched, and each under the sandbox its role's tool
	// posture requires: the developer able to edit the worktree, the reviewer not.
	// A permission mode the adapter refused would have failed the run above; this
	// is the other half, that an accepted mode became the right sandbox rather
	// than merely a sandbox.
	developer, reviewer := cli.roleInvocations(t)
	if got := codexSandboxOf(t, developer.Args); got != codexDeveloperSandbox {
		t.Errorf("the developer ran under sandbox %q, want %q", got, codexDeveloperSandbox)
	}
	if got := codexSandboxOf(t, reviewer.Args); got != codexReviewerSandbox {
		t.Errorf("the reviewer ran under sandbox %q, want %q", got, codexReviewerSandbox)
	}
	// And the developer's ran in the run's own worktree, which is what makes
	// `workspace-write` a bound rather than a permission.
	if developer.Dir != outcome.WorktreePath {
		t.Errorf("the developer ran in %q, want the run's worktree %q", developer.Dir, outcome.WorktreePath)
	}
}

// A run configured for a provider whose CLI is not installed says so about that
// provider. The check is the same one every run makes; what this holds is that
// it stopped naming one backend for all of them, because sending an operator to
// install the other provider is a remedy for a machine that is not theirs.
func TestARunNamesTheBackendWhoseCLIIsMissing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker,
		roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), []string{"exit 0"})
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: testDeveloperModel, Instances: 1,
	}
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{
		Role: domain.RoleReviewer, Backend: domain.BackendCodex, Model: testReviewerModel, Instances: 1,
	}
	pipeline.Backend = codex.Backend{Runner: &scriptedCodexCLI{absent: true}}

	_, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "codex backend is not installed") {
		t.Fatalf("Run() error = %v, want it to name the backend the agents selected", err)
	}
	if tracker.claimed {
		t.Error("a run whose provider is not installed claimed the work item anyway")
	}
}

const (
	codexSessionID     = "codex-session"
	codexResolvedModel = "gpt-5-codex"
)

// scriptedCodexCLI stands in for the installed Codex CLI. It records how each
// invocation was launched and answers with a stream in the provider's own shape:
// the availability probes the run makes first, then one turn per role.
type scriptedCodexCLI struct {
	mu sync.Mutex
	// absent makes every invocation fail to start, which is what a CLI that is
	// not on the machine does — and is a different failure from one that ran and
	// refused.
	absent   bool
	commands []execution.Command
}

func (c *scriptedCodexCLI) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if c.absent {
		return execution.ProcessResult{}, exec.ErrNotFound
	}
	c.mu.Lock()
	turn := len(c.commands)
	c.commands = append(c.commands, command)
	c.mu.Unlock()

	// The run asks whether the provider is there and logged in before it claims
	// anything, so those two answers come before either role's turn.
	switch {
	case len(command.Args) == 0:
		return execution.ProcessResult{}, exec.ErrNotFound
	case command.Args[0] == "--version":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"}, nil
	case command.Args[0] == "login":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"}, nil
	}

	message := approveVerdict
	if turn == codexDeveloperTurn {
		// The developer's invocation is the one that changes the worktree, so it
		// leaves behind the change the checks and the review are then about.
		if err := os.WriteFile(filepath.Join(command.Dir, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return execution.ProcessResult{}, err
		}
		message = "implemented the work item"
	}
	for _, line := range codexStream(message) {
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStdout, Text: line})
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// The two availability probes come first, so the developer's turn is the third
// invocation and the reviewer's the fourth.
const (
	codexDeveloperTurn = 2
	codexReviewerTurn  = 3
)

// roleInvocations is the developer's launch and the reviewer's, and a failure
// naming what actually happened when the run did not make both.
func (c *scriptedCodexCLI) roleInvocations(t *testing.T) (execution.Command, execution.Command) {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commands) != codexReviewerTurn+1 {
		t.Fatalf("the provider was launched %d times, want two availability probes and one turn each for the developer and the reviewer: %#v",
			len(c.commands), c.commands)
	}
	return c.commands[codexDeveloperTurn], c.commands[codexReviewerTurn]
}

func codexStream(message string) []string {
	configured, err := json.Marshal(map[string]any{
		"id":  "0",
		"msg": map[string]any{"type": "session_configured", "session_id": codexSessionID, "model": codexResolvedModel},
	})
	if err != nil {
		panic(err)
	}
	complete, err := json.Marshal(map[string]any{
		"id":  "1",
		"msg": map[string]any{"type": "task_complete", "last_agent_message": message},
	})
	if err != nil {
		panic(err)
	}
	return []string{string(configured), string(complete)}
}

func codexSandboxOf(t *testing.T, args []string) string {
	t.Helper()

	for index, arg := range args {
		if arg == "--sandbox" && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("no sandbox on the command line: %#v", args)
	return ""
}
