package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// fakeForge is the harness's forge adapter with the CLI taken out. It issues
// one pull request per branch, which is what lets a test tell an updated
// request from a second one, and it performs the merge itself the way the forge
// does: by moving the target branch on the remote, without a push.
type fakeForge struct {
	availability publish.Availability
	// reportAvailability makes the zero availability meaningful, so a test can
	// express "the CLI is missing" rather than getting the usable default.
	reportAvailability bool
	opened             []publish.Request
	number             int
	merged             bool
	stateErr           error
	// onEnsure runs when the pull request is opened, which is the moment after
	// the branch is published and before anything is promoted. It is how a test
	// expresses the world moving underneath a run.
	onEnsure func()
	// remote is the bare repository this forge merges into. A forge with none
	// records the merge and touches nothing, which is what an operator sees when
	// a forge reports a merge the remote does not show.
	remote string
	merges []publish.MergeRequest
	// mergeErr is what the forge answers instead of merging, which is how a
	// refusal by a protected branch is expressed.
	mergeErr error
	// queueMerge makes the forge queue the merge instead of performing it, which
	// is what a base branch with required checks produces: the request is
	// accepted, nothing moves yet, and the forge merges minutes later. queued is
	// the merge it is holding.
	queueMerge bool
	queued     bool
	// replayMerge makes the forge rewrite what it merges instead of merging it,
	// which is what GitHub's rebase and squash methods do: the base ends up with
	// a fresh commit carrying the same content, and the reviewed commit itself
	// never arrives.
	replayMerge bool
	// openReplies is how many times State reports the pull request still open
	// before reporting it merged, which is the forge's own record of a request
	// lagging the merge it just performed.
	openReplies int
	stateCalls  int
	// headCommit is the commit State reports the request carrying, where a test
	// needs the forge to say. A forge that says nothing leaves the reader to the
	// commit the harness itself pushed, which is what most tests want.
	headCommit string
	// ensureResets and mergeResets are how many times the connection carrying
	// that call drops before it goes through. They are the failure that killed
	// four runs on 2026-09-03: nothing about the request reached the forge, so
	// the class says the next attempt may well succeed.
	ensureResets int
	mergeResets  int
	// afterMergeReset runs when a merge attempt is dropped, which is the moment
	// the run then spends waiting before it asks again. It is how a test
	// expresses the world moving during that wait.
	afterMergeReset func()
	// protection is what the forge says protects the target branch, and
	// protectionErr is a forge that could not be asked. protectionAsked is every
	// branch it was asked about.
	protection      publish.BranchProtection
	protectionErr   error
	protectionAsked []string
	// onMerge runs when the forge is asked to merge, before it answers. It is
	// how a test reads where the local target stood at that moment.
	onMerge func()
}

// connectionReset is what the transport writes when it drops a request, in the
// words git and gh actually print. A test states the words rather than a class,
// because words are what the harness reads.
func connectionReset(what string) error {
	return fmt.Errorf("%s: OpenSSL SSL_read: Connection reset by peer, errno 54", what)
}

func (f *fakeForge) Availability(context.Context) (publish.Availability, error) {
	if f.reportAvailability {
		return f.availability, nil
	}
	return publish.Availability{Installed: true, Authenticated: true}, nil
}

func (f *fakeForge) Ensure(_ context.Context, request publish.Request) (publish.PullRequest, error) {
	if f.ensureResets > 0 {
		f.ensureResets--
		return publish.PullRequest{}, connectionReset("open pull request for " + request.Head)
	}
	f.opened = append(f.opened, request)
	if f.onEnsure != nil {
		f.onEnsure()
	}
	if f.number == 0 {
		f.number = 1
	}
	return publish.PullRequest{Number: f.number, URL: fmt.Sprintf("https://example.invalid/pull/%d", f.number), State: "OPEN"}, nil
}

