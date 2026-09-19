package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// countingProcess is who the tests below charge their rounds under. Which
// process charged a round matters only where a second one is in play, which is
// what TestARoundIsGivenBackOnlyToTheProcessThatChargedIt is about; everywhere
// else a round has to have been charged by somebody and this is who.
const countingProcess = "pid-1-000000000000000a"

// decidedRunID is the stoppage the decisions below are about. Every spend is
// authorized by a decision now, and the tests here are about the budgets rather
// than about which stoppage was decided, so one run stands for all of them.
const decidedRunID = "run-11112222333344445555666677778888"

// otherStoppageRunID is a second stopped run of the same item, for the tests
// where which stoppage a decision is about is the point: a decision about this
// run supersedes nothing decided about decidedRunID.
const otherStoppageRunID = "run-99998888777766665555444433332222"

// triageDecided is one decision as the durable record requires it: the word, the
// stoppage, the reasoning, and who recorded it where.
func triageDecided(decision, runID string) TriageDecision {
	return TriageDecision{
		Decision:     decision,
		RunID:        runID,
		Reason:       "the reasoning the development manager recorded for this decision",
		DecidedBy:    "development manager",
		Conversation: "chat-0123456789abcdef",
		Turn:         3,
	}
}

func newTriageStore(t *testing.T) *TriageStore {
	t.Helper()
	store, err := NewTriageStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	return store
}

// An item nothing has been triaged about is the ordinary case, and it has to
// read as an empty budget rather than as a failure to look: every item starts
// there, so an error would make the first read of every item an error.
func TestTriageCountersOfAnUntriagedItemAreEmptyRatherThanMissing(t *testing.T) {
	t.Parallel()

	counters, err := newTriageStore(t).Counters("yoyodyne-ifd.102.4")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Passes() != 0 || counters.ReviewRounds != 0 {
		t.Fatalf("Counters() = %+v, want an empty budget", counters)
	}
	if counters.WorkItemID != "yoyodyne-ifd.102.4" {
		t.Fatalf("Counters() work item = %q, want the item asked about", counters.WorkItemID)
	}
	if counters.TriagedAgain() {
		t.Fatal("TriagedAgain() on an untriaged item = true")
	}
}

// The whole reason the counters are not kept on a run: a run ends, its record is
// cleaned up, and the process that wrote it exits, and what the item has been
// given has to still be there afterwards. A second store over the same root is
// what a later process is.
func TestTriageCountersSurviveTheProcessThatWroteThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	if _, err := first.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if _, err := first.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}

	second, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	counters, err := second.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 || counters.Reruns != 1 {
		t.Fatalf("Counters() = %+v, want the round and the re-run a previous process recorded", counters)
	}
}

// A round is a verdict a developer attempt produced. The same attempt judged
// again -- a review resumed after an interrupted process, or re-obtained on a
// change replayed onto a moved target -- is one round, because charging the item
// for the second would charge it for a race it did not cause and for a process
// that died under it.
//
// The repeat is always of the attempt most recently counted, which is what the
// record compares against. Nothing can put another round of the same item in
// between: a run reserves its item exclusively, so no second run of it is in
// flight to produce one.
func TestAReviewRoundIsCountedOncePerConsecutiveDeveloperAttempt(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	for _, attempt := range []string{"run-a#0", "run-a#0", "run-a#1", "run-a#1", "run-b#0"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// Three distinct attempts, one of them in a second run: the count spans runs,
	// which is what a run's own repair budget cannot do.
	if counters.ReviewRounds != 3 {
		t.Fatalf("ReviewRounds = %d, want 3 -- one per distinct developer attempt", counters.ReviewRounds)
	}
}

// An approval costs the item nothing and is still recorded, and that record is
// what excludes the one thing that re-reviews an approved attempt: the
// integration replay. A promotion that lost its race replays the change and asks
// for a fresh verdict on the same developer attempt, and that verdict can be a
// repair because the ground moved. Charging it would charge the item for a race
// it did not cause, so the attempt has to be one the record already holds.
func TestAnApprovedAttemptIsRememberedAndTheReplayOfItIsFree(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	approved, err := store.RecordUnchargedVerdict(context.Background(), "yoyodyne-ifd.224", "run-a#0", time.Now())
	if err != nil {
		t.Fatalf("RecordUnchargedVerdict() error = %v", err)
	}
	if approved.ReviewRounds != 0 || approved.LastRound != "" || approved.LastRoundCharger != "" {
		t.Fatalf("counters after an approval = %#v, want nothing charged", approved)
	}
	if approved.LastJudged != "run-a#0" {
		t.Fatalf("LastJudged = %q, want the attempt the reviewer answered about", approved.LastJudged)
	}

	// The replay's fresh verdict, sending the same attempt back.
	replayed, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.224", "run-a#0", countingProcess, time.Now())
	if err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if replayed.ReviewRounds != 0 {
		t.Fatalf("ReviewRounds = %d, want none: the replay judged an attempt the item had already been answered about", replayed.ReviewRounds)
	}

	// The repair that verdict asked for is a new attempt, and it is chargeable
	// like any other: the exclusion is about one attempt, not about the item.
	repaired, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.224", "run-a#1", countingProcess, time.Now())
	if err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if repaired.ReviewRounds != 1 || repaired.LastRound != "run-a#1" {
		t.Fatalf("counters after the repair was judged = %#v, want the one round it cost", repaired)
	}
}

// A record written before approvals were remembered names its attempt only at
// the round it charged, and an item mid-flight when the executable changed under
// it must not have its resumed review charged a second time. So the
// deduplication asks the counted round as well as the judged attempt.
func TestARecordWrittenBeforeApprovalsWereRememberedStillDeduplicates(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	// The shape such a record has: a counted round and nothing saying which
	// attempt was judged.
	aged, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	aged.LastJudged = ""
	if err := store.save("yoyodyne-ifd.7", aged); err != nil {
		t.Fatalf("save() error = %v", err)
	}

	resumed, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now())
	if err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if resumed.ReviewRounds != 1 {
		t.Fatalf("ReviewRounds = %d, want the one round the aged record already held", resumed.ReviewRounds)
	}
}

