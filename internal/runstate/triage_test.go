package runstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

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
	caps := TriageCaps{RepairGrants: 1, Reruns: 1, MergeRearms: 2, ReviewRounds: 4}
	if _, err := first.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", "run-a#0", time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if _, err := first.RecordRerun(context.Background(), "yoyodyne-ifd.7", caps); err != nil {
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
// twice -- a review resumed after an interrupted process, or re-obtained on a
// change replayed onto a moved target -- is one round, because charging the item
// for the second would charge it for a race it did not cause and for a process
// that died under it.
func TestAReviewRoundIsCountedOncePerDeveloperAttempt(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	for _, attempt := range []string{"run-a#0", "run-a#0", "run-a#1", "run-a#1", "run-b#0"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, time.Now()); err != nil {
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

// The truncation rule, at the harness defaults the architect stated: an item
// that has been through its repair budget once has spent three rounds of four,
// so a grant of two is cut to the one round that is left. An untruncated grant
// would promise a round nothing would let it take, and one that overshot the cap
// would make the cap decorative.
func TestARepairGrantIsTruncatedToTheRoundsTheCapHasRoomFor(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{RepairGrants: 1, Reruns: 1, MergeRearms: 2, ReviewRounds: 4}
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", 2, caps)
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

// A grant that has room asks for what it asks for and is not cut, so the
// truncation above is a bound rather than a habit.
func TestARepairGrantWithRoomIsGivenInFull(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{RepairGrants: 2, ReviewRounds: 4}
	granted, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", 2, caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 2 || granted.Truncated {
		t.Fatalf("GrantRepair() = %+v, want both rounds, untruncated", granted)
	}
}

// Past a cap the action is refused rather than given and counted, and the
// refusal says which budget and what was already spent, because that is what an
// operator deciding whether to raise a cap needs.
func TestTriageActionsAreRefusedPastTheirCaps(t *testing.T) {
	t.Parallel()

	caps := TriageCaps{RepairGrants: 1, Reruns: 1, MergeRearms: 1, ReviewRounds: 4}
	for _, action := range []struct {
		name  string
		take  func(*TriageStore) error
		spent func(TriageCounters) int
	}{
		{
			name: TriageRepairGrant,
			take: func(store *TriageStore) error {
				_, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", 1, caps)
				return err
			},
			spent: func(counters TriageCounters) int { return counters.RepairGrants },
		},
		{
			name: TriageRerun,
			take: func(store *TriageStore) error {
				_, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", caps)
				return err
			},
			spent: func(counters TriageCounters) int { return counters.Reruns },
		},
		{
			name: TriageMergeRearm,
			take: func(store *TriageStore) error {
				_, err := store.RecordMergeRearm(context.Background(), "yoyodyne-ifd.7", caps)
				return err
			},
			spent: func(counters TriageCounters) int { return counters.MergeRearms },
		},
	} {
		t.Run(action.name, func(t *testing.T) {
			t.Parallel()
			store := newTriageStore(t)
			if err := action.take(store); err != nil {
				t.Fatalf("the first %s error = %v", action.name, err)
			}
			err := action.take(store)
			if !errors.Is(err, ErrTriageCapReached) {
				t.Fatalf("the second %s error = %v, want a cap refusal", action.name, err)
			}
			var refusal TriageCapError
			if !errors.As(err, &refusal) {
				t.Fatalf("the second %s error = %v, want it to name the budget", action.name, err)
			}
			if refusal.Action != action.name || refusal.Cap != 1 || refusal.Spent != 1 {
				t.Fatalf("refusal = %+v, want the %s budget with 1 of 1 spent", refusal, action.name)
			}
			// A refused action must not have been counted, or a cap of one would
			// silently become a cap of however many times somebody asked.
			counters, err := store.Counters("yoyodyne-ifd.7")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if action.spent(counters) != 1 {
				t.Fatalf("counters after a refusal = %+v, want the refused action uncounted", counters)
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
	caps := TriageCaps{RepairGrants: 3, ReviewRounds: 2}
	for _, attempt := range []string{"run-a#0", "run-a#1"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}
	_, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", 1, caps)
	if !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("GrantRepair() error = %v, want a refusal for want of rounds", err)
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
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, time.Now()); err != nil {
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
	caps := TriageCaps{RepairGrants: 1, Reruns: 1, MergeRearms: 1, ReviewRounds: 4}
	if _, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.7", 1, caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	after, err := store.Counters("yoyodyne-ifd.7")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.Passes() != 1 || after.TriagedAgain() {
		t.Fatalf("counters after one pass = %+v, want one pass and no repeat", after)
	}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", caps); err != nil {
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

// Each item's counters are its own. A budget that leaked between items would
// refuse work that had never been triaged, which is worse than not counting at
// all.
func TestTriageCountersAreKeptPerWorkItem(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{Reruns: 1}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.8", caps); err != nil {
		t.Fatalf("RecordRerun() on a second item error = %v", err)
	}
	// Two identifiers that a naive file name would fold together stay apart, which
	// is what the digest in the name is for.
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd-7", caps); err != nil {
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
			if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.7", attempt, time.Now()); err != nil {
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
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", TriageCaps{Reruns: 1}); err != nil {
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
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", TriageCaps{Reruns: 5}); err == nil {
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
	if _, err := mine.RecordRerun(context.Background(), "yoyodyne-ifd.7", TriageCaps{Reruns: 1}); err != nil {
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

// A cap set nothing could be spent against is refused where it is supplied
// rather than silently treated as zero, because a negative cap is a mistake and
// zero is a decision.
func TestNegativeTriageCapsAreRefused(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.7", TriageCaps{Reruns: -1}); err == nil {
		t.Fatal("RecordRerun() with a negative cap = nil error, want a refusal")
	}
	// Zero is a choice -- never do this, hand it to a person -- and refuses the
	// action rather than the caps.
	if _, err := store.RecordMergeRearm(context.Background(), "yoyodyne-ifd.7", TriageCaps{}); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordMergeRearm() under a zero cap = %v, want a cap refusal", err)
	}
}
