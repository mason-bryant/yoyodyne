package runstate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A directive recorded by one process is read by another. That is the whole
// mechanism behind a directive applying regardless of which agent received it,
// so it is proved over two stores built independently on the same root rather
// than over one that happens to remember what it wrote.
func TestADirectiveRecordedByOneStoreIsReadByAnother(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	recorder := newDirectives(t, root)
	recorded := ambiguousDirective(t, "which of the two readings was meant", nil)
	if err := recorder.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	reader := newDirectives(t, root)
	listed, err := reader.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != recorded.ID {
		t.Fatalf("List() = %#v, want the directive the other store recorded", listed)
	}
	pausing, err := reader.Pausing("yoyodyne-anything")
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(pausing) != 1 || pausing[0].Unresolved != recorded.Unresolved {
		t.Fatalf("Pausing() = %#v, want the unresolved directive and what it is waiting on", pausing)
	}
}

// Resolving is what lifts a pause, so it has to be visible to the next process
// that asks rather than only to the one that settled it.
func TestResolvingADirectiveStopsItPausingWork(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newDirectives(t, root)
	recorded := ambiguousDirective(t, "which of the two readings was meant", nil)
	if err := store.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	resolved, err := store.Resolve(recorded.ID, "the second reading", time.Now())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved.Resolved() || resolved.Resolution != "the second reading" {
		t.Fatalf("Resolve() = %#v, want a settled directive carrying how it was settled", resolved)
	}
	pausing, err := newDirectives(t, root).Pausing("yoyodyne-anything")
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(pausing) != 0 {
		t.Fatalf("Pausing() = %#v, want nothing pausing once the directive is settled", pausing)
	}
	if _, err := store.Resolve(recorded.ID, "again", time.Now()); err == nil {
		t.Fatal("Resolve() on a settled directive error = nil, want a refusal")
	}
}

// An identifier is thirty-two hex digits and nobody types thirty-two hex digits,
// so a prefix is an answer where it names exactly one directive and is reported
// as ambiguous where it does not. Resolving the wrong directive because a prefix
// was resolved by sort order would release work nobody meant to release.
func TestFindResolvesAUniquePrefixAndRefusesAnAmbiguousOne(t *testing.T) {
	t.Parallel()

	store := newDirectives(t, t.TempDir())
	first := ambiguousDirective(t, "the first question", nil)
	first.ID = "directive-" + strings.Repeat("a", 32)
	second := ambiguousDirective(t, "the second question", nil)
	second.ID = "directive-" + strings.Repeat("a", 31) + "b"
	third := ambiguousDirective(t, "the third question", nil)
	third.ID = "directive-" + strings.Repeat("c", 32)
	for _, recorded := range []directive.Directive{first, second, third} {
		if err := store.Record(recorded); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	found, err := store.Find("directive-c")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.ID != third.ID {
		t.Fatalf("Find() = %q, want %q", found.ID, third.ID)
	}
	if _, err := store.Find("directive-a"); err == nil || !strings.Contains(err.Error(), "names 2 directives") {
		t.Fatalf("Find() error = %v, want an ambiguous prefix reported rather than resolved", err)
	}
	if _, err := store.Find("directive-zzz"); !errors.Is(err, ErrNoDirective) {
		t.Fatalf("Find() error = %v, want ErrNoDirective", err)
	}
}

// A directive belongs to one product. Recording another product's into this
// store would make it enforce something nobody directed about this product.
func TestAStoreRefusesADirectiveFromAnotherProduct(t *testing.T) {
	t.Parallel()

	store := newDirectives(t, t.TempDir())
	foreign := ambiguousDirective(t, "what was meant", nil)
	foreign.ProductID = "somebody-else"
	if err := store.Record(foreign); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Record() error = %v, want a refusal naming the product mismatch", err)
	}
}

// A directive is recorded once. Two processes recording at the same moment must
// not be able to overwrite each other's record.
func TestRecordingTheSameDirectiveTwiceIsRefused(t *testing.T) {
	t.Parallel()

	store := newDirectives(t, t.TempDir())
	recorded := ambiguousDirective(t, "what was meant", nil)
	if err := store.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Record(recorded); err == nil || !strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("Record() error = %v, want a refusal of the duplicate", err)
	}
}

// A product nobody has directed is not a failure to read. Every run consults
// this before it commits to work, so an empty answer has to be the ordinary one.
func TestListingDirectivesForAnUndirectedProductIsEmptyRatherThanAFailure(t *testing.T) {
	t.Parallel()

	pausing, err := newDirectives(t, t.TempDir()).Pausing("yoyodyne-anything")
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(pausing) != 0 {
		t.Fatalf("Pausing() = %#v, want nothing", pausing)
	}
}

func newDirectives(t *testing.T, root string) *DirectiveStore {
	t.Helper()
	store, err := NewDirectiveStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	return store
}

func ambiguousDirective(t *testing.T, unresolved string, scope []string) directive.Directive {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("directive.NewID() error = %v", err)
	}
	return directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
		Text:          "hold the publishing change until I have decided",
		Unresolved:    unresolved,
		Scope:         scope,
	}
}
