package triage

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
			name: "a stopped run with neither a blocker nor a failure",
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
	// A publication is the run and the pull request together, so two publications
	// of one run are two events rather than one entry standing for both.
	if PublicationKey(run, 42) == PublicationKey(run, 43) {
		t.Fatalf("two publications of one run share a key, so one would hide the other")
	}
	if !strings.HasPrefix(PublicationKey(run, 42), Key(ClassPublication, run)) {
		t.Fatalf("a publication key does not name the publication event: %s", PublicationKey(run, 42))
	}
}

// A publication entry names the run and the pull request it is about, and the
// entries already on the docket name the run alone. Both are accepted, because
// the docket is an append-only log that nothing rewrites: refusing the older
// form would make every docket carrying one unreadable.
func TestAPublicationEntryIsAcceptedUnderEitherKeyItCanCarry(t *testing.T) {
	t.Parallel()

	current := publicationEntry()
	current.Key = PublicationKey(current.RunID, current.Publication.Number)
	if err := current.Validate(); err != nil {
		t.Fatalf("Validate() error = %v for the key the harness now writes", err)
	}
	// The key of some other pull request of the same run is not this entry's, and
	// is what the check exists to catch.
	other := publicationEntry()
	other.Key = PublicationKey(other.RunID, other.Publication.Number+1)
	if err := other.Validate(); err == nil || !strings.Contains(err.Error(), "does not name the publication event") {
		t.Fatalf("Validate() error = %v, want the mismatched publication refused", err)
	}
}

// The docket is where a decision is made, so what has already been decided has
// to be on it. A guard that refuses a second re-run while the entry shows
// nothing recorded is how one authorized recovery is spent twice.
func TestARenderedEntrySaysWhatTriageHasAlreadyDecided(t *testing.T) {
	t.Parallel()

	decided := stoppedRunEntry()
	decided.Counters.RepairGrants, decided.Counters.RepairGrantsCap = 1, 1
	decided.Counters.Reruns, decided.Counters.RerunsCap = 1, 1
	decided.Counters.MergeRearmsCap = 1
	for _, want := range []string{
		"1 of 1 repair grant(s)",
		"1 of 1 re-run(s), 0 carried out",
		"0 of 1 merge re-arm(s)",
		"already recorded and not yet carried out",
	} {
		if rendered := decided.Render(); !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// Once it has been carried out against this stoppage, the entry says that
	// instead: the counter is a total nothing clears, so it cannot say on its own
	// whether this stoppage may still be run again.
	carriedOut := decided
	carriedOut.Counters.RerunsCarriedOut = 1
	carriedOut.Rerun = &Rerun{
		ClaimedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC),
		RunID:     "run-fedcba9876543210fedcba9876543210",
	}
	rendered := carriedOut.Render()
	if !strings.Contains(rendered, "already re-run as run run-fedcba9876543210fedcba9876543210") {
		t.Fatalf("rendered entry does not say this stoppage was re-run:\n%s", rendered)
	}
	if strings.Contains(rendered, "not yet carried out") {
		t.Fatalf("a spent decision was rendered as one still standing:\n%s", rendered)
	}

	// A claim whose fresh run never existed is still a claim, and the entry says
	// so rather than reading as a stoppage nothing was done about.
	unstarted := carriedOut
	unstarted.Rerun = &Rerun{ClaimedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)}
	if rendered := unstarted.Render(); !strings.Contains(rendered, "no fresh run was recorded for it") {
		t.Fatalf("rendered entry does not say the claim started nothing:\n%s", rendered)
	}
}

