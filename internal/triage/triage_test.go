package triage

import (
	"strings"
	"testing"
	"time"
)

func stoppedRunEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassStoppedRun, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassStoppedRun,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Blocker:       "Yoyodyne stopped this item: the repair budget was spent.",
		Counters:      Counters{ReviewRounds: 3, ReviewRoundsCap: 4, RepairAttempts: 2, RepairGrantAttempts: 2},
	}
}

func publicationEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassPublication, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassPublication,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Publication: &Publication{
			Number:     42,
			URL:        "https://forge.invalid/pull/42",
			State:      "OPEN",
			ApprovedAt: time.Date(2026, 8, 19, 6, 30, 0, 0, time.UTC),
		},
		Counters: Counters{ReviewRounds: 1, ReviewRoundsCap: 4, RepairAttempts: 0, RepairGrantAttempts: 2},
	}
}

func TestAnEntryIsRefusedWhenItCannotSayWhatStopped(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name: "a stopped run with no blocker",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Blocker = ""
				return entry
			},
			want: "carries the durable blocker",
		},
		{
			name: "a publication entry with no publication",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication = nil
				return entry
			},
			want: "carries the publication",
		},
		{
			// A merged publication is the thing that was supposed to happen, and
			// docketing one would put finished work in front of somebody as
			// stopped work.
			name: "a publication the forge merged",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication.Merged = true
				return entry
			},
			want: "not stuck",
		},
		{
			name: "a publication with nothing to measure an age from",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication.ApprovedAt = time.Time{}
				return entry
			},
			want: "approved_at is required",
		},
		{
			// The key is what makes two records of one stoppage impossible, so a
			// key that names some other event is refused rather than stored.
			name: "a key that does not name the event it describes",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Key = Key(ClassStoppedRun, "run-ffffffffffffffffffffffffffffffff")
				return entry
			},
			want: "does not name the stopped_run event",
		},
		{
			name: "a class nothing dockets",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Class = "something-else"
				return entry
			},
			want: "class \"something-else\"",
		},
		{
			name: "a blocker too long to keep",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Blocker = strings.Repeat("x", MaxBlockerBytes+1)
				return entry
			},
			want: "blocker is",
		},
		{
			name: "more findings than an entry carries",
			entry: func() Entry {
				entry := stoppedRunEntry()
				for range MaxFindings + 1 {
					entry.Findings = append(entry.Findings, Finding{Severity: "blocker", Message: "fix it"})
				}
				return entry
			},
			want: "exceeds the bound",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

func TestAWellFormedEntryOfEitherClassIsAccepted(t *testing.T) {
	t.Parallel()

	// A merge the forge performed that the harness could not finish is the one
	// merged publication that still needs a person, and it says so by carrying
	// what the harness recorded about it.
	unconfirmed := publicationEntry()
	unconfirmed.Publication.Merged = true
	unconfirmed.Publication.Message = "the merge landed and confirming it failed"

	for _, entry := range []Entry{stoppedRunEntry(), publicationEntry(), unconfirmed} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for %s", err, entry.Class)
		}
	}
}

// The key is derived from the event rather than generated, which is the whole
// of what makes docketing idempotent: two processes that notice one stoppage
// have to produce the same key without talking to each other.
func TestOneEventHasOneKeyWhoeverDerivesIt(t *testing.T) {
	t.Parallel()

	run := "run-0123456789abcdef0123456789abcdef"
	if Key(ClassStoppedRun, run) != Key(ClassStoppedRun, " "+run+" ") {
		t.Fatalf("the same stoppage produced two keys")
	}
	if Key(ClassStoppedRun, run) == Key(ClassPublication, run) {
		t.Fatalf("a run's stoppage and its publication share a key, so one would hide the other")
	}
}

func TestARenderedEntryCarriesTheEvidenceSomebodyDecidesOn(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.Summary = "the change misses the acceptance criteria"
	entry.Findings = []Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}}
	entry.Check = &Check{Command: "make test", ExitCode: 1, Output: "FAIL\tinternal/thing"}
	entry.Artifacts = Artifacts{
		Branch:       "yoyodyne/task/abc",
		WorktreePath: "/state/worktrees/task",
		TargetBranch: "main",
	}
	rendered := entry.Render()
	for _, want := range []string{
		"stopped run",
		"yoyodyne-task",
		"the repair budget was spent",
		"the change misses the acceptance criteria",
		"Finding [blocker] (feature.txt:1): add the missing file",
		"Failing check: make test (exit 1)",
		"FAIL\tinternal/thing",
		"Branch (preserved): yoyodyne/task/abc",
		"Worktree (preserved): /state/worktrees/task",
		"Integration target: main",
		"3 of 4 review round(s) used",
		"2 repair attempt(s) spent",
		"a grant would hand it 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}
}

// An artifact the harness already removed is named as removed rather than
// omitted: a development manager sent after a worktree that is gone finds that
// out by going there, which is the errand the docket exists to remove.
func TestARenderedEntrySaysWhichArtifactsAreStillThere(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.Artifacts = Artifacts{
		Branch: "yoyodyne/task/abc", BranchRemoved: true,
		WorktreePath: "/state/worktrees/task", WorktreeRemoved: true,
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "Branch (removed)") || !strings.Contains(rendered, "Worktree (removed)") {
		t.Fatalf("rendered entry describes removed artifacts as preserved:\n%s", rendered)
	}
}

func TestARenderedPublicationSaysWhatTheForgeDidAndHowLongItHasBeenSitting(t *testing.T) {
	t.Parallel()

	entry := publicationEntry()
	entry.Publication.MergeQueued = true
	entry.Publication.Message = "the forge dropped the queued merge of pull request 42"
	rendered := entry.Render()
	for _, want := range []string{
		"unfinished publication",
		"Pull request #42 https://forge.invalid/pull/42",
		"Forge state: OPEN, the forge has its merge queued",
		"unmerged for 5h30m when docketed",
		"Forge merge message: the forge dropped the queued merge",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered publication is missing %q:\n%s", want, rendered)
		}
	}
}

// The counters exist so a decision is made against the budget rather than
// against the evidence alone, so an item at its cap says so where the decision
// is made rather than leaving it to be worked out from two numbers.
func TestARenderedEntrySaysWhenTheReviewRoundCapIsReached(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	if strings.Contains(entry.Render(), "the cap is reached") {
		t.Fatalf("an item inside its cap was rendered as having reached it:\n%s", entry.Render())
	}
	entry.Counters.ReviewRounds = entry.Counters.ReviewRoundsCap
	if !strings.Contains(entry.Render(), "another repair is not triage's to grant") {
		t.Fatalf("an item at its cap did not say so:\n%s", entry.Render())
	}
}

func TestAnAgeIsStatedTheWaySomebodyReadsIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		age  time.Duration
		want string
	}{
		{age: 30 * time.Second, want: "30s"},
		{age: 90 * time.Minute, want: "1h30m"},
		{age: 50 * time.Hour, want: "2d2h"},
		// A clock that went backwards between the run ending and the docket
		// being built is not a negative age; it is no age at all.
		{age: -time.Hour, want: "0s"},
	} {
		if got := describeAge(test.age); got != test.want {
			t.Fatalf("describeAge(%s) = %q, want %q", test.age, got, test.want)
		}
	}
}
