package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The brake's record rides the hold it placed: written with it, revised under
// the store's lock, and read back by every process that reads the hold. The
// operator's hold carries none, and a decision aimed at it is refused rather
// than recorded onto a switch the development manager does not hold.
func TestTheBrakesRecordRidesItsOwnHoldAndNoOther(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	trippedAt := time.Date(2026, 9, 19, 17, 56, 0, 0, time.UTC)
	trip := IntakeBrake{
		Blocked: []BrakeBlockedRun{
			{RunID: "run-1", WorkItemID: "yoyodyne-ifd.398", Reason: "independent review still required repair"},
			{RunID: "run-2", WorkItemID: "yoyodyne-ifd.353", Reason: "a configured check still failed"},
			{RunID: "run-3", WorkItemID: "yoyodyne-ifd.404", Reason: "independent review still required repair"},
		},
		CooldownEndsAt: trippedAt.Add(30 * time.Minute),
	}
	held, err := store.Brake(trip, "3 run(s) blocked in a row with nothing landing between them", trippedAt)
	if err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	if held.HeldBy != IntakeHolderBrake || !held.Braked() || len(held.Brake.Blocked) != 3 {
		t.Fatalf("Brake() = %#v, want the brake's hold carrying its trip", held)
	}
	if held.WaitsOnAPerson() {
		t.Fatal("a fresh brake hold waits on a person, want it the development manager's or the harness's")
	}
	if whose := held.Whose(); !strings.HasPrefix(whose, "the development manager's") {
		t.Fatalf("Whose() = %q, want the development manager named while she decides", whose)
	}

	// Another process reads the same record.
	loaded, found, err := newIntakeStoreAt(t, root, "yoyodyne").Held()
	if err != nil || !found || loaded.Brake == nil || len(loaded.Brake.Blocked) != 3 {
		t.Fatalf("Held() = %#v, %t, %v, want the trip read back whole", loaded, found, err)
	}

	// The summons, the decision, and the probe are revisions of the same record.
	summonedAt := trippedAt.Add(time.Minute)
	if _, err := store.ReviseBrake(func(brake *IntakeBrake) error {
		brake.SummonedAt = &summonedAt
		return nil
	}); err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}
	decided, err := store.DecideBrake(BrakeDecisionProbe, "three verdicts on three changes; probe the line", "development-manager conversation chat-1, turn 4", summonedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("DecideBrake() error = %v", err)
	}
	if decided.Brake.Decision != BrakeDecisionProbe || decided.Brake.DecidedAt == nil || decided.Brake.SummonedAt == nil {
		t.Fatalf("DecideBrake() = %#v, want the decision recorded beside the summons", decided.Brake)
	}
	if !decided.Brake.ProbeDue(summonedAt) {
		t.Fatal("ProbeDue() = false after a decision to probe, want a probe due at once")
	}
	if whose := decided.Whose(); !strings.HasPrefix(whose, "the harness's") {
		t.Fatalf("Whose() = %q, want the harness named once she decided on a probe", whose)
	}
	probing, err := store.ReviseBrake(func(brake *IntakeBrake) error {
		brake.Probe = &IntakeProbe{WorkItemID: "yoyodyne-ifd.410", StartedAt: summonedAt.Add(2 * time.Minute)}
		brake.Probes++
		brake.Decision, brake.DecidedAt = "", nil
		return nil
	})
	if err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}
	if !probing.Probing("yoyodyne-ifd.410") || probing.Probing("yoyodyne-ifd.398") {
		t.Fatalf("Probing() names the wrong item on %#v", probing.Brake.Probe)
	}
	if standing := probing.Standing(); !strings.Contains(standing, "a probe run of yoyodyne-ifd.410 is in flight") {
		t.Fatalf("Standing() = %q, want the probe named", standing)
	}

	// An escalation is the one decision that makes the hold a person's.
	escalated, err := store.DecideBrake(BrakeDecisionEscalate, "the same check fails everywhere; the machine needs looking at", "development-manager conversation chat-1, turn 6", summonedAt.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("DecideBrake(escalate) error = %v", err)
	}
	if !escalated.WaitsOnAPerson() || !strings.HasPrefix(escalated.Whose(), "the operator's") {
		t.Fatalf("escalated hold = %q, waits on a person = %t; want the operator named", escalated.Whose(), escalated.WaitsOnAPerson())
	}
	if escalated.Brake.ProbeDue(summonedAt.Add(time.Hour)) {
		t.Fatal("ProbeDue() = true on an escalated hold, want no probe however long the cooldown has run out")
	}

	// Released by the harness, the record goes with the hold.
	if _, lifted, err := store.ReleaseBrake(); err != nil || !lifted {
		t.Fatalf("ReleaseBrake() = %t, %v, want the brake's hold lifted", lifted, err)
	}
	if _, err := store.DecideBrake(BrakeDecisionRelease, "nothing", "nobody", trippedAt); !errors.Is(err, ErrNoBrakeHold) {
		t.Fatalf("DecideBrake() with no hold error = %v, want %v", err, ErrNoBrakeHold)
	}

	// The operator's hold takes no brake decision at all.
	if _, err := store.Hold(IntakeHolderOperator, "reordering the queue", trippedAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, err := store.DecideBrake(BrakeDecisionRelease, "release it", "development-manager conversation chat-1, turn 7", trippedAt); !errors.Is(err, ErrNoBrakeHold) {
		t.Fatalf("DecideBrake() on the operator's hold error = %v, want %v", err, ErrNoBrakeHold)
	}
	operators, _, _ := store.Held()
	if operators.Braked() || !operators.WaitsOnAPerson() || operators.Whose() != "the operator's — nothing new is chosen until `yoyo release` lifts it" {
		t.Fatalf("the operator's hold = %#v, whose %q; want it theirs and untouched", operators, operators.Whose())
	}
	// And the harness's own release does not lift it either.
	if _, lifted, err := store.ReleaseBrake(); err != nil || lifted {
		t.Fatalf("ReleaseBrake() over the operator's hold = %t, %v, want theirs left where it is", lifted, err)
	}
	if _, held, _ := store.Held(); !held {
		t.Fatal("the operator's hold was lifted by the harness's own release")
	}
	// And the brake tripping over it leaves it theirs, trip and all.
	again, err := store.Brake(trip, "3 run(s) blocked in a row", trippedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Brake() over the operator's hold error = %v", err)
	}
	if again.HeldBy != IntakeHolderOperator || again.Brake != nil {
		t.Fatalf("Brake() over the operator's hold = %#v, want theirs left exactly as it was", again)
	}
}