// A grant is more than a count of grants, and the rest of it is what the harness
// already told whoever recorded the decision: how many rounds it came to, and
// whether the cap cut it. An entry that carried the count alone disagreed with
// the sentence the development manager had just been handed.
func TestARenderedEntrySaysWhatARecordedGrantCameTo(t *testing.T) {
	t.Parallel()

	// The record left behind by "the grant was cut from 2 round(s) to the 1 the
	// cap still had room for": one grant, worth one round, cut, committing the
	// item to the whole of its cap.
	granted := stoppedRunEntry()
	granted.Counters.RepairGrants, granted.Counters.RepairGrantsCap = 1, 1
	granted.Counters.GrantedRounds, granted.Counters.TruncatedGrants = 1, 1
	granted.Counters.CommittedRounds = 4
	rendered := granted.Render()
	for _, want := range []string{
		"1 of 1 repair grant(s) worth 1 review round(s), 1 of them cut down to the room the cap still had",
		"4 of 4 are committed by a grant not yet spent",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// A grant given in full says what it was worth and nothing about a cut that
	// did not happen.
	full := stoppedRunEntry()
	full.Counters.RepairGrants, full.Counters.RepairGrantsCap = 1, 2
	full.Counters.GrantedRounds = 2
	if rendered := full.Render(); !strings.Contains(rendered, "1 of 2 repair grant(s) worth 2 review round(s);") {
		t.Fatalf("rendered entry does not say what the grant was worth:\n%s", rendered)
	}
	if rendered := full.Render(); strings.Contains(rendered, "cut down") {
		t.Fatalf("a grant given in full was rendered as one the cap cut:\n%s", rendered)
	}
}

// The counts say what has been decided; they do not say what deciding it again
// would meet. Leaving that to the guard is the round-trip this docket exists to
// remove: the development manager reads the entry, decides, and is refused by a
// budget the entry could have shown them.
func TestARenderedEntrySaysWhatARepeatedDecisionWouldMeet(t *testing.T) {
	t.Parallel()

	// An item nothing has been granted says nothing about grants: an entry that
	// announced every untouched budget is one a reader learns to skip.
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "repair grant for") {
		t.Fatalf("an item with no grant recorded was rendered as standing somewhere with them:\n%s", rendered)
	}

	// Its own budget refuses first, exactly as the guard asks it first.
	spent := stoppedRunEntry()
	spent.Counters.RepairGrants, spent.Counters.RepairGrantsCap = 1, 1
	spent.Counters.GrantedRounds = 1
	if rendered := spent.Render(); !strings.Contains(rendered,
		"A further repair grant for yoyodyne-task is refused: 1 of 1 permitted grant(s) are already recorded") {
		t.Fatalf("rendered entry does not say a further grant is refused:\n%s", rendered)
	}

	// Both spent is said as both. Naming one of them sends the operator to cross
	// it and the same decision back to be refused by the other, which is the two
	// override ceremonies minutes apart that this line exists to prevent.
	both := stoppedRunEntry()
	both.Counters.RepairGrants, both.Counters.RepairGrantsCap = 1, 1
	both.Counters.GrantedRounds, both.Counters.CommittedRounds = 1, 4
	if rendered := both.Render(); !strings.Contains(rendered,
		"refused by both of its budgets: 1 of 1 permitted grant(s) are already recorded, and 4 of 4 round(s) are spent or committed") {
		t.Fatalf("rendered entry does not say both budgets refuse a further grant:\n%s", rendered)
	}

	// With its own budget to spare, the round budget is what refuses, and the
	// figure it refuses against is what the item is committed to.
	committed := stoppedRunEntry()
	committed.Counters.RepairGrants, committed.Counters.RepairGrantsCap = 1, 2
	committed.Counters.GrantedRounds, committed.Counters.CommittedRounds = 1, 4
	if rendered := committed.Render(); !strings.Contains(rendered,
		"refused by the review round budget: 4 of 4 round(s) are spent or committed") {
		t.Fatalf("rendered entry does not say the round budget refuses a further grant:\n%s", rendered)
	}

	// A grant with room left and rounds unspent is a decision standing rather
	// than one to take again.
	standing := stoppedRunEntry()
	standing.Counters.ReviewRoundsCap = 6
	standing.Counters.RepairGrants, standing.Counters.RepairGrantsCap = 1, 2
	standing.Counters.GrantedRounds, standing.Counters.CommittedRounds = 2, 5
	if rendered := standing.Render(); !strings.Contains(rendered,
		"is recorded and its rounds are not spent, so this stoppage may be handed back on the decision that stands") {
		t.Fatalf("rendered entry does not say the grant still stands:\n%s", rendered)
	}

	// The re-arms are the third budget, and silent until one is spent.
	rearmed := stoppedRunEntry()
	rearmed.Counters.MergeRearms, rearmed.Counters.MergeRearmsCap = 2, 2
	if rendered := rearmed.Render(); !strings.Contains(rendered,
		"A further merge re-arm for yoyodyne-task is refused: 2 of 2 permitted re-arm(s) are already recorded") {
		t.Fatalf("rendered entry does not say a further re-arm is refused:\n%s", rendered)
	}
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "merge re-arm for") {
		t.Fatalf("an item with no re-arm recorded was rendered as standing somewhere with them:\n%s", rendered)
	}
}