// A round given back is a round the environment refused, so the attempt is going
// to be judged again and has to be chargeable when it is. The judged attempt is
// cleared with the round for the same reason the head is: leaving it named would
// make the next review of that attempt free by the other door.
func TestAReturnedRoundLeavesTheAttemptChargeableAgain(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	returned, outcome, err := store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if !outcome.Returned || returned.LastJudged != "" {
		t.Fatalf("counters after the return = %#v (returned %v), want the judged attempt cleared with the round", returned, outcome.Returned)
	}
	recharged, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now())
	if err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if recharged.ReviewRounds != 1 {
		t.Fatalf("ReviewRounds = %d, want the re-judged attempt charged once", recharged.ReviewRounds)
	}
}

// The truncation rule, at the harness defaults the architect stated: an item
// that has been through its repair budget once has spent three rounds of four,
// so the configured grant of two attempts is cut to the one round that is left.
// An untruncated grant would promise a round nothing would let it take, and one
// that overshot the cap would make the cap decorative.
func TestARepairGrantIsTruncatedToTheRoundsTheCapHasRoomFor(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || !granted.Truncated || granted.Requested != 2 {
		t.Fatalf("GrantRepair() = %+v, want one round of the two asked for, recorded as truncated", granted)
	}
	if granted.Counters.TruncatedGrants != 1 || granted.Counters.GrantedRounds != 1 {
		t.Fatalf("counters after the grant = %+v, want one truncated grant of one round", granted.Counters)
	}
	// And the truncation is durable, because it is what says the item is at the
	// end of what it will be given.
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.TruncatedGrants != 1 {
		t.Fatalf("TruncatedGrants = %d, want the truncation recorded", counters.TruncatedGrants)
	}
}

// The truncation counts what is already promised, not only what has been
// produced. Two grants taken before either is carried out see no counted round
// between them, so each cut against the rounds counted would be given the whole
// of the same room, and the rounds granted would pass the cap with neither grant
// overshooting it.
func TestASecondGrantIsTruncatedAgainstWhatTheFirstAlreadyPromised(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	// Three grants permitted so the round cap is what answers here rather than the
	// grant budget, which is the bound this is not about.
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 3, Reruns: 1, MergeRearms: 1}
	first, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 3, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if first.Rounds != 3 || first.Truncated {
		t.Fatalf("the first grant = %+v, want all three rounds, untruncated", first)
	}
	// Not one round has been produced in between, so a grant truncated against the
	// rounds counted would be given three again.
	second, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 3, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if second.Rounds != 1 || !second.Truncated {
		t.Fatalf("the second grant = %+v, want the one round the first left, recorded as truncated", second)
	}
	if second.Counters.GrantedRounds != 4 {
		t.Fatalf("counters after both grants = %+v, want the granted rounds inside the cap of 4", second.Counters)
	}
	// And with the cap's room entirely promised, a third is refused outright
	// rather than granted nothing.
	_, err = store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
	if !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("GrantRepair() with the cap's room promised = %v, want a refusal", err)
	}
	var refusal TriageCapError
	rounds, refusedByRounds := TriageCapRefusal{}, false
	if errors.As(err, &refusal) {
		rounds, refusedByRounds = refusal.RefusedBy(TriageReviewRoundBudget)
	}
	if !refusedByRounds || rounds.Spent != 4 {
		t.Fatalf("refusal = %+v, want the round budget refusing it with the item's four committed rounds", refusal)
	}
	// A re-run of some other stoppage is refused by the same room, because it too
	// would produce a verdict past what the cap has left. It has to be another
	// stoppage: a re-run decided about the run the grants stand on supersedes them
	// and takes their reservation with it, which is
	// TestARerunInPlaceOfARepairReleasesWhatTheRepairReserved.
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, otherStoppageRunID), time.Now(), caps); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordRerun() with the cap's room promised = %v, want a refusal", err)
	}
}

// The other direction of the same accounting: a grant that has been carried out
// is charged once rather than twice. Its rounds become counted rounds as the
// attempts it bought are judged, so an item that spent exactly what it was given
// has as much room left as one that was never granted anything.
func TestAGrantThatHasBeenCarriedOutIsNotChargedTwice(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 8, RepairGrants: 2, Reruns: 1, MergeRearms: 1}
	if _, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	for _, attempt := range []string{"run-a#1", "run-a#2"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 2 || granted.Truncated {
		t.Fatalf("the second grant = %+v, want both rounds against the six the item has left", granted)
	}
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RoundsUncommitted(caps.ReviewRounds) != 4 {
		t.Fatalf("counters = %+v, want four of the eight rounds neither spent nor promised", counters)
	}
}

// A grant that has room asks for what it asks for and is not cut, so the
// truncation above is a bound rather than a habit.
func TestARepairGrantWithRoomIsGivenInFull(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1})
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 2 || granted.Truncated {
		t.Fatalf("GrantRepair() = %+v, want both rounds, untruncated", granted)
	}
}

