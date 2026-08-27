package publish

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
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
// reviewed commit or a rewritten copy of it. The merge is asked for as of when
// the requirements are met rather than as of now, which is what a protected
// branch with required checks is asking for.
func TestGitHubMergeNamesTheMethodAndPinsTheHeadCommit(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if !result.Queued {
		t.Errorf("Merge() = %#v, want the merge queued for when the requirements are met", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 1 {
		t.Fatalf("merges = %d, want exactly one", len(merges))
	}
	for _, expected := range []string{"7", "--merge", "--auto", "--match-head-commit", head, "--repo", "https://example.invalid/acme/thing"} {
		if !contains(merges[0], expected) {
			t.Errorf("pr merge args = %v, missing %q", merges[0], expected)
		}
	}
	// Merging with administrator privileges would bypass the checks the branch
	// protection expresses, which removes the gate rather than satisfying it.
	if contains(merges[0], "--admin") {
		t.Errorf("pr merge args = %v, want no administrator override", merges[0])
	}
	for method, flag := range map[MergeMethod]string{MergeRebase: "--rebase", MergeSquash: "--squash"} {
		if _, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: method}); err != nil {
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
		if _, err := forge.Merge(context.Background(), request); err == nil {
			t.Errorf("Merge(%#v) error = nil", request)
		}
	}
	if merges := runner.matching("pr merge"); len(merges) != 3 {
		t.Fatalf("refused requests still asked for a merge: %v", merges)
	}
}

// A repository whose settings forbid queued merges cannot be published to when
// something is holding the request back, and the operator has to be told which
// setting to change rather than left reading a refusal that names no remedy.
func TestGitHubMergeReportsARepositoryThatCannotQueueAMerge(t *testing.T) {
	t.Parallel()

	for name, reason := range map[string]string{
		"the forge's own wording": "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
		"hyphenated":              "failed to enable auto-merge: auto-merge is not enabled for this repository",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr merge", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: reason})
			// The base branch is holding the request back, which is the half that
			// makes a repository without queued merges unpublishable.
			runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
			forge := GitHub{Runner: runner, Dir: t.TempDir()}

			_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
			var unavailable AutoMergeUnavailable
			if !errors.As(err, &unavailable) {
				t.Fatalf("Merge() error = %v, want an AutoMergeUnavailable", err)
			}
			if unavailable.Number != 7 || unavailable.Status != "BLOCKED" {
				t.Errorf("unavailable = %#v, want the pull request and the state that held it back named", unavailable)
			}
			for _, want := range []string{"Allow auto-merge", "protection rules", "BLOCKED", reason} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err.Error(), want)
				}
			}
			// The request was never merged past the requirement, and above all not
			// with an override: that would satisfy nothing the repository asked for.
			calls := runner.matching("pr merge")
			if len(calls) != 1 {
				t.Fatalf("pr merge calls = %v, want the queued request and nothing after it", calls)
			}
			if contains(calls[0], "--admin") {
				t.Errorf("pr merge args = %v, want no administrator override", calls[0])
			}
		})
	}
}

// "Allow auto-merge" is off by default on GitHub, so a repository that forbids
// queued merges is the ordinary case rather than a broken one. When nothing is
// holding the request back there is nothing for a queue to wait for, and the
// merge is simply made — reporting the setting instead would fail the
// publication of every project without branch protection, which is the manual
// step queuing exists to remove.
func TestGitHubMergeMergesWhenQueuingIsForbiddenAndNothingIsWaiting(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"CLEAN"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v, want the request merged rather than reported as unpublishable", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 {
		t.Fatalf("pr merge calls = %v, want the queued request and then the merge itself", merges)
	}
	if contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Errorf("second pr merge args = %v, want an ordinary merge with no override", merges[1])
	}
	for _, expected := range []string{"--merge", "--match-head-commit", head} {
		if !contains(merges[1], expected) {
			t.Errorf("second pr merge args = %v, missing %q", merges[1], expected)
		}
	}
}