// A triage record that could not be read is stated. Rendering it as an item with
// nothing decided about it is the one reading that turns an unreadable record
// into a decision taken twice.
func TestARenderedEntrySaysWhenTheTriageRecordCouldNotBeRead(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.CountersProblem = "read what triage has recorded about yoyodyne-task: permission denied"
	rendered := entry.Render()
	if !strings.Contains(rendered, "Triage decisions could not be read: read what triage has recorded") {
		t.Fatalf("rendered entry does not say the record could not be read:\n%s", rendered)
	}
	if strings.Contains(rendered, "Triage decisions recorded") {
		t.Fatalf("an unreadable record was rendered as decisions:\n%s", rendered)
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

// An entry outlives the run that produced it, so a reader who finds one has the
// identifier and whatever the entry says in words. It says what the item is
// called where the run recorded it, and the identifier alone where it did not.
func TestARenderedEntryNamesTheItemInWordsWhereTheRunRecordedThem(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.WorkItemTitle = "Slack thread headers carry the item's title"
	rendered := entry.Render()
	if !strings.Contains(rendered, "yoyodyne-task — Slack thread headers carry the item's title") {
		t.Fatalf("rendered entry does not name the item in words:\n%s", rendered)
	}
	untitled := stoppedRunEntry().Render()
	if !strings.Contains(untitled, "on yoyodyne-task (") {
		t.Fatalf("an entry with no recorded title does not name the item alone:\n%s", untitled)
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

// A run that died holding its change left the work item carrying no blocker,
// because nothing got as far as recording one. The entry says what stopped it
// all the same, and says which of the two it is: a reader sent to the item for a
// blocker nobody wrote there is a reader who concludes the entry is wrong.
func TestAStoppedRunEntryStandsOnTheFailureWhereNothingRecordedABlocker(t *testing.T) {
	t.Parallel()

	died := stoppedRunEntry()
	died.Blocker = ""
	died.Failure = "publish the developer branch: remote rejected the push: Connection reset"
	if err := died.Validate(); err != nil {
		t.Fatalf("Validate() refused an entry that says what stopped the run: %v", err)
	}
	rendered := died.Render()
	if !strings.Contains(rendered, died.Failure) {
		t.Fatalf("the rendered entry does not say what stopped the run:\n%s", rendered)
	}
	if !strings.Contains(rendered, "carries no blocker") {
		t.Fatalf("the rendered entry does not say the item carries no blocker:\n%s", rendered)
	}
}

// And on the ordinary stoppage the blocker is the whole of it. The failure a run
// recorded says the same thing in different words, and an entry printing both
// would be the same fact twice on nearly every entry there is.
func TestAStoppedRunEntryWithABlockerDoesNotAlsoPrintTheFailure(t *testing.T) {
	t.Parallel()

	stopped := stoppedRunEntry()
	if strings.Contains(stopped.Render(), "carries no blocker") {
		t.Fatalf("an entry with a blocker claimed the item carries none:\n%s", stopped.Render())
	}
}

// The failure is held to the bound the blocker beside it is, and for the same
// reason: an entry too big to record is a stoppage that reaches nobody.
func TestAnEntryIsRefusedWhenItsFailureExceedsTheBound(t *testing.T) {
	t.Parallel()

	died := stoppedRunEntry()
	died.Blocker = ""
	died.Failure = strings.Repeat("x", MaxBlockerBytes+1)
	err := died.Validate()
	if err == nil || !strings.Contains(err.Error(), "failure is") {
		t.Fatalf("Validate() error = %v, want the failure refused for its length", err)
	}
}

func unreadyEntry() Entry {
	read := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	prerequisites := []Prerequisite{{
		Kind:     "forbidden-by-ruling",
		Missing:  `it says of itself: "Blocked until the architect's answer exists"`,
		Evidence: "the sentence is in the item's own statement, and nothing in the tracker records it as a dependency",
		Decides:  "the product manager, or the development manager who records the dependency",
	}}
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           UnreadyKey("yoyodyne-ifd.100.1", []string{"forbidden-by-ruling"}),
		Class:         ClassUnreadyItem,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.100.1",
		WorkItemTitle: "Commit and publish an approved artifact write",
		RecordedAt:    read,
		Unready:       &Unready{Prerequisites: prerequisites, ReadAt: read},
		Counters:      Counters{ReviewRounds: 0, ReviewRoundsCap: 4, RepairGrantAttempts: 2},
	}
}

// The one entry on this docket with no run behind it. It is valid without one
// precisely because nothing ran: an entry that had to name a run could only be
// written by the run this exists to save.
func TestAnUnreadyItemEntryNamesNoRun(t *testing.T) {
	t.Parallel()

	entry := unreadyEntry()

	if err := entry.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want an entry about work that never started to be valid", err)
	}
	withRun := unreadyEntry()
	withRun.RunID = "run-0123456789abcdef0123456789abcdef"
	if err := withRun.Validate(); err == nil || !strings.Contains(err.Error(), "names no run") {
		t.Fatalf("Validate() error = %v, want an unready entry carrying a run to be refused", err)
	}
}

// The evidence that makes the class the thing it claims to be. An entry that
// cannot say what the item asks for is one nobody can act on.
func TestAnUnreadyItemEntryIsRefusedWithoutItsPrerequisites(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name:  "nothing recorded at all",
			entry: func() Entry { entry := unreadyEntry(); entry.Unready = nil; return entry },
			want:  "carries the prerequisites",
		},
		{
			name: "an empty list",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready = &Unready{ReadAt: entry.RecordedAt}
				return entry
			},
			want: "names at least one unmet prerequisite",
		},
		{
			name: "a prerequisite that does not say what is missing",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready.Prerequisites[0].Missing = ""
				return entry
			},
			want: "what is missing is required",
		},
		{
			name: "no reading time, so the entry reads as a standing fact",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready.ReadAt = time.Time{}
				return entry
			},
			want: "read_at is required",
		},
		{
			name:  "evidence that belongs to a run",
			entry: func() Entry { entry := unreadyEntry(); entry.Blocker = "something stopped"; return entry },
			want:  "work that never started",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

// The key is the item and what was found about it, so a session that meets the
// same unready item at every poll dockets it once — and one that later finds a
// different kind of prerequisite unmet dockets that as the separate finding it
// is. The kinds are sorted so that the order they were read in decides nothing.
func TestAnUnreadyItemIsKeyedByTheItemAndWhatWasFound(t *testing.T) {
	t.Parallel()

	first := UnreadyKey("yoyodyne-ifd.100.1", []string{"stale-pinpoint", "forbidden-by-ruling"})
	again := UnreadyKey("yoyodyne-ifd.100.1", []string{"forbidden-by-ruling", "stale-pinpoint"})
	if first != again {
		t.Fatalf("keys = %q and %q, want the order they were read in to decide nothing", first, again)
	}
	if other := UnreadyKey("yoyodyne-ifd.100.1", []string{"stale-pinpoint"}); other == first {
		t.Fatalf("key = %q, want a different finding about one item to be a different entry", other)
	}
	if !strings.Contains(first, "yoyodyne-ifd.100.1") || len(first) > MaxKeyBytes {
		t.Fatalf("key = %q, want it to name the item and stay inside the bound", first)
	}
	// A key that does not derive from what the entry describes would make two
	// records of one finding, which is what the derivation exists to prevent.
	entry := unreadyEntry()
	entry.Key = "unready_item:yoyodyne-ifd.100.1:something-else"
	if err := entry.Validate(); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("Validate() error = %v, want a key that does not derive from the entry to be refused", err)
	}
}