// A brake record that names no run, or a decision outside the vocabulary, is
// refused rather than written: the record returns the line to work, and a
// record nothing can read the state of is a line nobody can account for.
func TestABrakeRecordIsRefusedWhereItSaysNothing(t *testing.T) {
	t.Parallel()

	store := newIntakeStoreAt(t, t.TempDir(), "yoyodyne")
	at := time.Date(2026, 9, 19, 17, 56, 0, 0, time.UTC)
	if _, err := store.Brake(IntakeBrake{CooldownEndsAt: at}, "nothing blocked", at); err == nil {
		t.Fatal("Brake() with no blocked runs error = nil, want a refusal")
	}
	if _, held, _ := store.Held(); held {
		t.Fatal("a refused trip held intake")
	}
	trip := IntakeBrake{Blocked: []BrakeBlockedRun{{WorkItemID: "yoyodyne-1", Reason: "blocked"}}, CooldownEndsAt: at}
	if _, err := store.Brake(trip, "1 run blocked", at); err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	if _, err := store.DecideBrake("ignore", "no", "nobody", at); err == nil || !strings.Contains(err.Error(), "release, probe, escalate") {
		t.Fatalf("DecideBrake(ignore) error = %v, want the vocabulary named", err)
	}
	// A revision that leaves the record invalid is refused and leaves the record
	// as it was.
	if _, err := store.ReviseBrake(func(brake *IntakeBrake) error {
		brake.Blocked = nil
		return nil
	}); err == nil {
		t.Fatal("ReviseBrake() emptying the trip error = nil, want a refusal")
	}
	held, _, _ := store.Held()
	if len(held.Brake.Blocked) != 1 {
		t.Fatalf("held = %#v, want the record left as it was after a refused revision", held.Brake)
	}
}

// A summons claims a firing whether or not the cadence is due, and it is a
// firing like any other: counted, stamped, and paced from, so the scheduled
// pass does not follow it a minute later over the same ground.
func TestASummonsClaimsAFiringOutOfCadence(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 19, 17, 30, 0, 0, time.UTC)
	if _, err := store.Claim(ctx, "development-manager-sweep", time.Hour, at); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	summonedAt := at.Add(26 * time.Minute)
	if _, err := store.Claim(ctx, "development-manager-sweep", time.Hour, summonedAt); !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("Claim() half an hour in error = %v, want the cadence refusing", err)
	}
	summoned, err := store.Summon(ctx, "development-manager-sweep", summonedAt)
	if err != nil {
		t.Fatalf("Summon() error = %v", err)
	}
	if summoned.Firings != 2 || !summoned.FiredAt.Equal(summonedAt) {
		t.Fatalf("Summon() = %#v, want a second firing stamped at the summons", summoned)
	}
	if _, err := store.Claim(ctx, "development-manager-sweep", time.Hour, at.Add(time.Hour)); !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("Claim() an hour after the first error = %v, want the cadence paced from the summons", err)
	}
	if _, err := store.Claim(ctx, "development-manager-sweep", time.Hour, summonedAt.Add(time.Hour)); err != nil {
		t.Fatalf("Claim() an hour after the summons error = %v, want the cadence due again", err)
	}
	// A task never fired is summoned as readily as one that was.
	if fresh, err := store.Summon(ctx, "product-manager-sweep", summonedAt); err != nil || fresh.Firings != 1 {
		t.Fatalf("Summon() of a task never fired = %#v, %v, want its first firing", fresh, err)
	}
	// The record says what summoned it.
	if err := store.Append(Sweep{
		Task: "development-manager-sweep", Role: "development-manager",
		StartedAt: summonedAt, EndedAt: summonedAt.Add(time.Minute), Turns: 1,
		Problem: "the pass produced no account", Summoned: "the intake brake, after 3 run(s) blocked in a row",
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 || recorded[0].Summoned == "" {
		t.Fatalf("List() = %#v, %v, want the summoned pass read back as one", recorded, err)
	}
}