// Past a cap the action is refused rather than given and counted, and the
// refusal names both the action and the budget that refused it, because an
// operator deciding what to raise needs the budget rather than the verb.
func TestTriageActionsAreRefusedPastTheirCaps(t *testing.T) {
	t.Parallel()

	// Every case is one of one: one round, one grant, one re-run, one re-arm, so
	// the refusal always reads "1 of 1 spent" whichever budget refused it.
	//
	// The two actions that buy rounds appear twice, because they have two budgets
	// and either can refuse them. That is the whole reason each has one of its
	// own: an item whose runs stopped before any reviewer verdict has spent no
	// round, so the round budget would refuse it nothing.
	rounds := TriageCaps{ReviewRounds: 1, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	ownBudget := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	spendRound := func(store *TriageStore) error {
		_, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", countingProcess, time.Now())
		return err
	}
	grant := func(caps TriageCaps) func(*TriageStore) error {
		return func(store *TriageStore) error {
			_, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
			return err
		}
	}
	rerun := func(caps TriageCaps) func(*TriageStore) error {
		return func(store *TriageStore) error {
			_, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
			return err
		}
	}
	rearm := func(caps TriageCaps) func(*TriageStore) error {
		return func(store *TriageStore) error {
			_, err := store.RecordMergeRearm(context.Background(), "yoyodyne-ifd.7", "publication:run-a#7", triageDecided(TriageDecisionRearm, decidedRunID), time.Now(), caps)
			return err
		}
	}
	for _, action := range []struct {
		name   string
		action string
		budget string
		// spend is what puts the item at its cap; take is the action then refused.
		spend func(*TriageStore) error
		take  func(*TriageStore) error
		spent func(TriageCounters) int
	}{
		{
			name:   "a repair grant past the review rounds",
			action: TriageRepairGrant,
			budget: TriageReviewRoundBudget,
			spend:  spendRound,
			take:   grant(rounds),
			spent:  func(counters TriageCounters) int { return counters.RepairGrants },
		},
		{
			name:   "a second repair grant with rounds to spare",
			action: TriageRepairGrant,
			budget: TriageRepairGrantBudget,
			spend:  grant(ownBudget),
			take:   grant(ownBudget),
			spent:  func(counters TriageCounters) int { return counters.RepairGrants },
		},
		{
			name:   "a re-run past the review rounds",
			action: TriageRerun,
			budget: TriageReviewRoundBudget,
			spend:  spendRound,
			take:   rerun(rounds),
			spent:  func(counters TriageCounters) int { return counters.Reruns },
		},
		{
			// The case the rounds cannot cover: nothing was ever reviewed, so
			// nothing has been spent against the rounds and the re-run's own budget
			// is the only thing that refuses a second one.
			name:   "a second re-run with rounds to spare",
			action: TriageRerun,
			budget: TriageRerunBudget,
			spend:  rerun(ownBudget),
			take:   rerun(ownBudget),
			spent:  func(counters TriageCounters) int { return counters.Reruns },
		},
		{
			name:   "a second merge re-arm of one publication",
			action: TriageMergeRearm,
			budget: TriageMergeRearmBudget,
			spend:  rearm(rounds),
			take:   rearm(rounds),
			spent:  func(counters TriageCounters) int { return counters.RearmsOf("publication:run-a#7") },
		},
	} {
		t.Run(action.name, func(t *testing.T) {
			t.Parallel()
			store := newTriageStore(t)
			if err := action.spend(store); err != nil {
				t.Fatalf("spending the %s budget error = %v", action.budget, err)
			}
			before, err := store.Counters("yoyodyne-ifd.7")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			err = action.take(store)
			if !errors.Is(err, ErrTriageCapReached) {
				t.Fatalf("%s past its cap error = %v, want a cap refusal", action.action, err)
			}
			var refusal TriageCapError
			if !errors.As(err, &refusal) {
				t.Fatalf("%s past its cap error = %v, want it to name the budget", action.action, err)
			}
			refused, byThisBudget := refusal.RefusedBy(action.budget)
			if refusal.Action != action.action || !byThisBudget || refused.Cap != 1 || refused.Spent != 1 {
				t.Fatalf("refusal = %+v, want %s refused by the %s budget with 1 of 1 spent", refusal, action.action, action.budget)
			}
			// The refusal states the ceiling that would permit it, so the operator's
			// override is arithmetic they can read off the sentence rather than a
			// figure they have to go and dig out of the item's record.
			if refused.Permits() != 2 || !strings.Contains(err.Error(), fmt.Sprintf("a %s cap of 2", action.budget)) {
				t.Fatalf("refusal text = %q, want it to name the %s cap of 2 that permits the action", err, action.budget)
			}
			// A refused action must not have been counted, or a cap of one would
			// silently become a cap of however many times somebody asked.
			after, err := store.Counters("yoyodyne-ifd.7")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if action.spent(after) != action.spent(before) {
				t.Fatalf("counters after a refusal = %+v, want the refused action uncounted", after)
			}
		})
	}
}

// An item with no rounds left is refused a grant outright rather than given one
// of zero rounds: a grant that permits nothing is a grant somebody would go and
// try to spend.
func TestARepairGrantIsRefusedWhenNoRoundsRemain(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 2, RepairGrants: 1, Reruns: 1}
	for _, attempt := range []string{"run-a#0", "run-a#1"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	_, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
	if !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("GrantRepair() error = %v, want a refusal for want of rounds", err)
	}
	// And so is a re-run, which buys a whole run of rounds the item cannot spend.
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordRerun() error = %v, want a refusal for want of rounds", err)
	}
}

// A round is recorded whatever the caps say, because it is something that
// happened rather than something being asked for: a record that refused to write
// down a round would make the count disagree with the world and understate what
// the item cost.
func TestAReviewRoundIsRecordedPastEveryCap(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2", "run-a#3", "run-a#4", "run-a#5"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 6 {
		t.Fatalf("ReviewRounds = %d, want every round recorded", counters.ReviewRounds)
	}
	if counters.RoundsRemaining(4) != 0 {
		t.Fatalf("RoundsRemaining(4) = %d, want nothing remaining rather than a debt", counters.RoundsRemaining(4))
	}
}