// What a development manager reads. The run is not named, because there is none
// and an empty pair of brackets reads as a run whose identifier nobody recorded;
// and the reading is dated, because this is the one entry whose subject can go
// out of date without anybody touching the item.
func TestAnUnreadyItemRendersAsWorkThatNeverStarted(t *testing.T) {
	t.Parallel()

	rendered := unreadyEntry().Render()

	for _, required := range []string{
		"item the tree is not ready for",
		"nothing ran",
		"the tree was read at 2026-09-06T09:00:00Z",
		"Unmet [forbidden-by-ruling]",
		"Blocked until the architect's answer exists",
		"Who releases it",
		"the development manager who records the dependency",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, required)
		}
	}
}

// escalationEntry is a role's judgement that the item cannot be met as it
// stands: no blocker, no findings, no publication, and the account the
// development manager decides from.
func escalationEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassEscalation, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassEscalation,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.100.1",
		WorkItemTitle: "Convert the management anchors",
		RecordedAt:    time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		Escalation: &Escalation{
			RaisedBy: domain.RoleReviewer,
			Reason:   "the acceptance criteria ask for the conversion the entanglement ruling forbade, so no change here can meet them",
		},
		Counters: Counters{ReviewRounds: 1, ReviewRoundsCap: 4},
	}
}

