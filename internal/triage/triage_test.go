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

func stoppageClosure() Closure {
	return Closure{
		SchemaVersion: ClosureSchemaVersion,
		Key:           Key(ClassStoppedRun, "run-0123456789abcdef0123456789abcdef"),
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		Decision:      "escalate",
		Reason:        "the findings dispute the item's criteria",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
	}
}

// A closure takes a stoppage off the docket, so what it must never be is a
// record that cannot say which stoppage, what was decided, or by whom: each of
// those missing is an entry nobody is looking at any more with nothing saying
// why.
func TestAClosureIsRefusedWhenItCannotSayWhatWasSettledOrByWhom(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		closure func(Closure) Closure
		want    string
	}{
		{
			name:    "no entry",
			closure: func(c Closure) Closure { c.Key = "  "; return c },
			want:    "key is required",
		},
		{
			name:    "no decision",
			closure: func(c Closure) Closure { c.Decision = ""; return c },
			want:    "the decision that settled this stoppage is required",
		},
		{
			name:    "prose where a decision was expected",
			closure: func(c Closure) Closure { c.Decision = strings.Repeat("x", MaxDecisionBytes+1); return c },
			want:    "decision is",
		},
		{
			name:    "nobody",
			closure: func(c Closure) Closure { c.DecidedBy = ""; return c },
			want:    "who decided this is required",
		},
		{
			name:    "no moment",
			closure: func(c Closure) Closure { c.ClosedAt = time.Time{}; return c },
			want:    "closed_at is required",
		},
		{
			name:    "reasoning past the bound",
			closure: func(c Closure) Closure { c.Reason = strings.Repeat("x", MaxMessageBytes+1); return c },
			want:    "reason is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.closure(stoppageClosure()).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q refused", err, test.want)
			}
		})
	}
	if err := stoppageClosure().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a well-formed closure accepted", err)
	}
}

// An entry that has been decided must never render as one nobody has looked at.
// A closed entry is not listed on the development manager's docket at all, so
// this is what says so wherever else one is shown.
func TestARenderedEntrySaysTheDecisionThatClosedIt(t *testing.T) {
	t.Parallel()

	closed := stoppedRunEntry()
	settled := stoppageClosure()
	closed.Closed = &settled
	rendered := closed.Render()
	for _, want := range []string{
		"Closed: decided as \"escalate\"",
		"the development manager in conversation",
		"2026-08-20T09:00:00Z",
		"dispute the item's criteria",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered entry is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(stoppedRunEntry().Render(), "Closed:") {
		t.Fatalf("an undecided entry rendered as closed:\n%s", stoppedRunEntry().Render())
	}
}