// A repository with no required checks has nothing for a queued merge to wait
// for, and the forge says so by refusing to queue one. Merging then is what the
// queued request asked for rather than a way around it — every requirement is
// already met — and treating the refusal as final would leave the unprotected
// case unable to publish at all.
func TestGitHubMergeMergesWhenTheForgeHasNothingLeftToWaitFor(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request is in clean status (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 {
		t.Fatalf("merges = %d, want the queued request and then the merge itself", len(merges))
	}
	if !contains(merges[0], "--auto") {
		t.Errorf("first pr merge args = %v, want the queued request", merges[0])
	}
	if contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Errorf("second pr merge args = %v, want an ordinary merge with no override", merges[1])
	}
	for _, expected := range []string{"--merge", "--match-head-commit", head} {
		if !contains(merges[1], expected) {
			t.Errorf("second pr merge args = %v, missing %q", merges[1], expected)
		}
	}
}

// The words a forge refuses a queued merge in can be reworded, and a repository
// with no required checks would then stop publishing entirely — it is refused on
// every run. So the same question is asked of the merge state the forge reports,
// which is its own vocabulary: a clean request has nothing for a queue to wait
// for, whatever the message says.
func TestGitHubMergeMergesACleanRequestItDoesNotRecognizeTheRefusalOf(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: some wording nobody has seen before (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"CLEAN"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 || contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Fatalf("pr merge calls = %v, want an ordinary merge after the queue was refused", merges)
	}

	// A request that is not clean is still a refusal, and it names the state it
	// was refused in even when nothing recognized the message.
	blocked := &scriptedRunner{}
	blocked.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	blocked.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: some wording nobody has seen before (enablePullRequestAutoMerge)",
	})
	blocked.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"DIRTY"}`})
	_, err = (GitHub{Runner: blocked, Dir: t.TempDir()}).Merge(context.Background(),
		MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	var refused MergeRefused
	if !errors.As(err, &refused) || refused.Status != "DIRTY" {
		t.Fatalf("Merge() error = %v, want a refusal naming the state it was refused in", err)
	}
	if calls := blocked.matching("pr merge"); len(calls) != 1 {
		t.Errorf("pr merge calls = %v, want the refused request not to be merged anyway", calls)
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
		Stderr:   "GraphQL: Pull Request is not mergeable: the merge commit cannot be cleanly created. (enablePullRequestAutoMerge)",
	})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	var refused MergeRefused
	if !errors.As(err, &refused) {
		t.Fatalf("Merge() error = %v, want a MergeRefused", err)
	}
	if refused.Number != 7 || refused.Method != MergeCommit || refused.Status != "BLOCKED" {
		t.Fatalf("refusal = %#v", refused)
	}
	for _, want := range []string{"protection rules", "BLOCKED", "the merge commit cannot be cleanly created"} {
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
	_, err = (GitHub{Runner: quiet, Dir: t.TempDir()}).Merge(context.Background(),
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
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"MERGED","mergedAt":"2026-08-16T12:00:00Z","autoMergeRequest":null}]`,
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

// An open, unmerged request means two different things depending on whether the
// forge is still holding a merge for it: one is a queued merge that has not
// landed yet, the other is a queued merge the forge dropped, which needs a
// person. The state has to tell them apart.
func TestGitHubStateReportsAQueuedMergeSeparatelyFromADroppedOne(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		reported string
		want     bool
	}{
		"still queued": {reported: `{"authorEmail":"harness@example.invalid","mergeMethod":"MERGE"}`, want: true},
		"dropped":      {reported: "null", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr list", execution.ProcessResult{
				Status: execution.ProcessSucceeded,
				Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":"","autoMergeRequest":` + test.reported + `}]`,
			})
			observed, err := (GitHub{Runner: runner}).State(context.Background(), "yoyodyne/task/abcd1234")
			if err != nil {
				t.Fatalf("State() error = %v", err)
			}
			if observed.Merged {
				t.Fatalf("State() = %#v, want an open request", observed)
			}
			if observed.AutoMerge != test.want {
				t.Errorf("State() auto-merge = %t, want %t", observed.AutoMerge, test.want)
			}
		})
	}

	// The queued merge is only knowable if it was asked for, so the query has to
	// ask for it.
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"})
	_, _ = (GitHub{Runner: runner}).State(context.Background(), "yoyodyne/task/abcd1234")
	listed := runner.matching("pr list")
	if len(listed) != 1 || !contains(listed[0], "number,url,state,mergedAt,autoMergeRequest") {
		t.Errorf("pr list args = %v, want the queued merge among the requested fields", listed)
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

// A contributor's run branch lives in their fork, so the request has to be
// opened across two repositories: against the one the work is going into, from a
// head that repository has never heard of. What qualifies the head is the fork's
// owner, read out of the fork remote's own URL.
func TestGitHubOpensACrossRepositoryRequestFromTheFork(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url upstream", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("remote get-url fork", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "git@example.invalid:contributor/thing.git\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":9,"url":"https://example.invalid/pull/9","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "upstream", PushRemote: "fork"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pr create calls = %d, want exactly one", len(created))
	}
	if !contains(created[0], "contributor:yoyodyne/task/abcd1234") {
		t.Errorf("pr create args = %v, want the head qualified with the fork's owner", created[0])
	}
	// The request is still opened against the repository the work is going into,
	// which is the half of a cross-repository request that must not move.
	if !contains(created[0], "https://example.invalid/acme/thing") {
		t.Errorf("pr create args = %v, want the request opened against the publishing remote", created[0])
	}
	// The listing is by head reference name, which is what a request from a fork
	// carries on the base repository. Qualifying it would name a reference that
	// repository does not have.
	for _, call := range runner.matching("pr list") {
		if !contains(call, "yoyodyne/task/abcd1234") {
			t.Errorf("pr list args = %v, want the run branch's head reference", call)
		}
	}
}

