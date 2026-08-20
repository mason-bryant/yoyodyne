package runstate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const rerunDocketKey = "stopped_run:run-0123456789abcdef0123456789abcdef"

func newRerunStore(t *testing.T) *RerunStore {
	t.Helper()
	store, err := NewRerunStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	return store
}

func claimedRerun() Rerun {
	return Rerun{
		DocketKey:  rerunDocketKey,
		PriorRunID: "run-0123456789abcdef0123456789abcdef",
		WorkItemID: "yoyodyne-ifd.102.6",
		Reason:     "the development manager triaged this stoppage as a re-run: the ground moved under a correct change",
		ClaimedAt:  time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Preserved: PreservedArtifacts{
			Branch:       "yoyodyne/task/abc",
			WorktreePath: "/state/worktrees/task",
			Disposition:  PreservedKept,
		},
	}
}

// The bound this record exists for. One docketed stoppage is run again once,
// whoever asks and however much budget the item has left.
func TestOneStoppageIsClaimedOnce(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	if _, err := store.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	_, err := store.Claim(context.Background(), claimedRerun())
	if !errors.Is(err, ErrRerunTaken) {
		t.Fatalf("second Claim() error = %v, want the re-run refused", err)
	}
	var taken RerunTakenError
	if !errors.As(err, &taken) || taken.Existing.WorkItemID != "yoyodyne-ifd.102.6" {
		t.Fatalf("refusal = %#v, want it to carry the re-run that already exists", err)
	}
}

// Two processes claiming one stoppage at the same instant are one claim and one
// refusal, because the read and the write are under the stoppage's own lock.
func TestConcurrentClaimsOfOneStoppageLeaveOneRerun(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	var group sync.WaitGroup
	claimed := make([]error, 4)
	for index := range claimed {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, claimed[index] = store.Claim(context.Background(), claimedRerun())
		}(index)
	}
	group.Wait()
	granted := 0
	for _, err := range claimed {
		switch {
		case err == nil:
			granted++
		case errors.Is(err, ErrRerunTaken):
		default:
			t.Fatalf("Claim() error = %v, want either the claim or the refusal", err)
		}
	}
	if granted != 1 {
		t.Fatalf("claims granted = %d, want exactly one", granted)
	}
}

// What the stopped run preserved outlives the process that claimed the re-run,
// which is the whole reason it is written down rather than reported.
func TestARerunsDispositionSurvivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	if _, err := first.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	retired := time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)
	if _, err := first.Settle(context.Background(), rerunDocketKey, "run-fedcba9876543210fedcba9876543210", PreservedArtifacts{
		Branch:       "yoyodyne/task/abc",
		WorktreePath: "/state/worktrees/task",
		Disposition:  PreservedRetired,
		RetiredAt:    &retired,
	}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}

	later, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	recorded, found, err := later.Find(rerunDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = %t, error = %v", found, err)
	}
	if recorded.RunID != "run-fedcba9876543210fedcba9876543210" {
		t.Fatalf("run = %q, want the fresh run the re-run started", recorded.RunID)
	}
	if recorded.Preserved.Disposition != PreservedRetired || recorded.Preserved.RetiredAt == nil {
		t.Fatalf("preserved = %#v, want a dated retirement", recorded.Preserved)
	}
	if !strings.Contains(recorded.Render(), "yoyodyne/task/abc") {
		t.Fatalf("Render() = %q, want the artifacts named", recorded.Render())
	}
}

