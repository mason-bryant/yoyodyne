package runstate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// An exchange outlives the process that conducted it, which is the durability
// the channel's first property rests on: what two roles said to each other is
// readable afterwards by anybody, from a record neither of their conversations
// owns.
func TestAnExchangeOutlivesTheProcessThatConductedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestExchangeStore(t, root)
	recorded := testExchange("a")
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// A second process, addressing the same state root.
	reopened := newTestExchangeStore(t, root)
	loaded, err := reopened.Load(recorded.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.MaxRounds != recorded.MaxRounds || len(loaded.Rounds) != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Rounds[0].Answer != "More than the ordering assumes." {
		t.Fatalf("answer = %q", loaded.Rounds[0].Answer)
	}
}

// Revising an exchange replaces it, because the record is one thread rather than
// a stream of events about one.
func TestSavingAnExchangeAgainReplacesIt(t *testing.T) {
	t.Parallel()

	store := newTestExchangeStore(t, t.TempDir())
	recorded := testExchange("b")
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	closed := recorded.OpenedAt.Add(time.Minute)
	recorded.Outcome = exchange.OutcomeResolved
	recorded.Settled = "twice what the ordering assumed"
	recorded.ClosedAt = &closed
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() again error = %v", err)
	}
	loaded, err := store.Load(recorded.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Open() || loaded.Settled != "twice what the ordering assumed" {
		t.Fatalf("loaded = %+v", loaded)
	}
}

// A listing puts the exchanges still being conducted first, and a prefix names
// one, because nobody types thirty-two hex digits out of one.
func TestListingPutsOpenExchangesFirstAndAPrefixNamesOne(t *testing.T) {
	t.Parallel()

	store := newTestExchangeStore(t, t.TempDir())
	open := testExchange("c")
	settled := testExchange("d")
	closed := settled.OpenedAt.Add(time.Minute)
	settled.Outcome = exchange.OutcomeResolved
	settled.ClosedAt = &closed
	settled.UpdatedAt = closed
	for _, one := range []exchange.Exchange{settled, open} {
		if err := store.Save(one); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 || listed[0].ID != open.ID {
		t.Fatalf("listing did not put the open exchange first: %+v", listed)
	}

	found, err := store.Find(open.ID[:16])
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.ID != open.ID {
		t.Fatalf("Find() = %s, want %s", found.ID, open.ID)
	}
	if _, err := store.Find("exchange-" + strings.Repeat("f", 32)); !errors.Is(err, ErrNoExchange) {
		t.Fatalf("Find() on nothing error = %v, want ErrNoExchange", err)
	}
	// A prefix every exchange shares names none of them rather than the one that
	// sorted first.
	if _, err := store.Find("exchange-"); err == nil {
		t.Fatal("an ambiguous prefix resolved to something")
	}
}

// An identifier that came from outside can never name a path, and a record that
// belongs to another product is refused rather than read.
func TestAnExchangeStoreRefusesWhatIsNotItsOwn(t *testing.T) {
	t.Parallel()

	store := newTestExchangeStore(t, t.TempDir())
	if _, err := store.Load("../../etc/passwd"); err == nil {
		t.Fatal("Load() accepted a path")
	}
	elsewhere := testExchange("e")
	elsewhere.ProductID = domain.ProductID("other")
	if err := store.Save(elsewhere); err == nil {
		t.Fatal("Save() accepted another product's exchange")
	}
	if _, err := store.Load(elsewhere.ID); !errors.Is(err, ErrNoExchange) {
		t.Fatalf("Load() error = %v, want ErrNoExchange", err)
	}
}

func newTestExchangeStore(t *testing.T, root string) *ExchangeStore {
	t.Helper()

	store, err := NewExchangeStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() error = %v", err)
	}
	return store
}

func testExchange(seed string) exchange.Exchange {
	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	answered := at.Add(time.Second)
	return exchange.Exchange{
		SchemaVersion: exchange.SchemaVersion,
		ID:            "exchange-" + strings.Repeat(seed, 32),
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Asker:         exchange.Party{Role: domain.RoleProductManager, Agent: "product-manager"},
		Answerer:      exchange.Party{Role: domain.RoleArchitect, Agent: "architect"},
		Question:      "what does this goal cost, and what am I missing?",
		MaxRounds:     10,
		Rounds: []exchange.Round{{
			Number:     1,
			Question:   "what does this goal cost, and what am I missing?",
			Answer:     "More than the ordering assumes.",
			CostUSD:    0.25,
			AskedAt:    at,
			AnsweredAt: &answered,
		}},
		OpenedAt:  at,
		UpdatedAt: answered,
	}
}