// The question a reader of one item's record asks first, answerable from that
// record and nothing else: has this item been round before?
func TestASecondTriagePassIsVisibleFromTheItemsRecordAlone(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	if _, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	after, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.Passes() != 1 || after.TriagedAgain() {
		t.Fatalf("counters after one pass = %+v, want one pass and no repeat", after)
	}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	again, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if again.Passes() != 2 || !again.TriagedAgain() {
		t.Fatalf("counters after two passes = %+v, want the second pass visible", again)
	}
}

// A re-arm repeats one merge request the reviewer's verdict already authorized,
// so its budget is that publication's rather than the item's. Keyed to the item
// it was both bounds at once and neither of them right: it granted one
// publication a second re-arm the governed design calls an escalation, and it
// refused a later publication of the same item its first.
func TestMergeRearmsAreKeptPerPublication(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	const item = "yoyodyne-ifd.7"
	first, second := "publication:run-a#92", "publication:run-b#93"
	// Each decision names the run whose publication it is about, which is the run
	// each key names.
	firstDecided := triageDecided(TriageDecisionRearm, "run-aaaa2222333344445555666677778888")
	secondDecided := triageDecided(TriageDecisionRearm, "run-bbbb2222333344445555666677778888")
	if _, err := store.RecordMergeRearm(context.Background(), item, first, firstDecided, time.Now(), caps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
	// A second re-arm of the same publication is what a second drop of it is, and
	// past the cap that is an escalation rather than a larger budget.
	var refusal TriageCapError
	err := func() error {
		_, err := store.RecordMergeRearm(context.Background(), item, first, firstDecided, time.Now(), caps)
		return err
	}()
	if !errors.As(err, &refusal) || refusal.Publication != first {
		t.Fatalf("a second re-arm of one publication error = %v, want it refused and naming that publication", err)
	}
	// A different publication of the same item has spent nothing, so its own
	// first re-arm stands.
	counters, err := store.RecordMergeRearm(context.Background(), item, second, secondDecided, time.Now(), caps)
	if err != nil {
		t.Fatalf("RecordMergeRearm() of a second publication error = %v", err)
	}
	if counters.RearmsOf(first) != 1 || counters.RearmsOf(second) != 1 {
		t.Fatalf("re-armed publications = %+v, want one each", counters.RearmedPublications)
	}
	// The item's own total is what every reading of the item reports, and it
	// counts both.
	if counters.MergeRearms != 2 {
		t.Fatalf("the item records %d re-arm(s) in total, want both", counters.MergeRearms)
	}
	// A publication nothing has been decided about is where every publication
	// starts, whatever the item's total says.
	if counters.RearmsOf("publication:run-c#94") != 0 {
		t.Fatalf("an untouched publication reads as spent: %+v", counters.RearmedPublications)
	}
	// And a re-arm has to name the publication whose budget it spends: a decision
	// recorded against the item alone is the accounting this replaced.
	if _, err := store.RecordMergeRearm(context.Background(), item, "  ", firstDecided, time.Now(), caps); err == nil {
		t.Fatal("RecordMergeRearm() naming no publication = nil error, want it refused")
	}
}

// Each item's counters are its own. A budget that leaked between items would
// refuse work that had never been triaged, which is worse than not counting at
// all.
func TestTriageCountersAreKeptPerWorkItem(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	// One re-run each is the whole of what the budget permits, so three items
	// each taking one is exactly the leak this would catch: a budget shared
	// between them would refuse the second item its first re-run.
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.8", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() on a second item error = %v", err)
	}
	// Two identifiers that a naive file name would fold together stay apart, which
	// is what the digest in the name is for.
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd-7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() on a similarly named item error = %v", err)
	}
	for _, item := range []string{"yoyodyne-ifd.7", "yoyodyne-ifd.8", "yoyodyne-ifd-7"} {
		counters, err := store.Counters(item)
		if err != nil {
			t.Fatalf("Counters(%q) error = %v", item, err)
		}
		if counters.Reruns != 1 {
			t.Fatalf("Counters(%q) = %+v, want one re-run of its own", item, counters)
		}
	}
}

// Two processes acting on one item at the same instant is the case a
// read-modify-write loses an increment to. Both must land, because a lost
// increment is a grant given and not counted, which is exactly what a cap is
// supposed to make impossible.
func TestConcurrentTriageUpdatesEachLand(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	const rounds = 12
	var waiting sync.WaitGroup
	failures := make(chan error, rounds)
	for round := 0; round < rounds; round++ {
		waiting.Add(1)
		go func(round int) {
			defer waiting.Done()
			attempt := RoundKey("run-a", round)
			if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, countingProcess, time.Now()); err != nil {
				failures <- err
			}
		}(round)
	}
	waiting.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != rounds {
		t.Fatalf("ReviewRounds = %d, want all %d increments", counters.ReviewRounds, rounds)
	}
}

// A record that cannot be read must never be spent through as though it were
// empty: an unreadable budget reads as an unlimited one, and every cap in it
// stops meaning anything.
func TestUnreadableTriageCountersAreARefusalRatherThanAnEmptyBudget(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1}); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	corrupted := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := os.WriteFile(filepath.Join(store.Root(), entry.Name()), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		corrupted = true
	}
	if !corrupted {
		t.Fatal("no recorded counter file was found to corrupt")
	}
	if _, err := store.Counters("yoyodyne-ifd.7"); err == nil {
		t.Fatal("Counters() over an unreadable record = nil error, want a refusal")
	}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), TriageCaps{ReviewRounds: 5, RepairGrants: 1, Reruns: 1}); err == nil {
		t.Fatal("RecordRerun() over an unreadable record = nil error, want a refusal")
	}
}