// What has been claimed for one item is what says a decision about it has been
// acted on, so it is read back by item and counts every claim whatever became of
// the run it caused.
func TestTheClaimsOfOneItemAreReadBackFromEveryStoppage(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	second := claimedRerun()
	second.DocketKey = "stopped_run:run-fedcba9876543210fedcba9876543210"
	second.PriorRunID = "run-fedcba9876543210fedcba9876543210"
	second.ClaimedAt = second.ClaimedAt.Add(time.Hour)
	other := claimedRerun()
	other.DocketKey = "stopped_run:run-11112222333344445555666677778888"
	other.PriorRunID = "run-11112222333344445555666677778888"
	other.WorkItemID = "yoyodyne-ifd.119"
	for _, rerun := range []Rerun{claimedRerun(), second, other} {
		if _, err := store.Claim(context.Background(), rerun); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
	}

	claimed, err := store.Claimed("yoyodyne-ifd.102.6")
	if err != nil {
		t.Fatalf("Claimed() error = %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("Claimed() = %d re-run(s), want both stoppages of that item", len(claimed))
	}
	// Oldest first, so a reader following the record reads the item's history in
	// the order it happened.
	if claimed[0].DocketKey != rerunDocketKey || claimed[1].DocketKey != second.DocketKey {
		t.Fatalf("Claimed() order = %q, %q", claimed[0].DocketKey, claimed[1].DocketKey)
	}
	// Another item's claims are its own: nothing here bounds one item by what
	// triage did about another.
	elsewhere, err := store.Claimed("yoyodyne-ifd.119")
	if err != nil || len(elsewhere) != 1 {
		t.Fatalf("Claimed() = %d re-run(s), error = %v, want the other item's own", len(elsewhere), err)
	}
	all, err := store.List()
	if err != nil || len(all) != 3 {
		t.Fatalf("List() = %d re-run(s), error = %v, want every record", len(all), err)
	}
	// The lock file each record is guarded by sits in the same directory and is
	// not a record; a listing that read it would fail on every product that has
	// ever claimed one.
	if _, err := store.Claimed("yoyodyne-ifd.102.6"); err != nil {
		t.Fatalf("Claimed() error = %v after the locks were written", err)
	}
}

// An item nothing has been re-run about has no claims, which is the ordinary
// answer rather than a failure to look.
func TestAnItemWithNoRerunsHasNoClaims(t *testing.T) {
	t.Parallel()

	claimed, err := newRerunStore(t).Claimed("yoyodyne-ifd.102.6")
	if err != nil || len(claimed) != 0 {
		t.Fatalf("Claimed() = %#v, error = %v, want nothing", claimed, err)
	}
}

// A stoppage nothing has been recorded about is an absence rather than a
// failure to look: that is every stoppage before somebody decides about it.
func TestAnUnclaimedStoppageIsAnAbsence(t *testing.T) {
	t.Parallel()

	recorded, found, err := newRerunStore(t).Find(rerunDocketKey)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found {
		t.Fatalf("Find() found %#v on a store nothing has claimed in", recorded)
	}
}

// Settling a stoppage nothing claimed is refused rather than creating a record:
// a disposition with no claim behind it would describe a re-run nobody took.
func TestSettlingAnUnclaimedStoppageIsRefused(t *testing.T) {
	t.Parallel()

	_, err := newRerunStore(t).Settle(context.Background(), rerunDocketKey, "run-fedcba9876543210fedcba9876543210",
		PreservedArtifacts{Disposition: PreservedGone})
	if err == nil || !strings.Contains(err.Error(), "nothing to settle") {
		t.Fatalf("Settle() error = %v, want a refusal", err)
	}
}

// The contract each record is held to. Every one of these describes a re-run
// somebody could misread as accounted for.
func TestARerunIsValidatedAgainstWhatItHasToSay(t *testing.T) {
	t.Parallel()

	for _, invalid := range []struct {
		name   string
		change func(*Rerun)
		want   string
	}{
		{"with no reasoning", func(r *Rerun) { r.Reason = "" }, "triage decision"},
		{"with no stoppage", func(r *Rerun) { r.DocketKey = "" }, "docket key"},
		{"with no prior run", func(r *Rerun) { r.PriorRunID = "" }, "prior run"},
		{"with an unknown disposition", func(r *Rerun) { r.Preserved.Disposition = "abandoned" }, "disposition"},
		{"with an undated retirement", func(r *Rerun) { r.Preserved.Disposition = PreservedRetired }, "when it was retired"},
		{
			"with a retirement it did not make",
			func(r *Rerun) {
				retired := time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)
				r.Preserved.RetiredAt = &retired
			},
			"was not retired",
		},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Parallel()

			rerun := claimedRerun()
			rerun.SchemaVersion = RerunSchemaVersion
			rerun.ProductID = "yoyodyne"
			rerun.UpdatedAt = rerun.ClaimedAt
			invalid.change(&rerun)
			err := rerun.Validate()
			if err == nil || !strings.Contains(err.Error(), invalid.want) {
				t.Fatalf("Validate() error = %v, want one naming %q", err, invalid.want)
			}
		})
	}
}

// The store belongs to one product, and the run store reaches its own without
// being told the state root a second time.
func TestTheRerunStoreIsTheRunStoresOwnProduct(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := NewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	beside, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	if runs.Reruns().Root() != beside.Root() {
		t.Fatalf("Reruns().Root() = %q, want %q", runs.Reruns().Root(), beside.Root())
	}
	if _, err := runs.Reruns().Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, found, err := beside.Find(rerunDocketKey); err != nil || !found {
		t.Fatalf("Find() = %t, error = %v, want the claim the run store's own record took", found, err)
	}
}