// A project that pushes run branches to the repository it publishes into names
// the branch and nothing else, and resolves one remote to do it.
func TestGitHubLeavesTheHeadUnqualifiedWithoutAFork(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":4,"url":"https://example.invalid/pull/4","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "origin"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pr create calls = %d, want exactly one", len(created))
	}
	if !contains(created[0], "yoyodyne/task/abcd1234") {
		t.Errorf("pr create args = %v, want the plain head reference", created[0])
	}
}

// A fork remote can be written any way Git accepts one, and the account is the
// same in all of them. A URL that names no repository is refused rather than
// guessed at: a request opened from an unidentifiable head is worse than one
// that was not opened.
func TestRepositoryOwnerReadsEveryRemoteURLForm(t *testing.T) {
	t.Parallel()

	for _, url := range []string{
		"git@github.com:contributor/thing.git",
		"ssh://git@github.com/contributor/thing.git",
		"https://github.com/contributor/thing.git",
		"https://github.com/contributor/thing",
		"https://github.com/contributor/thing/",
	} {
		owner, err := repositoryOwner(url)
		if err != nil || owner != "contributor" {
			t.Errorf("repositoryOwner(%q) = %q, %v, want contributor", url, owner, err)
		}
	}
	for _, url := range []string{"", "   ", "https://github.com", "github.com:thing.git"} {
		if owner, err := repositoryOwner(url); err == nil {
			t.Errorf("repositoryOwner(%q) = %q, want a refusal", url, owner)
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

// TestGitHubMergeMergesWhenChecksAreNotRequired covers the ordinary repository
// that has CI and no branch protection. Its checks are not required by anything,
// so the forge reports UNSTABLE rather than CLEAN, and nothing is holding the
// request back. Treating that as unpublishable would misreport the commonest
// configuration there is as needing a setting changed.
func TestGitHubMergeMergesWhenChecksAreNotRequired(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"UNSTABLE", "HAS_HOOKS", "clean"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr merge", execution.ProcessResult{
				Status:   execution.ProcessFailed,
				ExitCode: 1,
				Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
			})
			runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
			runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"` + status + `"}`})
			forge := GitHub{Runner: runner, Dir: t.TempDir()}

			head := "0123456789abcdef0123456789abcdef01234567"
			result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
			if err != nil {
				t.Fatalf("Merge() error = %v, want the request merged rather than reported as unpublishable", err)
			}
			if result.Queued {
				t.Errorf("Merge() = %#v, want a merge rather than a queued request", result)
			}
		})
	}
}

// TestGitHubMergeStillReportsAnUnavailableSettingWhenSomethingIsWaiting keeps
// the fallback narrow: a state with something genuinely outstanding must not be
// merged past just because queuing was refused.
func TestGitHubMergeStillReportsAnUnavailableSettingWhenSomethingIsWaiting(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	var unavailable AutoMergeUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("Merge() error = %v, want the unavailable setting reported when something is genuinely waiting", err)
	}
	if merges := runner.matching("pr merge"); len(merges) != 1 {
		t.Errorf("pr merge calls = %v, want no second merge attempt when something is waiting", merges)
	}
}