// Merge is the forge merging the pull request, which is what moves the remote
// target branch now. The update is written inside the bare remote rather than
// pushed into it, so a branch that refuses direct pushes is merged into exactly
// as a real forge merges into one, and it is a real merge commit: a fresh
// commit whose first parent is the base, whose second parent is the published
// head, and whose tree is the head's. Modelling that rather than moving the
// base ref onto the head is the difference between exercising the harness and
// exercising an assumption — no forge merge method leaves the base at the
// commit the harness promoted.
func (f *fakeForge) Merge(_ context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	if f.mergeResets > 0 {
		f.mergeResets--
		if f.afterMergeReset != nil {
			f.afterMergeReset()
		}
		return publish.MergeResult{}, connectionReset(fmt.Sprintf("merge pull request %d", request.Number))
	}
	if f.onMerge != nil {
		f.onMerge()
	}
	if f.mergeErr != nil {
		return publish.MergeResult{}, f.mergeErr
	}
	f.merges = append(f.merges, request)
	// A queued merge accepts the request and moves nothing: what the forge does
	// with it happens after the run that asked for it has finished.
	if f.queueMerge {
		f.queued = true
		return publish.MergeResult{Queued: true}, nil
	}
	if f.remote != "" {
		if err := f.mergeIntoRemote(f.opened[len(f.opened)-1].Base, request.HeadCommit); err != nil {
			return publish.MergeResult{}, err
		}
	}
	f.merged = true
	return publish.MergeResult{}, nil
}

// PerformQueuedMerge is the forge merging a request it queued, which is what
// happens once the base branch's required checks pass — minutes after the run
// that asked for it ended.
func (f *fakeForge) PerformQueuedMerge(t *testing.T) {
	t.Helper()
	if !f.queued {
		t.Fatal("no merge is queued with the forge")
	}
	if err := f.mergeIntoRemote(f.opened[len(f.opened)-1].Base, f.merges[len(f.merges)-1].HeadCommit); err != nil {
		t.Fatalf("perform the queued merge: %v", err)
	}
	f.queued = false
	f.merged = true
}

// DropQueuedMerge is the forge giving up on a merge it queued, which is what a
// required check that failed leaves behind: an open request with nothing
// waiting to merge it.
func (f *fakeForge) DropQueuedMerge() {
	f.queued = false
}

// mergeIntoRemote writes the forge's own merge of a request into the bare
// remote. A replaying forge — GitHub's rebase and squash methods — is modelled
// by leaving the published head out of the new commit's parents, so the commit
// that was reviewed never reaches the base at all.
func (f *fakeForge) mergeIntoRemote(base, head string) error {
	tip, err := f.Git("rev-parse", "refs/heads/"+base)
	if err != nil {
		return err
	}
	tree, err := f.Git("rev-parse", head+"^{tree}")
	if err != nil {
		return err
	}
	arguments := []string{
		"-c", "user.name=Forge",
		"-c", "user.email=forge@example.invalid",
		"commit-tree", tree, "-p", tip,
	}
	if !f.replayMerge {
		arguments = append(arguments, "-p", head)
	}
	merged, err := f.Git(append(arguments, "-m", fmt.Sprintf("Merge pull request #%d", f.number))...)
	if err != nil {
		return err
	}
	_, err = f.Git("update-ref", "refs/heads/"+base, merged, tip)
	return err
}

func (f *fakeForge) Git(arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", f.remote}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in the forge: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// State reports merged once the forge has merged the request — after
// openReplies further answers of "still open", which is the forge's own record
// lagging the merge it performed rather than the merge being unfinished.
func (f *fakeForge) State(context.Context, string) (publish.PullRequest, error) {
	if f.stateErr != nil {
		return publish.PullRequest{}, f.stateErr
	}
	f.stateCalls++
	url := fmt.Sprintf("https://example.invalid/pull/%d", f.number)
	if !f.merged || f.stateCalls <= f.openReplies {
		return publish.PullRequest{Number: f.number, URL: url, State: "OPEN", AutoMerge: f.queued, HeadCommit: f.headCommit}, nil
	}
	return publish.PullRequest{Number: f.number, URL: url, State: "MERGED", Merged: true, HeadCommit: f.headCommit}, nil
}

// Protection answers what the forge says about the target branch. A forge that
// was told nothing reports it unprotected, which is the arrangement every test
// written before protection was asked about assumed.
func (f *fakeForge) Protection(_ context.Context, branch string) (publish.BranchProtection, error) {
	f.protectionAsked = append(f.protectionAsked, branch)
	if f.protectionErr != nil {
		return publish.BranchProtection{}, f.protectionErr
	}
	return f.protection, nil
}

var _ PullRequests = (*fakeForge)(nil)
