package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/publish"
	"yoyodyne/internal/runstate"
)

// The shape the operator asked for: the developer phase pushes the branch and
// opens the pull request, and the approving verdict is what merges it. Both are
// performed by the harness — the fake forge here is the harness's own adapter,
// and neither agent is ever asked to run anything.
func TestPipelinePublishesInTheDeveloperPhaseAndMergesOnApproval(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{}
	provider := roleBackend(func(request backend.RunRequest) error {
		// The branch must already be published by the time the reviewer is asked,
		// because a pull request nobody can see is not what a reviewer reviews.
		if request.Role == domain.RoleReviewer && len(forge.opened) == 0 {
			return errors.New("the reviewer ran before the branch was published")
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PullRequest == nil {
		t.Fatalf("outcome = %#v, want a published pull request", outcome)
	}
	if outcome.PullRequest.Number != 1 || !outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want the approved request merged", outcome.PullRequest)
	}
	if outcome.PublishFailure != "" || outcome.PublishSkipped != "" {
		t.Fatalf("outcome = %#v, want a clean publication", outcome)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("pull requests opened = %d, want exactly one", len(forge.opened))
	}
	if forge.opened[0].Base != "main" || forge.opened[0].Head != outcome.Branch {
		t.Fatalf("pull request request = %#v, want the run branch into main", forge.opened[0])
	}
	// The merge is the arrival of the integrated commit on the remote target: the
	// published branch carries exactly what the authoritative local branch does.
	if published := publishedCommit(t, remote, "main"); published != outcome.Integration.TargetCommit {
		t.Errorf("remote main = %q, want the integrated commit %q", published, outcome.Integration.TargetCommit)
	}
	if local := publishedCommit(t, repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want the integrated commit %q", local, outcome.Integration.TargetCommit)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}

	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || !state.PullRequest.Merged || state.PullRequest.Branch != outcome.Branch {
		t.Fatalf("durable pull request = %#v", state.PullRequest)
	}
	if !strings.Contains(tracker.notes, outcome.PullRequest.URL) {
		t.Errorf("tracker notes do not name the pull request:\n%s", tracker.notes)
	}
}

// A repair attempt updates the pull request its first attempt opened. A second
// request for one branch would give the same change two places to be reviewed.
func TestPipelinePublishesEveryAttemptOntoOnePullRequest(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"),
			[]byte(fmt.Sprintf("attempt %d\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want one", outcome.RepairAttempts)
	}
	if len(forge.opened) != 2 || forge.number != 1 {
		t.Fatalf("forge saw %d requests and issued number %d, want one pull request updated twice", len(forge.opened), forge.number)
	}
	// Every attempt is published, so what merged is the repaired change rather
	// than the one the reviewer rejected.
	if outcome.PullRequest.HeadCommit != outcome.Integration.SourceCommit {
		t.Errorf("published head = %q, want the integrated commit %q", outcome.PullRequest.HeadCommit, outcome.Integration.SourceCommit)
	}
	if published := publishedCommit(t, remote, "main"); published != outcome.Integration.TargetCommit {
		t.Errorf("remote main = %q, want the integrated commit %q", published, outcome.Integration.TargetCommit)
	}
}

// A repository with no remote is the degradation the design requires: the run
// behaves exactly as it did before publishing existed, and says why.
func TestPipelineWithoutARemoteRunsExactlyAsItDidBefore(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the ordinary local integration", outcome)
	}
	if outcome.PullRequest != nil || len(forge.opened) != 0 {
		t.Fatalf("a repository with no remote published anyway: %#v", outcome.PullRequest)
	}
	if !strings.Contains(outcome.PublishSkipped, "no \"origin\" remote") {
		t.Fatalf("publish skipped = %q, want it to name the missing remote", outcome.PublishSkipped)
	}
}

// A project that asked to publish and cannot must fail before it claims
// anything, rather than quietly producing work nobody sees.
func TestPipelineRefusesPublishingItCannotPerform(t *testing.T) {
	t.Parallel()

	for name, forge := range map[string]*fakeForge{
		"no forge CLI":          {availability: publish.Availability{}},
		"unauthenticated forge": {availability: publish.Availability{Installed: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository, _ := publishedRepository(t)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
			pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
			forge.reportAvailability = true

			if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); err == nil {
				t.Fatal("Run() error = nil, want a refusal before the item is claimed")
			}
			if tracker.claimed {
				t.Error("the work item was claimed by a run that could not publish")
			}
		})
	}
}

// A promotion that could not be published is an outstanding publication, not a
// failed run: the local target branch is the authoritative one and it moved.
func TestPipelineReportsAnOutstandingPublicationWithoutFailingThePromotion(t *testing.T) {
	t.Parallel()

	repository, _ := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{stateErr: errors.New("the forge is unreachable")}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want a succeeded, integrated, closed run", outcome)
	}
	if !strings.Contains(outcome.PublishFailure, "the forge is unreachable") {
		t.Fatalf("publish failure = %q", outcome.PublishFailure)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure == "" {
		t.Fatal("the outstanding publication was not recorded durably")
	}
	if !strings.Contains(tracker.notes, "Publication outstanding") {
		t.Errorf("tracker notes do not report the outstanding publication:\n%s", tracker.notes)
	}
}

// newPublishingPipeline is the automatic pipeline with publishing turned on.
func newPublishingPipeline(t *testing.T, repository string, tracker *fakeTracker, provider *fakeBackend, forge *fakeForge, commands []string) (Pipeline, *runstate.Store) {
	t.Helper()
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, commands)
	return publishing(pipeline, forge), store
}

