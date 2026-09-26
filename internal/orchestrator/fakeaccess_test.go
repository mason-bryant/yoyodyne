package orchestrator

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// The fixtures and helpers several test files share are typed on what they ask
// of a fake rather than on a fake, so a file can move from this package's fakes
// onto orchestratortest's while the files beside it have not. What a shared
// piece reads or arranges beyond the interface the orchestrator asks of it goes
// through one of the accessors below, and both sets of fakes answer each of
// them.

// recordingTracker is a work tracker whose record a test can read and move.
type recordingTracker interface {
	WorkTracker
	Record() orchestratortest.TrackerRecord
	SetItemStatus(status string)
	ForgetSettlement()
}

// recordingBackend is a provider that remembers every invocation it served and
// the session it served the developer under.
type recordingBackend interface {
	backend.Backend
	RequestsMade() []backend.RunRequest
	DeveloperSessionID() string
}

// queuedForge is a forge a test reads what it was asked and arranges how it
// answers, including the merges it queues and later performs or drops.
type queuedForge interface {
	PullRequests
	OpenedRequests() []publish.Request
	MergeRequests() []publish.MergeRequest
	HoldsQueuedMerge() bool
	HoldQueuedMerge()
	PerformQueuedMerge(t *testing.T)
	DropQueuedMerge()
	ForgetMerges()
	MergeByHand(base, head string) error
	Git(arguments ...string) (string, error)
	SetQueueMerge(queue bool)
	SetReplayMerge(replay bool)
	SetMergeErr(err error)
	SetHeadCommit(commit string)
	SetTargetProtection(protection publish.BranchProtection)
	SetOnMerge(onMerge func())
}

var (
	_ recordingTracker = (*fakeTracker)(nil)
	_ recordingTracker = (*orchestratortest.Tracker)(nil)
	_ recordingBackend = (*fakeBackend)(nil)
	_ recordingBackend = (*orchestratortest.Backend)(nil)
	_ queuedForge      = (*fakeForge)(nil)
	_ queuedForge      = (*orchestratortest.Forge)(nil)
)

func (f *fakeTracker) Record() orchestratortest.TrackerRecord {
	return orchestratortest.TrackerRecord{
		Item: f.item, Claimed: f.claimed, Notes: f.notes, NoteRecords: f.noteRecords,
		Closed: f.closed, CloseReason: f.closeReason, Blocked: f.blocked, BlockReason: f.blockReason,
		Calls: f.calls,
	}
}

func (f *fakeTracker) SetItemStatus(status string) {
	f.item.Status = status
}

func (f *fakeTracker) ForgetSettlement() {
	f.blocked, f.blockReason, f.closed = false, "", false
}

func (f *fakeBackend) RequestsMade() []backend.RunRequest {
	return f.requests
}

func (f *fakeBackend) DeveloperSessionID() string {
	return f.developerSession
}

func (f *fakeForge) OpenedRequests() []publish.Request {
	return f.opened
}

func (f *fakeForge) MergeRequests() []publish.MergeRequest {
	return f.merges
}

func (f *fakeForge) HoldsQueuedMerge() bool {
	return f.queued
}

func (f *fakeForge) HoldQueuedMerge() {
	f.queued = true
}

func (f *fakeForge) ForgetMerges() {
	f.queued = false
	f.merges = nil
}

func (f *fakeForge) MergeByHand(base, head string) error {
	if err := f.mergeIntoRemote(base, head); err != nil {
		return err
	}
	f.queued, f.merged = false, true
	return nil
}

func (f *fakeForge) SetQueueMerge(queue bool) { f.queueMerge = queue }

func (f *fakeForge) SetReplayMerge(replay bool) { f.replayMerge = replay }

func (f *fakeForge) SetMergeErr(err error) { f.mergeErr = err }

func (f *fakeForge) SetHeadCommit(commit string) { f.headCommit = commit }

func (f *fakeForge) SetTargetProtection(protection publish.BranchProtection) {
	f.protection = protection
}

func (f *fakeForge) SetOnMerge(onMerge func()) { f.onMerge = onMerge }
