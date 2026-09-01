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

const escalationDocketKey = "stopped_run:run-0123456789abcdef0123456789abcdef"

func newEscalationStore(t *testing.T) *EscalationStore {
	t.Helper()
	store, err := NewEscalationStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewEscalationStore() error = %v", err)
	}
	return store
}

func attemptedEscalation() Escalation {
	return Escalation{
		DocketKey:        escalationDocketKey,
		RunID:            "run-0123456789abcdef0123456789abcdef",
		WorkItemID:       "yoyodyne-ifd.209.16",
		FirstAttemptedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
	}
}

func delivered() Delivery {
	return Delivery{
		At:             time.Date(2026, 9, 1, 9, 1, 0, 0, time.UTC),
		ConversationID: "chat-91253e0e070c17b0663651cc48602122",
		Decision:       "repair",
		Reason:         "the findings are narrow and the change is preserved, so one more bounded attempt is worth it",
	}
}

// The bound this record exists for. One docketed stoppage is put in front of the
// development manager once, so the same evidence is never in front of her twice
// under two decisions.
func TestADeliveredStoppageIsNotDeliveredAgain(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	_, err := store.Attempt(context.Background(), attemptedEscalation())
	if !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("second Attempt() error = %v, want the delivery refused", err)
	}
	var spent EscalationSpentError
	if !errors.As(err, &spent) || !spent.Existing.Delivered() {
		t.Fatalf("refusal = %#v, want it to carry the delivery that already happened", err)
	}
}

// A delivery that failed is worth making again, and not forever. The bound is
// what separates riding out a provider that was briefly unreachable from
// spending every pass on a conversation nothing can open.
func TestDeliveriesThatFailAreBounded(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	for attempt := 1; attempt <= MaxEscalationAttempts; attempt++ {
		recorded, err := store.Attempt(context.Background(), attemptedEscalation())
		if err != nil {
			t.Fatalf("attempt %d: Attempt() error = %v", attempt, err)
		}
		if recorded.Attempts != attempt {
			t.Fatalf("attempt %d recorded %d attempt(s)", attempt, recorded.Attempts)
		}
		if _, err := store.Settle(context.Background(), escalationDocketKey, Delivery{Problem: "the conversation could not be held"}); err != nil {
			t.Fatalf("attempt %d: Settle() error = %v", attempt, err)
		}
	}
	_, err := store.Attempt(context.Background(), attemptedEscalation())
	if !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("Attempt() past the bound error = %v, want it refused", err)
	}
	if !strings.Contains(err.Error(), "needs a person") {
		t.Fatalf("refusal = %q, want it to say a stoppage nobody could reach her about needs a person", err)
	}
}

// Two processes delivering one stoppage at the same instant are one delivery and
// one refusal, because the read and the write are under the stoppage's own lock.
func TestConcurrentAttemptsAtOneStoppageDoNotExceedTheBound(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	var group sync.WaitGroup
	attempts := make([]int, 6)
	failures := make([]error, len(attempts))
	for index := range attempts {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			recorded, err := store.Attempt(context.Background(), attemptedEscalation())
			attempts[index] = recorded.Attempts
			failures[index] = err
		}(index)
	}
	group.Wait()
	taken := 0
	for index, err := range failures {
		switch {
		case err == nil:
			taken++
			if attempts[index] < 1 || attempts[index] > MaxEscalationAttempts {
				t.Fatalf("attempt recorded as %d of %d permitted", attempts[index], MaxEscalationAttempts)
			}
		case errors.Is(err, ErrEscalationSpent):
		default:
			t.Fatalf("Attempt() error = %v, want either the attempt or the refusal", err)
		}
	}
	if taken != MaxEscalationAttempts {
		t.Fatalf("attempts taken = %d, want the %d the bound permits", taken, MaxEscalationAttempts)
	}
}