// The counters belong to one product, and a record from another one must not be
// read as this product's: two products' budgets for identically named items
// would otherwise be one budget.
func TestTriageCountersOfAnotherProductAreRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mine, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	if _, err := mine.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1}); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	theirs, err := NewTriageStore(root, domain.ProductID("other"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	counters, err := theirs.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Reruns != 0 {
		t.Fatalf("Counters() = %+v, want another product's record left alone", counters)
	}
}

// The counters live beside the runs of the same product, reached from the run
// store, so whoever can read what became of an item's runs can read what triage
// has spent on it without being told the state root twice.
func TestTheRunStoreReachesItsProductsTriageCounters(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := NewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	triage, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	if runs.Triage().Root() != triage.Root() {
		t.Fatalf("the run store reaches %s, the triage store is at %s", runs.Triage().Root(), triage.Root())
	}
	if filepath.Dir(runs.Triage().Root()) != filepath.Dir(runs.Root()) {
		t.Fatalf("triage counters at %s are not beside the runs at %s", runs.Triage().Root(), runs.Root())
	}
}

// The contract refuses what could not describe a record it wrote, so a record
// hand-edited into an impossible shape is caught on the way in rather than acted
// on.
func TestTriageCountersRefuseAnImpossibleRecord(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		counters TriageCounters
		want     string
	}{
		{
			name:     "a negative counter",
			counters: TriageCounters{Reruns: -1},
			want:     "reruns cannot be negative",
		},
		{
			name:     "more grants truncated than given",
			counters: TriageCounters{RepairGrants: 1, TruncatedGrants: 2},
			want:     "recorded as truncated",
		},
		{
			name:     "granted rounds with no grant",
			counters: TriageCounters{GrantedRounds: 2},
			want:     "granted rounds require the grant",
		},
		{
			name:     "committed rounds with no grant",
			counters: TriageCounters{CommittedRounds: 2},
			want:     "committed rounds require the grant",
		},
		{
			name:     "a counted round with no round count",
			counters: TriageCounters{LastRound: "run-a#0"},
			want:     "a counted round requires the round count",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			counters := test.counters
			counters.SchemaVersion = TriageCountersSchemaVersion
			counters.ProductID = domain.ProductID("yoyodyne")
			counters.WorkItemID = "yoyodyne-ifd.7"
			counters.UpdatedAt = time.Now()
			err := counters.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// A record written before committed rounds were counted is still read and still
// spent against: the field is an addition to the schema rather than a new one,
// and a record without it reads as an item with nothing outstanding, which is
// how the grant that wrote it was treated at the time.
func TestCountersWrittenBeforeCommittedRoundsAreStillSpendable(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	written := `{"schema_version":1,"product_id":"yoyodyne","work_item_id":"yoyodyne-ifd.7",` +
		`"repair_grants":1,"granted_rounds":2,"review_rounds":2,"last_round":"run-a#1",` +
		`"updated_at":"2026-08-16T08:00:00Z"}`
	if err := os.WriteFile(store.path("yoyodyne-ifd.7"), []byte(written), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	counters, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RoundsUncommitted(4) != 2 || counters.RoundsRemaining(4) != 2 {
		t.Fatalf("counters = %+v, want the two rounds the cap has left, promised to nothing", counters)
	}
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), TriageCaps{ReviewRounds: 4, RepairGrants: 2, Reruns: 1, MergeRearms: 1})
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 2 || granted.Truncated {
		t.Fatalf("GrantRepair() = %+v, want the two rounds the record left", granted)
	}
}

// A cap set nothing could be spent against is refused where it is supplied
// rather than silently treated as zero, because a negative cap is a mistake and
// zero is a decision.
func TestNegativeTriageCapsAreRefused(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), TriageCaps{ReviewRounds: -1}); err == nil {
		t.Fatal("RecordRerun() with a negative cap = nil error, want a refusal")
	}
	// Zero is a choice -- never do this, hand it to a person -- and refuses the
	// action rather than the caps. `triage.review_rounds_cap` accepts zero for
	// exactly this: an item that reaches triage is escalated or re-scoped rather
	// than repaired again.
	if _, err := store.RecordMergeRearm(context.Background(), "yoyodyne-ifd.7", "publication:run-a#7", triageDecided(TriageDecisionRearm, decidedRunID), time.Now(), TriageCaps{}); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordMergeRearm() under a zero cap = %v, want a cap refusal", err)
	}
	if _, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), TriageCaps{ReviewRounds: 0}); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("GrantRepair() under a zero round cap = %v, want a cap refusal", err)
	}
}