// The judgement is the whole of the entry, so an entry that cannot carry it is
// one she can read and not decide from.
func TestAnEscalationEntryCarriesTheJudgementItIsAbout(t *testing.T) {
	t.Parallel()

	if err := escalationEntry().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, refused := range []struct {
		name  string
		entry func(Entry) Entry
		says  string
	}{
		{
			name:  "no judgement at all",
			entry: func(e Entry) Entry { e.Escalation = nil; return e },
			says:  "carries the judgement",
		},
		{
			name:  "no account of it",
			entry: func(e Entry) Entry { e.Escalation.Reason = "  "; return e },
			says:  "the reason is required",
		},
		{
			// Every other role decides about work rather than doing it, so an entry
			// naming one describes an escalation nothing in a run could have raised.
			name:  "raised by a role that is not in a run",
			entry: func(e Entry) Entry { e.Escalation.RaisedBy = domain.RoleProductManager; return e },
			says:  "is not one of the roles that raises one",
		},
		{
			name:  "carrying a publication",
			entry: func(e Entry) Entry { e.Publication = &Publication{Number: 4}; return e },
			says:  "rather than a publication",
		},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()
			entry := escalationEntry()
			entry.Escalation = &Escalation{RaisedBy: entry.Escalation.RaisedBy, Reason: entry.Escalation.Reason}
			err := refused.entry(entry).Validate()
			if err == nil || !strings.Contains(err.Error(), refused.says) {
				t.Fatalf("Validate() error = %v, want it to say %q", err, refused.says)
			}
		})
	}
}

// What a development manager reads. It has to say what she is being asked for —
// a decision about the item rather than about a change — and what the escalation
// cost, because every other entry on this docket is work that spent its budget
// before anybody heard about it.
func TestAnEscalationRendersAsADecisionAboutTheItem(t *testing.T) {
	t.Parallel()

	rendered := escalationEntry().Render()

	for _, required := range []string{
		"item raised as unmeetable",
		"Nothing was integrated",
		"the reviewer judged this item cannot be met as it stands",
		"in the round it reached",
		"replan, park, resequence, or redirect",
		"the entanglement ruling forbade",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, required)
		}
	}
}
