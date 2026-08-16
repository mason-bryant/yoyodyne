package publish

import (
	"context"
	"errors"
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

// Merging is what publishes a promotion now, so the request has to name the
// method deliberately and pin the commit that may merge: a head that moved
// since the harness published it must be refused by the forge rather than
// merged in place of what the run integrated. The method reaches the CLI as the
// flag for it, because that is what decides whether the base ends up with the
// reviewed commit or a rewritten copy of it.
func TestGitHubMergeNamesTheMethodAndPinsTheHeadCommit(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	if err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit}); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 1 {
		t.Fatalf("merges = %d, want exactly one", len(merges))
	}
	for _, expected := range []string{"7", "--merge", "--match-head-commit", head, "--repo", "https://example.invalid/acme/thing"} {
		if !contains(merges[0], expected) {
			t.Errorf("pr merge args = %v, missing %q", merges[0], expected)
		}
	}
	for method, flag := range map[MergeMethod]string{MergeRebase: "--rebase", MergeSquash: "--squash"} {
		if err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: method}); err != nil {
			t.Fatalf("Merge(%s) error = %v", method, err)
		}
		asked := runner.matching(flag)
		if len(asked) != 1 {
			t.Errorf("merges asking for %s = %d, want exactly one", flag, len(asked))
		}
	}
	// A method the forge does not offer, a missing one, and a head that is not a
	// commit are all refused before anything runs: the merge method is explicit
	// or there is no merge.
	for _, request := range []MergeRequest{
		{Number: 7, HeadCommit: head},
		{Number: 7, HeadCommit: head, Method: MergeMethod("fast-forward")},
		{Number: 7, HeadCommit: "--repo=elsewhere", Method: MergeCommit},
		{Number: 0, HeadCommit: head, Method: MergeCommit},
	} {
		if err := forge.Merge(context.Background(), request); err == nil {
			t.Errorf("Merge(%#v) error = nil", request)
		}
	}
	if merges := runner.matching("pr merge"); len(merges) != 3 {
		t.Fatalf("refused requests still asked for a merge: %v", merges)
	}
}

// A protected branch declining the merge is the repository's rules being
// applied, not the harness failing, so the refusal has to name the requirement
// that was unmet rather than read as a generic error.
func TestGitHubMergeReportsARefusalAsTheUnmetRequirement(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Required status check \"build\" is expected. (mergePullRequest)",
	})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	var refused MergeRefused
	if !errors.As(err, &refused) {
		t.Fatalf("Merge() error = %v, want a MergeRefused", err)
	}
	if refused.Number != 7 || refused.Method != MergeCommit || refused.Status != "BLOCKED" {
		t.Fatalf("refusal = %#v", refused)
	}
	for _, want := range []string{"protection rules", "BLOCKED", `Required status check "build" is expected`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err.Error(), want)
		}
	}

	// A forge that cannot say why still reports what it printed. Losing the
	// refusal to a failed follow-up query would be the worst of both.
	quiet := &scriptedRunner{}
	quiet.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	quiet.reply("pr merge", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Pull request is not mergeable"})
	quiet.reply("pr view", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1})
	err = (GitHub{Runner: quiet, Dir: t.TempDir()}).Merge(context.Background(),
		MergeRequest{Number: 9, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	if err == nil || !strings.Contains(err.Error(), "Pull request is not mergeable") {
		t.Fatalf("Merge() unexplained refusal error = %v", err)
	}
}

// State is how the harness confirms that the merge it asked the forge for
// actually happened, rather than assuming it did.
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