// publishing turns a pipeline into one that publishes, the way automatic turns
// one into a pipeline that reviews and integrates.
func publishing(pipeline Pipeline, forge *fakeForge) Pipeline {
	pipeline.Config.Approvals.Publishing = domain.ApprovalAutomatic
	pipeline.Publisher = forge
	return pipeline
}

// publishedRepository is a pipeline repository with a bare remote that already
// carries its target branch.
func publishedRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := pipelineRepository(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runPipelineGit(t, remote, "init", "--bare", "-b", "main")
	runPipelineGit(t, repository, "remote", "add", "origin", remote)
	runPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	return repository, remote
}

func publishedCommit(t *testing.T, repository, branch string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
}

// fakeForge is the harness's forge adapter with the CLI taken out. It issues
// one pull request per branch, which is what lets a test tell an updated
// request from a second one.
type fakeForge struct {
	availability publish.Availability
	// reportAvailability makes the zero availability meaningful, so a test can
	// express "the CLI is missing" rather than getting the usable default.
	reportAvailability bool
	opened             []publish.Request
	number             int
	merged             bool
	stateErr           error
	// openReplies is how many times State reports the pull request still open
	// before reporting it merged, which is the forge noticing its commits on the
	// base shortly after the push rather than during it.
	openReplies int
	stateCalls  int
}

func (f *fakeForge) Availability(context.Context) (publish.Availability, error) {
	if f.reportAvailability {
		return f.availability, nil
	}
	return publish.Availability{Installed: true, Authenticated: true}, nil
}

func (f *fakeForge) Ensure(_ context.Context, request publish.Request) (publish.PullRequest, error) {
	f.opened = append(f.opened, request)
	if f.number == 0 {
		f.number = 1
	}
	return publish.PullRequest{Number: f.number, URL: fmt.Sprintf("https://example.invalid/pull/%d", f.number), State: "OPEN"}, nil
}

// State reports merged once the integrated commit has been pushed, which is
// exactly what a forge does when a pull request's commits arrive on its base —
// after openReplies further answers of "still open", which is what makes that
// bookkeeping asynchronous rather than instant.
func (f *fakeForge) State(context.Context, string) (publish.PullRequest, error) {
	if f.stateErr != nil {
		return publish.PullRequest{}, f.stateErr
	}
	f.stateCalls++
	url := fmt.Sprintf("https://example.invalid/pull/%d", f.number)
	if f.stateCalls <= f.openReplies {
		return publish.PullRequest{Number: f.number, URL: url, State: "OPEN"}, nil
	}
	f.merged = true
	return publish.PullRequest{Number: f.number, URL: url, State: "MERGED", Merged: true}, nil
}

var _ PullRequests = (*fakeForge)(nil)

// Publishing and automatic integration are separate opt-ins, so a project can
// have the harness open pull requests and still merge them itself. The run then
// stops where a human-integration run always stopped: checks pass, nothing is
// promoted, and the worktree is preserved — with the work now published.
func TestPipelinePublishesWithoutIntegratingWhenAHumanMerges(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration != nil || outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want nothing promoted under a human policy", outcome)
	}
	if outcome.PullRequest == nil || outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it opened and left for the human", outcome.PullRequest)
	}
	// Nothing merges it, so the remote target must not have moved and the run
	// branch must still be there for whoever does.
	if head := publishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q", published, outcome.PullRequest.HeadCommit)
	}
	if outcome.WorktreeRemoved || outcome.BranchRemoved {
		t.Errorf("outcome = %#v, want the artifacts preserved for the human", outcome)
	}
}