// An uncharged verdict spends nothing and releases the round a grant reserved for
// it, which is the whole of what the operator directed on 2026-09-05 and what
// yoyodyne-ifd.391 completed: a round that approved the work, or left one trivial
// note beside it, must not walk the item toward a cap on its own success — and a
// commitment that went on holding that round was exactly that walk by another
// figure, since the round budget is refused against what the item is committed
// to.
//
// Every field is compared rather than the rounds alone. What may move is the
// judged attempt, which is not a budget, and the commitment, which moves in the
// one direction that is not a spend; the failure this guards against is a later
// change spending something else in passing — a grant, the head of the record,
// the rounds themselves — on a verdict that was supposed to cost nothing.
func TestAnUnchargedVerdictSpendsNothingAndReleasesTheRoundItWasReserved(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.279"
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	// An item mid-flight rather than a fresh one: a grant of two recorded, one of
	// its rounds spent, so every counter the record keeps carries something an
	// uncharged verdict could disturb — and one reserved round is still standing
	// for the attempt about to be judged.
	if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if _, err := store.RecordReviewRound(ctx, item, "run-a#1", countingProcess, time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	before, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if before.CommittedRounds != 2 || before.ReviewRounds != 1 {
		t.Fatalf("counters before the verdict = %d committed, %d spent; want the grant's second round still reserved", before.CommittedRounds, before.ReviewRounds)
	}

	after, err := store.RecordUnchargedVerdict(ctx, item, "run-a#2", time.Now())
	if err != nil {
		t.Fatalf("RecordUnchargedVerdict() error = %v", err)
	}
	// The judged attempt moves, and it is not a budget: it is what keeps the
	// replay of this attempt free.
	if after.LastJudged != "run-a#2" {
		t.Fatalf("LastJudged = %q, want the attempt the reviewer answered about", after.LastJudged)
	}
	// The reservation for this round is released and nothing was spent: the round
	// ran, the reviewer answered, and the answer was not the reviewer arguing.
	if after.CommittedRounds != 1 || after.ReviewRounds != 1 {
		t.Fatalf("counters after the verdict = %d committed, %d spent; want the reserved round released and the rounds untouched", after.CommittedRounds, after.ReviewRounds)
	}
	stripped := after
	stripped.LastJudged = before.LastJudged
	stripped.CommittedRounds = before.CommittedRounds
	stripped.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(stripped, before) {
		t.Fatalf("counters after an uncharged verdict = %+v, want everything but the judged attempt and the reservation left at %+v", after, before)
	}
	// And the room the guards read has the released round back in it, which is
	// the figure the escalations were about.
	if after.RoundsRemaining(caps.ReviewRounds) != 3 || after.RoundsUncommitted(caps.ReviewRounds) != 3 {
		t.Fatalf("room after an uncharged verdict = %d remaining / %d uncommitted, want 3 / 3",
			after.RoundsRemaining(caps.ReviewRounds), after.RoundsUncommitted(caps.ReviewRounds))
	}
	// A second answer about the same attempt — the integration replay — releases
	// nothing further: the attempt was judged once, and its round was resolved by
	// that judgement.
	again, err := store.RecordUnchargedVerdict(ctx, item, "run-a#2", time.Now())
	if err != nil {
		t.Fatalf("RecordUnchargedVerdict() again error = %v", err)
	}
	if again.CommittedRounds != 1 {
		t.Fatalf("committed rounds after a re-review of the same attempt = %d, want the reservation left where the first verdict put it", again.CommittedRounds)
	}
	// An item with nothing reserved has nothing to release: an uncharged verdict
	// on it moves no budget at all, which is what every item nobody has granted
	// anything looks like.
	if _, err := store.RecordUnchargedVerdict(ctx, "yoyodyne-ifd.279.ungranted", "run-b#0", time.Now()); err != nil {
		t.Fatalf("RecordUnchargedVerdict() on an ungranted item error = %v", err)
	}
	ungranted, err := store.Counters("yoyodyne-ifd.279.ungranted")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if ungranted.CommittedRounds != 0 || ungranted.ReviewRounds != 0 {
		t.Fatalf("an ungranted item after an uncharged verdict = %d committed, %d spent; want zero of each", ungranted.CommittedRounds, ungranted.ReviewRounds)
	}
}

// yoyodyne-ifd.349, 2026-09-15, replayed: three rounds spent, the one repair
// grant recorded and cut to the round the cap had left, that round ending in an
// approve-as-implementation verdict, and the promotion then stopping on a replay
// conflict. The development manager's re-run was refused at 4 of 4 — the
// approving round was counted, through the commitment the grant had made — and
// it took an operator override. Under the completed rule the approval releases
// the round it was reserved and the re-run is recorded without one.
func TestTheApprovedGrantedRoundOfIfd349ReachesItsRerunWithoutAnOverride(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.349"
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	spendTriageRounds(t, store, item, 3)
	granted, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || !granted.Truncated || granted.Counters.CommittedRounds != 4 {
		t.Fatalf("grant = %+v, want the one round the cap had left, reserved to 4 of 4", granted)
	}
	// The granted round ran and the reviewer approved the change.
	approved, err := store.RecordUnchargedVerdict(ctx, item, "run-a#3", time.Now())
	if err != nil {
		t.Fatalf("RecordUnchargedVerdict() error = %v", err)
	}
	if approved.CommittedRounds != 3 || approved.ReviewRounds != 3 {
		t.Fatalf("counters after the approval = %d committed, %d spent; want the approving round released and 3 of 4 spent", approved.CommittedRounds, approved.ReviewRounds)
	}
	// The replay conflicted and the run stopped; the development manager decides
	// to run the item again on the moved base. Nothing is overridden.
	rerun, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
	if err != nil {
		t.Fatalf("RecordRerun() after the approving round = %v, want it permitted without an override", err)
	}
	if rerun.Reruns != 1 || len(rerun.Overrides) != 0 {
		t.Fatalf("counters after the re-run = %+v, want one re-run recorded and no override", rerun)
	}
}

// yoyodyne-ifd.309, 2026-09-18, replayed: a repair recorded against a cap with
// one round left reserved that round, the harness then found the stopped run's
// worktree retired and refused to carry the repair out, and the re-run recorded
// in its place was refused at 6 of 6 — a round that never ran was counted. Under
// the completed rule the re-run supersedes the repair, releases what it
// reserved, and is recorded without an override.
func TestTheNeverRunGrantedRoundOfIfd309ReachesItsRerunWithoutAnOverride(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.309"
	caps := TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	spendTriageRounds(t, store, item, 5)
	granted, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || granted.Counters.CommittedRounds != 6 {
		t.Fatalf("grant = %+v, want the cap's last round reserved", granted)
	}
	if decision, found := granted.Counters.DecisionOf(decidedRunID); !found || decision.Rounds != 1 {
		t.Fatalf("the repair decision = %+v (found %t), want it to record the one round it reserved", decision, found)
	}
	// Nothing ran: the carry-out refused. The re-run recorded in the repair's
	// place is measured against the rounds the item spent, not the one the repair
	// reserved and nothing will produce.
	rerun, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
	if err != nil {
		t.Fatalf("RecordRerun() in place of the repair = %v, want it permitted without an override", err)
	}
	if rerun.CommittedRounds != 5 || rerun.ReviewRounds != 5 || rerun.Reruns != 1 {
		t.Fatalf("counters after the re-run = %+v, want the reservation released and 5 of 6 spent", rerun)
	}
	if decision, found := rerun.DecisionOf(decidedRunID); !found || decision.Decision != TriageDecisionRerun {
		t.Fatalf("the standing decision = %+v (found %t), want the re-run in the repair's place", decision, found)
	}
	// The grant itself stays spent: what was released is the reservation, not the
	// decision that was recorded and refused.
	if rerun.RepairGrants != 1 || rerun.GrantedRounds != 1 {
		t.Fatalf("grants after the re-run = %d of %d round(s), want the grant still recorded", rerun.RepairGrants, rerun.GrantedRounds)
	}
}

// The release reaches exactly what the superseded repair reserved. A re-run
// decided about some other stoppage of the item releases nothing, because the
// repair it does not supersede may still be carried out; and a decision that
// spends nothing — an escalation — releases the reservation as a re-run does,
// since it is just as much the decision that the repair's attempts will not be
// made.
func TestARerunInPlaceOfARepairReleasesWhatTheRepairReserved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 2, Reruns: 2, MergeRearms: 2}
	const item = "yoyodyne-ifd.391"

	t.Run("a re-run of another stoppage leaves the reservation standing", func(t *testing.T) {
		t.Parallel()
		store := newTriageStore(t)
		spendTriageRounds(t, store, item, 2)
		if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps); err != nil {
			t.Fatalf("GrantRepair() error = %v", err)
		}
		counters, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, otherStoppageRunID), time.Now(), caps)
		if err != nil {
			t.Fatalf("RecordRerun() error = %v", err)
		}
		if counters.CommittedRounds != 3 {
			t.Fatalf("committed rounds = %d, want the other stoppage's reservation left standing at 3", counters.CommittedRounds)
		}
	})

	t.Run("an escalation in place of the repair releases it", func(t *testing.T) {
		t.Parallel()
		store := newTriageStore(t)
		spendTriageRounds(t, store, item, 2)
		if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps); err != nil {
			t.Fatalf("GrantRepair() error = %v", err)
		}
		counters, err := store.RecordDecision(ctx, item, triageDecided(TriageDecisionEscalate, decidedRunID), time.Now())
		if err != nil {
			t.Fatalf("RecordDecision() error = %v", err)
		}
		if counters.CommittedRounds != 2 {
			t.Fatalf("committed rounds = %d, want the superseded repair's reservation released to 2", counters.CommittedRounds)
		}
	})

	t.Run("a repair in place of a repair keeps both reservations and records their sum", func(t *testing.T) {
		t.Parallel()
		store := newTriageStore(t)
		for range 2 {
			if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps); err != nil {
				t.Fatalf("GrantRepair() error = %v", err)
			}
		}
		counters, err := store.Counters(item)
		if err != nil {
			t.Fatalf("Counters() error = %v", err)
		}
		if counters.CommittedRounds != 2 {
			t.Fatalf("committed rounds = %d, want both grants reserved", counters.CommittedRounds)
		}
		if decision, _ := counters.DecisionOf(decidedRunID); decision.Rounds != 2 {
			t.Fatalf("the standing repair records %d reserved round(s), want both grants' 2", decision.Rounds)
		}
		released, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
		if err != nil {
			t.Fatalf("RecordRerun() error = %v", err)
		}
		if released.CommittedRounds != 0 {
			t.Fatalf("committed rounds after the re-run = %d, want both reservations released", released.CommittedRounds)
		}
	})

	t.Run("a release never reaches below what the item has spent", func(t *testing.T) {
		t.Parallel()
		store := newTriageStore(t)
		if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps); err != nil {
			t.Fatalf("GrantRepair() error = %v", err)
		}
		// Both granted rounds were spent, so the commitment has been spent through
		// and there is nothing left to release.
		spendTriageRounds(t, store, item, 2)
		counters, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
		if err != nil {
			t.Fatalf("RecordRerun() error = %v", err)
		}
		if counters.CommittedRounds != 2 || counters.ReviewRounds != 2 {
			t.Fatalf("counters after the re-run = %d committed, %d spent; want the spent rounds untouched", counters.CommittedRounds, counters.ReviewRounds)
		}
	})
}

