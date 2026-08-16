package publish

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
)

// Ensure has to be idempotent, because it runs after every developer attempt.
// The first attempt opens the pull request; each repair attempt finds the same
// one rather than opening a second for the same branch.
func TestGitHubEnsureOpensOncePerBranch(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	request := Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "task Do the thing", Body: "opened by the harness"}

	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/pull/7\n"})

	opened, err := forge.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if opened.Number != 7 || opened.URL != "https://example.invalid/pull/7" || opened.Merged {
		t.Fatalf("Ensure() = %#v", opened)
	}
	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pull request creations = %d, want exactly one", len(created))
	}
	for _, expected := range []string{"--base", "main", "--head", "yoyodyne/task/abcd1234"} {
		if !contains(created[0], expected) {
			t.Errorf("pr create args = %v, missing %q", created[0], expected)
		}
	}

	again, err := forge.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() repeated error = %v", err)
	}
	if again.Number != 7 {
		t.Fatalf("Ensure() repeated = %#v", again)
	}
	if creations := runner.matching("pr create"); len(creations) != 1 {
		t.Fatalf("pull request creations = %d after a second Ensure, want exactly one", len(creations))
	}
}

// A branch whose pull request is already closed or merged cannot receive more
// of this run's work. Opening a second one would publish the branch twice and
// leave two answers about what is under review.
func TestGitHubEnsureRefusesAPullRequestThatIsNoLongerOpen(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":9,"url":"https://example.invalid/pull/9","state":"MERGED","mergedAt":"2026-08-16T00:00:00Z"}]`,
	})
	forge := GitHub{Runner: runner}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "task"}); err == nil || !strings.Contains(err.Error(), "cannot be republished into") {
		t.Fatalf("Ensure() merged error = %v", err)
	}
	if creations := runner.matching("pr create"); len(creations) != 0 {
		t.Fatalf("a merged pull request was republished into: %v", creations)
	}
}