// A forge marks a pull request merged when its commits reach the base, which
// happens shortly after the push rather than during it. Asking once would
// report a successful publication as outstanding and leave the merged branch on
// the remote, so the confirmation waits the way the thing it observes behaves.
func TestPipelineWaitsForTheForgeToNoticeTheMerge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{openReplies: 2}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	waits := 0
	pipeline.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a publication that waited and succeeded", outcome.PublishFailure)
	}
	if !outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it merged", outcome.PullRequest)
	}
	if waits != 2 || forge.stateCalls != 3 {
		t.Errorf("waits = %d and forge queries = %d, want two waits and three queries", waits, forge.stateCalls)
	}
	// A confirmation that never arrived would have skipped the branch removal.
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A forge that keeps reporting the request open past the bounded wait is an
// outstanding publication, not a run that waits forever.
func TestPipelineStopsWaitingForAMergeThatNeverArrives(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{openReplies: 99}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Sleep = func(context.Context, time.Duration) error { return nil }

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	if !strings.Contains(outcome.PublishFailure, "is still open") {
		t.Fatalf("publish failure = %q, want the unmerged request reported", outcome.PublishFailure)
	}
	if forge.stateCalls != len(mergeConfirmationDelays)+1 {
		t.Errorf("forge queries = %d, want %d", forge.stateCalls, len(mergeConfirmationDelays)+1)
	}
	// Nothing confirmed the merge, so the branch a person may still need is left
	// exactly where it was published.
	if published := publishedCommit(t, remote, outcome.Branch); published == "" {
		t.Error("an unconfirmed merge removed the published branch anyway")
	}
}

// A repository with no remote reports the same thing on every pass. A resumed
// run that said nothing about publishing would read as a run whose policy had
// quietly changed between processes.
func TestResumedRunReportsTheSkippedPublicationToo(t *testing.T) {
	t.Parallel()

	// The restartable fixture's repository has no remote, which is the case that
	// degrades to purely local behavior.
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	write := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}

	interrupted := &interruptedStore{StateStore: store, atAttempt: 2}
	first := roleBackend(write, repairVerdict)
	firstPipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first), &fakeForge{})
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !interrupted.stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
	}
	if !strings.Contains(firstOutcome.PublishSkipped, "no \"origin\" remote") {
		t.Fatalf("interrupted publish skipped = %q", firstOutcome.PublishSkipped)
	}

	second := roleBackend(write, approveVerdict)
	resumed := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second), &fakeForge{})
	outcome, err := resumed.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != firstOutcome.RunID {
		t.Fatalf("resumed run = %q, want the interrupted run %q", outcome.RunID, firstOutcome.RunID)
	}
	if outcome.PublishSkipped != firstOutcome.PublishSkipped {
		t.Errorf("resumed publish skipped = %q, want the same reason the first pass reported (%q)", outcome.PublishSkipped, firstOutcome.PublishSkipped)
	}
}

// Publishing commits the developer's work before the checks and the review run,
// so the whole merge gate depends on the reviewer still being handed that work.
// This asserts it against the prompt the reviewer is actually given: an approval
// granted over an empty patch would be a rubber stamp, and every other test here
// uses a reviewer that approves whatever it sees.
func TestPublishedWorkStillReachesTheReviewer(t *testing.T) {
	t.Parallel()

	repository, _ := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"),
			[]byte("the developer's work\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.requestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviewer invocations = %d, want exactly one", len(reviews))
	}
	reviewPrompt := reviews[0].Prompt
	// The work really was committed before the review, which is the condition
	// that makes the rest of this test worth asserting.
	if outcome.PullRequest == nil || outcome.PullRequest.HeadCommit == "" {
		t.Fatalf("outcome = %#v, want the attempt published before review", outcome)
	}
	for _, want := range []string{"feature.txt", "the developer's work"} {
		if !strings.Contains(reviewPrompt, want) {
			t.Fatalf("the reviewer was not shown %q in a published run:\n%s", want, reviewPrompt)
		}
	}
	// The summary beside the patch has to describe the change too, rather than
	// reporting a published worktree as having no changes at all.
	if strings.Contains(reviewPrompt, "No reported working tree changes.") {
		t.Errorf("the reviewer was told a published change is no change:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "No textual diff content.") {
		t.Errorf("the reviewer was given no patch for a published change:\n%s", reviewPrompt)
	}
}