// An attempt whose turn provably never happened is given back, so the stoppage
// keeps every delivery it is owed.
func TestAnUndeliveredAttemptIsWithdrawn(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if err := store.Withdraw(context.Background(), escalationDocketKey); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if _, found, err := store.Find(escalationDocketKey); err != nil || found {
		t.Fatalf("Find() = found %v, error %v, want the attempt gone", found, err)
	}
	// And the stoppage may be delivered again, which is the whole point of giving
	// the attempt back.
	recorded, err := store.Attempt(context.Background(), attemptedEscalation())
	if err != nil {
		t.Fatalf("Attempt() after a withdrawal error = %v", err)
	}
	if recorded.Attempts != 1 {
		t.Fatalf("attempts after a withdrawal = %d, want the stoppage to start again at 1", recorded.Attempts)
	}
}

// What was said to her is not something to un-say. A delivered escalation is
// refused rather than removed, because a second delivery of it is the thing this
// record exists to prevent.
func TestADeliveredEscalationIsNotWithdrawn(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	err := store.Withdraw(context.Background(), escalationDocketKey)
	if err == nil || !strings.Contains(err.Error(), "delivered to the development manager") {
		t.Fatalf("Withdraw() error = %v, want a delivered escalation refused", err)
	}
}

// What she decided outlives the process that delivered the stoppage, which is
// what makes this a record rather than a log line.
func TestWhatSheDecidedIsReadBack(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	recorded, found, err := store.Find(escalationDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v", found, err)
	}
	if recorded.Decision != "repair" || recorded.ConversationID != "chat-91253e0e070c17b0663651cc48602122" {
		t.Fatalf("recorded = %#v, want her decision and the conversation it was made in", recorded)
	}
	if !recorded.Delivered() || !recorded.Spent() {
		t.Fatalf("recorded = %#v, want a delivered stoppage nothing delivers again", recorded)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].DocketKey != escalationDocketKey {
		t.Fatalf("List() = %#v, error %v, want the one escalation", listed, err)
	}
}

// A delivery she answered without deciding anything is an answer. What must
// never be recordable is the opposite: a decision on a stoppage nobody ever put
// to her.
func TestADecisionCannotBeRecordedOnAnUndeliveredStoppage(t *testing.T) {
	t.Parallel()

	escalation := attemptedEscalation()
	escalation.SchemaVersion = EscalationSchemaVersion
	escalation.ProductID = domain.ProductID("yoyodyne")
	escalation.Attempts = 1
	escalation.UpdatedAt = escalation.FirstAttemptedAt
	escalation.Decision = "repair"
	err := escalation.Validate()
	if err == nil || !strings.Contains(err.Error(), "never delivered") {
		t.Fatalf("Validate() error = %v, want a decision on an undelivered stoppage refused", err)
	}
}

// A record nobody delivered says so, because a stoppage that reached nobody is
// exactly what somebody has to be able to find.
func TestAnUndeliveredEscalationSaysSo(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	recorded, err := store.Settle(context.Background(), escalationDocketKey, Delivery{Problem: "Claude Code is not authenticated"})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if recorded.Delivered() {
		t.Fatalf("recorded = %#v, want a stoppage that reached nobody", recorded)
	}
	rendered := recorded.Render()
	if !strings.Contains(rendered, "has not reached the development manager") || !strings.Contains(rendered, "not authenticated") {
		t.Fatalf("Render() = %q, want it to say the stoppage reached nobody and why", rendered)
	}
}

// The record belongs to one product, like every other record beside it: one read
// from another product's store is refused rather than answered.
func TestAnEscalationOfAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewEscalationStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewEscalationStore() error = %v", err)
	}
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	other := &EscalationStore{root: store.Root(), productID: domain.ProductID("elsewhere")}
	if _, _, err := other.Find(escalationDocketKey); err == nil || !strings.Contains(err.Error(), "belongs to product") {
		t.Fatalf("Find() error = %v, want another product's record refused", err)
	}
}