// The escalation shapes this change was directed at, replayed against the new
// semantics. Each is an item one verdict from its cap, and under the old
// accounting each of these verdicts took the last round the item had — which is
// how four items in a week reached their caps on the round that said their work
// was right, and needed a person to unstick them.
//
// The shapes are the documented ones rather than the items' verdict-by-verdict
// histories, which the durable record cannot supply: the counters hold totals,
// not the sequence of what each verdict was. The evidence that the shapes
// happened is those four items' own counter files, and it is cited here rather
// than reconstructed.
//
// Each case asserts both directions on one store: the decision the development
// manager was refused is permitted now, and charging the same verdict as a round
// still refuses it. The second half is what says the semantics moved this rather
// than the caps being loose.
func TestTheDocumentedCapEscalationsNoLongerEscalate(t *testing.T) {
	t.Parallel()

	// The harness defaults every one of these was met at.
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	for _, escalation := range []struct {
		name string
		// evidence names the items whose records document this shape.
		evidence string
		// verdict is the uncharged verdict the item's last round produced.
		verdict func(*testing.T, *TriageStore, string)
		// decision is what the development manager asked for afterwards and was
		// refused; it must be permitted now.
		decision func(*TriageStore, string) error
	}{
		{
			name:     "the last permitted round approved the change, and a re-run was refused",
			evidence: "yoyodyne-ifd.143, yoyodyne-ifd.68.22",
			verdict: func(t *testing.T, store *TriageStore, item string) {
				t.Helper()
				if _, err := store.RecordUnchargedVerdict(context.Background(), item, "run-a#3", time.Now()); err != nil {
					t.Fatalf("RecordUnchargedVerdict() error = %v", err)
				}
			},
			decision: func(store *TriageStore, item string) error {
				_, err := store.RecordRerun(context.Background(), item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
				return err
			},
		},
		{
			name:     "the last permitted round approved the change, and a repair grant was refused",
			evidence: "yoyodyne-ifd.209.16, yoyodyne-ifd.241",
			verdict: func(t *testing.T, store *TriageStore, item string) {
				t.Helper()
				if _, err := store.RecordUnchargedVerdict(context.Background(), item, "run-a#3", time.Now()); err != nil {
					t.Fatalf("RecordUnchargedVerdict() error = %v", err)
				}
			},
			decision: func(store *TriageStore, item string) error {
				_, err := store.GrantRepair(context.Background(), item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
				return err
			},
		},
		{
			// The operator's extension of the same rule: the reviewer said the work
			// is right and named one small thing beside it. The record cannot tell
			// this verdict from the approval above, which is the point — both cost
			// the item nothing, and whoever obtained the verdict is what decides
			// which it was.
			name:     "the last permitted round left one trivial finding, and a repair grant was refused",
			evidence: "yoyodyne-ifd.241, whose remaining round was to take the review's minors",
			verdict: func(t *testing.T, store *TriageStore, item string) {
				t.Helper()
				if _, err := store.RecordUnchargedVerdict(context.Background(), item, "run-a#3", time.Now()); err != nil {
					t.Fatalf("RecordUnchargedVerdict() error = %v", err)
				}
			},
			decision: func(store *TriageStore, item string) error {
				_, err := store.GrantRepair(context.Background(), item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
				return err
			},
		},
	} {
		t.Run(escalation.name, func(t *testing.T) {
			t.Parallel()
			const item = "yoyodyne-ifd.replayed"

			// The item as it stood one verdict from its cap.
			permitted := newTriageStore(t)
			spendTriageRounds(t, permitted, item, 3)
			escalation.verdict(t, permitted, item)
			if err := escalation.decision(permitted, item); err != nil {
				t.Fatalf("the decision the escalation was about (%s) error = %v, want it permitted now", escalation.evidence, err)
			}

			// And the same item whose last verdict was charged as a round, which is
			// the accounting that escalated.
			refused := newTriageStore(t)
			spendTriageRounds(t, refused, item, 3)
			if _, err := refused.RecordReviewRound(context.Background(), item, "run-a#3", countingProcess, time.Now()); err != nil {
				t.Fatalf("RecordReviewRound() error = %v", err)
			}
			if err := escalation.decision(refused, item); !errors.Is(err, ErrTriageCapReached) {
				t.Fatalf("the same decision with the last verdict charged = %v, want the refusal that escalated", err)
			}
		})
	}
}

// Two spent budgets guarding one decision refuse together. Serially is what
// actually happened on 2026-09-05: yoyodyne-ifd.272 and yoyodyne-ifd.209.20 each
// took two operator override ceremonies two minutes apart, because the first
// refusal named the grant budget, the override crossed it, and the round budget
// then refused the same decision in turn.
func TestTwoSpentBudgetsRefuseOneDecisionTogether(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.272"
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	// One grant given and its rounds spent, which puts the item at the end of both
	// budgets at once: the grant budget by the grant, the round budget by what it
	// committed.
	if _, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 2, time.Now(), caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	spendTriageRounds(t, store, item, 4)

	_, err := store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
	var refusal TriageCapError
	if !errors.As(err, &refusal) {
		t.Fatalf("second GrantRepair() error = %v, want a cap refusal", err)
	}
	grants, refusedByGrants := refusal.RefusedBy(TriageRepairGrantBudget)
	rounds, refusedByRounds := refusal.RefusedBy(TriageReviewRoundBudget)
	if !refusedByGrants || !refusedByRounds {
		t.Fatalf("refused by %v, want both budgets named in one refusal", refusal.Budgets())
	}
	// Each names what it stands at and what would permit the decision, so the
	// operator's override is arithmetic off the sentence rather than a dig through
	// the item's record.
	if grants.Spent != 1 || grants.Cap != 1 || grants.Permits() != 2 {
		t.Fatalf("the grant budget refused with %+v, want 1 of 1 spent and a cap of 2 permitting it", grants)
	}
	if rounds.Spent != 4 || rounds.Cap != 4 || rounds.Permits() != 5 {
		t.Fatalf("the round budget refused with %+v, want 4 of 4 spent and a cap of 5 permitting it", rounds)
	}
	for _, want := range []string{
		"1 of 1 permitted repair grant(s) are spent",
		"4 of 4 permitted review round(s) are spent",
		"a repair grant cap of 2 and a review round cap of 5",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %q, want it to say %q", err, want)
		}
	}

	// Crossing one of them leaves the other refusing, which is what made the
	// second ceremony necessary — and is now said before the first one happens.
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageRepairGrantBudget,
		Cap:       grants.Permits(),
		DecidedBy: "mason",
		Reason:    "one more findings-scoped grant",
	}, time.Now(), caps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	_, err = store.GrantRepair(ctx, item, triageDecided(TriageDecisionRepair, decidedRunID), 1, time.Now(), caps)
	var crossed TriageCapError
	if !errors.As(err, &crossed) || len(crossed.Refusals) != 1 {
		t.Fatalf("GrantRepair() past the first override = %v, want the round budget alone still refusing it", err)
	}
	if crossed.Refusals[0].Budget != TriageReviewRoundBudget {
		t.Fatalf("GrantRepair() past the first override was refused by %q, want the review round budget", crossed.Refusals[0].Budget)
	}
}

// spendTriageRounds charges one item the given number of review rounds, each
// under its own developer attempt so none of them deduplicates against the last.
func spendTriageRounds(t *testing.T, store *TriageStore, workItemID string, rounds int) {
	t.Helper()
	for round := 0; round < rounds; round++ {
		if _, err := store.RecordReviewRound(context.Background(), workItemID,
			fmt.Sprintf("run-a#%d", round), countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
}
