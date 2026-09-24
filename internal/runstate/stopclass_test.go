package runstate

import "testing"

// The reason a surface prints leads with the class, and each half survives the
// other's absence: a record written before the class existed prints its reason
// as it was written, and a succeeded run whose only account is what it stopped
// short of prints that account under its class.
func TestTheReasonLeadsWithTheClassThatStoppedTheRun(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		summary RunSummary
		want    string
	}{
		{
			name:    "a failure under its class",
			summary: RunSummary{StopClass: StopChecks, Failure: "verification failed after 2 of 2 permitted attempt(s): make test exited with 1"},
			want:    "checks: verification failed after 2 of 2 permitted attempt(s): make test exited with 1",
		},
		{
			name:    "a record written before the class existed",
			summary: RunSummary{Failure: "developer backend failed: signal: killed"},
			want:    "developer backend failed: signal: killed",
		},
		{
			name:    "a late completion record on a succeeded run",
			summary: RunSummary{StopClass: StopRecording, CompletionRecordingFailure: "save completed run state after cleanup: disk full"},
			want:    "recording: save completed run state after cleanup: disk full",
		},
		{
			name:    "an outstanding publication on a succeeded run",
			summary: RunSummary{StopClass: StopPublish, PublishFailure: "confirm the pull request merged: the forge is unreachable"},
			want:    "publish: confirm the pull request merged: the forge is unreachable",
		},
		{
			name:    "an outstanding cleanup on a succeeded run",
			summary: RunSummary{StopClass: StopCleanup, CleanupFailure: "remove worktree: directory is busy"},
			want:    "cleanup: remove worktree: directory is busy",
		},
		{
			// The run's own failure is its reason whatever else the record holds.
			name:    "a failure beside a bookkeeping account",
			summary: RunSummary{StopClass: StopRecording, Failure: "close integrated work item: bd close failed", CompletionRecordingFailure: "disk full"},
			want:    "recording: close integrated work item: bd close failed",
		},
		{
			name:    "a class with nothing beside it",
			summary: RunSummary{StopClass: StopCancelled},
			want:    "cancelled",
		},
		{
			name:    "a run nothing stopped",
			summary: RunSummary{},
			want:    "",
		},
	} {
		if got := test.summary.Reason(); got != test.want {
			t.Errorf("%s: Reason() = %q, want %q", test.name, got, test.want)
		}
	}
}