// State is how the harness confirms that pushing the integrated commit actually
// merged the pull request, rather than assuming it did.
func TestGitHubStateReportsMergeAndAbsence(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"MERGED","mergedAt":"2026-08-16T12:00:00Z"}]`,
	})
	forge := GitHub{Runner: runner}
	merged, err := forge.State(context.Background(), "yoyodyne/task/abcd1234")
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !merged.Merged || merged.Number != 7 {
		t.Fatalf("State() = %#v", merged)
	}

	absent := &scriptedRunner{}
	absent.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	absent.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"})
	if _, err := (GitHub{Runner: absent}).State(context.Background(), "yoyodyne/task/abcd1234"); err == nil || !strings.Contains(err.Error(), "no pull request exists") {
		t.Fatalf("State() absent error = %v", err)
	}
}

func TestGitHubAvailabilityReportsMissingAndUnauthenticatedCLIs(t *testing.T) {
	t.Parallel()

	missing := &scriptedRunner{}
	missing.fail("--version", exec.ErrNotFound)
	availability, err := (GitHub{Runner: missing}).Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability() missing error = %v", err)
	}
	if availability.Installed || availability.Authenticated {
		t.Fatalf("Availability() missing = %#v", availability)
	}

	unauthenticated := &scriptedRunner{}
	unauthenticated.reply("--version", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "gh version 2.0.0\nhttps://example.invalid\n"})
	unauthenticated.reply("auth", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1})
	availability, err = (GitHub{Runner: unauthenticated}).Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability() unauthenticated error = %v", err)
	}
	if !availability.Installed || availability.Authenticated {
		t.Fatalf("Availability() unauthenticated = %#v", availability)
	}
	if availability.Version != "gh version 2.0.0" {
		t.Errorf("Availability() version = %q", availability.Version)
	}
}

// Branch names reach a command line, so anything that could read as an option
// is refused before it gets there.
func TestGitHubRefusesArgumentsThatCouldReadAsOptions(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	forge := GitHub{Runner: runner}
	for _, request := range []Request{
		{Head: "--repo=elsewhere", Base: "main", Title: "task"},
		{Head: "yoyodyne/task/abcd1234", Base: "-f", Title: "task"},
		{Head: "yoyodyne/task/abcd1234", Base: "main"},
		{Head: "with space", Base: "main", Title: "task"},
	} {
		if _, err := forge.Ensure(context.Background(), request); err == nil {
			t.Errorf("Ensure(%#v) error = nil", request)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("refused requests still ran commands: %v", runner.commands)
	}
}

// scriptedRunner answers gh invocations by the first argument fragment they
// contain, so a test states what the CLI reports rather than how it is invoked.
type scriptedRunner struct {
	commands [][]string
	replies  map[string]execution.ProcessResult
	later    map[string]execution.ProcessResult
	after    map[string]int
	failures map[string]error
	seen     map[string]int
}

func (r *scriptedRunner) reply(match string, result execution.ProcessResult) {
	if r.replies == nil {
		r.replies = map[string]execution.ProcessResult{}
	}
	r.replies[match] = result
}

// replyAfter switches a match to a different answer once it has been called a
// given number of times, which is how "the pull request did not exist and then
// it did" is expressed.
func (r *scriptedRunner) replyAfter(match string, calls int, result execution.ProcessResult) {
	if r.later == nil {
		r.later = map[string]execution.ProcessResult{}
		r.after = map[string]int{}
	}
	r.later[match] = result
	r.after[match] = calls
}

func (r *scriptedRunner) fail(match string, err error) {
	if r.failures == nil {
		r.failures = map[string]error{}
	}
	r.failures[match] = err
}

func (r *scriptedRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	joined := strings.Join(command.Args, " ")
	for match, err := range r.failures {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{}, err
		}
	}
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	for match, result := range r.replies {
		if !strings.Contains(joined, match) {
			continue
		}
		r.seen[match]++
		if later, ok := r.later[match]; ok && r.seen[match] > r.after[match] {
			return later, nil
		}
		return result, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func (r *scriptedRunner) matching(match string) [][]string {
	var found [][]string
	for _, command := range r.commands {
		if strings.Contains(strings.Join(command, " "), match) {
			found = append(found, command)
		}
	}
	return found
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestGitHubScopesCommandsToTheConfiguredRemote pins the forge to the remote the
// project named. Without it the CLI infers a repository from the working
// directory, which is wrong rather than merely redundant in a checkout with
// more than one remote.
func TestGitHubScopesCommandsToTheConfiguredRemote(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":3,"url":"https://example.invalid/pull/3","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "upstream"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	resolved := runner.matching("remote get-url")
	if len(resolved) == 0 {
		t.Fatal("the remote was never resolved")
	}
	if !contains(resolved[0], "upstream") {
		t.Errorf("remote resolution args = %v, want the configured remote", resolved[0])
	}
	for _, kind := range []string{"pr list", "pr create"} {
		calls := runner.matching(kind)
		if len(calls) == 0 {
			t.Fatalf("%s was never called", kind)
		}
		for _, call := range calls {
			if !contains(call, "--repo") || !contains(call, "https://example.invalid/acme/thing") {
				t.Errorf("%s args = %v, want scoping to the resolved repository", kind, call)
			}
		}
	}
}

// TestGitHubRefusesWhenTheRemoteCannotBeResolved keeps the failure closed: a
// forge command that cannot tell which repository it is acting on must not fall
// back to inferring one.
func TestGitHubRefusesWhenTheRemoteCannotBeResolved(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "no such remote"})
	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "missing"}

	if _, err := forge.State(context.Background(), "yoyodyne/task/abcd1234"); err == nil {
		t.Fatal("State() error = nil, want a refusal when the remote cannot be resolved")
	}
	if calls := runner.matching("pr "); len(calls) != 0 {
		t.Errorf("forge commands ran despite an unresolved remote: %v", calls)
	}
}
