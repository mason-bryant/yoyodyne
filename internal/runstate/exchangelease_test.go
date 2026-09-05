package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// One exchange takes its rounds one at a time, and the lease is what makes that
// true rather than conventional. Two processes putting a round on the same
// thread would each load the record, each append a round numbered the same, and
// the second write would take the first away: one round the operator paid for
// and nothing recorded, against a cap counting one where two were spent.
func TestOnlyOneProcessAtATimeMayCarryAnExchange(t *testing.T) {
	t.Parallel()

	store, recorded := storedExchange(t, "exchange-"+strings.Repeat("a", 32))

	first, taken, err := store.Hold(recorded.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() = %v, %t, %v; want the lease taken", first, taken, err)
	}
	second, taken, err := store.Hold(recorded.ID)
	if err != nil {
		t.Fatalf("Hold() while held = %v", err)
	}
	if taken {
		second.Release()
		t.Fatal("Hold() took a lease a live holder already has")
	}

	// A lease the operating system drops when its holder exits is what a restart
	// finds free, which is the whole of how an interrupted round is told from one
	// being taken. Releasing it is the same event as the process dying.
	first.Release()
	third, taken, err := store.Hold(recorded.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() after release = %v, %t, %v; want the lease free again", third, taken, err)
	}
	third.Release()
}

// The leases live beside the records rather than among them: a lease is about
// who is working on an exchange and not part of what the exchange says, so
// nothing that reads the threads ever sees one.
func TestTakingALeaseDoesNotAppearAmongTheExchanges(t *testing.T) {
	t.Parallel()

	store, recorded := storedExchange(t, "exchange-"+strings.Repeat("b", 32))
	lease, taken, err := store.Hold(recorded.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() = %v, %t, %v", lease, taken, err)
	}
	defer lease.Release()

	ids, err := store.Records()
	if err != nil {
		t.Fatalf("Records() = %v", err)
	}
	if len(ids) != 1 || ids[0] != recorded.ID {
		t.Errorf("Records() = %v, want only the exchange", ids)
	}
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("List() = %#v, want only the exchange", listed)
	}
}

// An identifier that came from outside cannot name a lease path, for the reason
// it cannot name a record path: the store builds a filename out of it.
func TestALeaseIsRefusedForAnythingThatIsNotAnExchangeIdentifier(t *testing.T) {
	t.Parallel()

	store, err := NewExchangeStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() = %v", err)
	}
	for _, id := range []string{"", "../../etc/passwd", "exchange-nothex", "run-" + strings.Repeat("a", 32)} {
		if _, taken, err := store.Hold(id); err == nil || taken {
			t.Errorf("Hold(%q) = %t, %v; want a refusal", id, taken, err)
		}
	}
}

func storedExchange(t *testing.T, id string) (*ExchangeStore, exchange.Exchange) {
	t.Helper()
	store, err := NewExchangeStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() = %v", err)
	}
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	recorded := exchange.Exchange{
		SchemaVersion: exchange.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Asker:         exchange.Party{Role: domain.RoleProductManager, Conversation: "chat-1"},
		Answerer:      exchange.Party{Role: domain.RoleArchitect},
		Question:      "what am I missing?",
		MaxRounds:     3,
		OpenedAt:      at,
		UpdatedAt:     at,
	}
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	return store, recorded
}
