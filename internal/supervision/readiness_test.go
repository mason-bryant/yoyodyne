package supervision

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Each judgment belongs to one role. Three judgments any role could write would
// be one judgment with three names, and the whole value of asking three roles is
// that they answer independently.
func TestAJudgmentIsSignedByTheRoleThatOwnsIt(t *testing.T) {
	t.Parallel()

	for _, judgment := range Judgments() {
		record := testReadiness(1, judgment)
		if err := record.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for %s", err, judgment)
		}
		if record.JudgedBy != judgment.Owner() {
			t.Fatalf("%s is owned by %q", judgment, judgment.Owner())
		}
		wrongHands := record
		wrongHands.JudgedBy = domain.RoleReviewer
		assertRefused(t, wrongHands.Validate(), "signed by")
	}
}

// The revisions are the whole of what makes a judgment revision-aware. One made
// against nothing can never be told from a current one, so it does not load.
func TestAJudgmentRecordsWhatItWasJudgedAgainst(t *testing.T) {
	t.Parallel()

	record := testReadiness(1, JudgmentArchitecture)
	record.Against = nil
	assertRefused(t, record.Validate(), "records what it was judged against")
}

// A disposition with nothing behind it is an assertion. This is advisory
// precisely because somebody downstream reads the reasoning rather than obeying
// the verdict.
func TestAJudgmentSaysWhatItRestsOn(t *testing.T) {
	t.Parallel()

	record := testReadiness(1, JudgmentProduct)
	record.Evidence = "   "
	assertRefused(t, record.Validate(), "evidence is required")
}

// Staleness is derived from the records rather than marked on them: something
// the judgment read has moved since, which is a comparison anybody can make at
// any time.
func TestAJudgmentIsStaleWhenSomethingItReadHasMoved(t *testing.T) {
	t.Parallel()

	record := testReadiness(1, JudgmentArchitecture)
	record.Against = []Reference{
		{What: "artifact", ID: "v1-goals", Revision: "r7"},
		{What: "artifact", ID: "management-and-supervision", Revision: "r2"},
	}

	unchanged := map[string]string{
		"artifact/v1-goals":                   "r7",
		"artifact/management-and-supervision": "r2",
	}
	if record.Stale(unchanged) {
		t.Fatalf("Stale() = true against the revisions it was judged on")
	}

	moved := map[string]string{
		"artifact/v1-goals":                   "r8",
		"artifact/management-and-supervision": "r2",
	}
	changed := record.Moved(moved)
	if len(changed) != 1 || changed[0].Reference.ID != "v1-goals" || changed[0].Now != "r8" {
		t.Fatalf("Moved() = %#v, want the goals named at what they are now", changed)
	}
	if !record.Stale(moved) {
		t.Fatalf("Stale() = false with a reference moved")
	}
}

// Silence is not evidence that something held still. A reference nothing
// current is known about is answered as one nothing could be said about, which
// is a different thing from one that moved and is reported separately.
func TestAReferenceNothingIsKnownAboutIsNotMovement(t *testing.T) {
	t.Parallel()

	record := testReadiness(1, JudgmentDelivery)
	record.Against = []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}}

	if record.Stale(map[string]string{"artifact/something-else": "r1"}) {
		t.Fatalf("Stale() = true for a reference nothing current was said about")
	}
	unread := record.Unknown(map[string]string{"artifact/something-else": "r1"})
	if len(unread) != 1 || unread[0].ID != "v1-goals" {
		t.Fatalf("Unknown() = %#v, want the reference nothing was said about", unread)
	}
}

// A role that judges an item again records a new judgment rather than editing
// the old one, so what was judged when stays readable. Which one stands is a
// question about the records rather than about what is on disk.
func TestTheLatestJudgmentPerItemAndRoleIsTheOneThatStands(t *testing.T) {
	t.Parallel()

	first := testReadiness(1, JudgmentArchitecture)
	first.Disposition = DispositionDesignNeeded
	second := testReadiness(2, JudgmentArchitecture)
	second.Disposition = DispositionClear
	second.JudgedAt = first.JudgedAt.Add(time.Hour)
	other := testReadiness(3, JudgmentProduct)

	current := Current([]Readiness{second, first, other})
	if len(current) != 2 {
		t.Fatalf("Current() = %#v, want one judgment per item and role", current)
	}
	byJudgment := make(map[Judgment]Readiness, len(current))
	for _, record := range current {
		byJudgment[record.Judgment] = record
	}
	if standing := byJudgment[JudgmentArchitecture]; standing.ID != second.ID {
		t.Fatalf("the standing architecture judgment is %q, want the later one", standing.ID)
	}
	if _, judged := byJudgment[JudgmentProduct]; !judged {
		t.Fatalf("Current() dropped the product judgment: %#v", current)
	}
}

// Two judgments recorded at the same instant are settled by identifier, so two
// readings of one store agree about which one stands.
func TestJudgmentsRecordedAtOneInstantAreSettledTheSameWayTwice(t *testing.T) {
	t.Parallel()

	first := testReadiness(1, JudgmentDelivery)
	second := testReadiness(2, JudgmentDelivery)
	second.JudgedAt = first.JudgedAt

	forwards := Current([]Readiness{first, second})
	backwards := Current([]Readiness{second, first})
	if len(forwards) != 1 || len(backwards) != 1 || forwards[0].ID != backwards[0].ID {
		t.Fatalf("Current() = %#v and %#v from the same set", forwards, backwards)
	}
}

func TestAReadinessIdentifierIsCheckedBeforeItNamesAPath(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "readiness", "readiness-zz", "../escape", testReadinessID(1) + "x"} {
		if ValidReadinessID(id) {
			t.Fatalf("ValidReadinessID(%q) = true", id)
		}
	}
	issued, err := NewReadinessID()
	if err != nil {
		t.Fatalf("NewReadinessID() error = %v", err)
	}
	if !ValidReadinessID(issued) {
		t.Fatalf("NewReadinessID() issued %q, which it will not accept back", issued)
	}
}

func testReadiness(n int, judgment Judgment) Readiness {
	return Readiness{
		SchemaVersion: SchemaVersion,
		ID:            testReadinessID(n),
		ProductID:     "yoyodyne",
		Item:          "yoyodyne-ifd.142",
		Judgment:      judgment,
		Disposition:   DispositionClear,
		Evidence:      "the contract is ratified and the slice is bounded to three properties",
		Against:       []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}},
		JudgedBy:      judgment.Owner(),
		JudgedAt:      time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC),
	}
}
