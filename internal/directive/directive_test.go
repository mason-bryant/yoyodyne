package directive

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var recordedAt = time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

// A directive that pauses work has to say what it is waiting for, and one that
// does not must not pretend to. Both halves are what keeps a pause liftable: a
// record with nothing unresolved stops work nobody can release, and an
// operational directive carrying one would read as holding work up when it is
// already in effect.
func TestValidateHoldsEachKindToWhatItMustSay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Directive)
		wantErr string
	}{
		{name: "an operational directive needs nothing unresolved"},
		{
			name:    "an ambiguous directive must name what is unresolved",
			mutate:  func(d *Directive) { d.Kind = KindAmbiguous },
			wantErr: "unresolved is required",
		},
		{
			name: "an ambiguous directive that names it is valid",
			mutate: func(d *Directive) {
				d.Kind = KindAmbiguous
				d.Unresolved = "which of the two readings was meant"
			},
		},
		{
			name: "an artifact directive must name the artifact",
			mutate: func(d *Directive) {
				d.Kind = KindArtifact
				d.Unresolved = "whether the goal still covers this"
			},
			wantErr: "artifact is required",
		},
		{
			name:    "an operational directive may not name an artifact",
			mutate:  func(d *Directive) { d.Artifact = "docs/product/brief.md" },
			wantErr: "names an artifact",
		},
		{
			name:    "an operational directive may not carry something unresolved",
			mutate:  func(d *Directive) { d.Unresolved = "what was meant" },
			wantErr: "in effect already",
		},
		{
			name:    "a kind the harness does not know is refused",
			mutate:  func(d *Directive) { d.Kind = "urgent" },
			wantErr: "is not a directive kind",
		},
		{
			name:    "a resolution with no time is half a settlement",
			mutate:  func(d *Directive) { d.Resolution = "settled in conversation" },
			wantErr: "requires the time it was resolved",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorded := operational()
			if test.mutate != nil {
				test.mutate(&recorded)
			}
			err := recorded.Validate()
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v, want none", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// Only an unresolved directive of a pausing kind holds work up. Settling one is
// exactly what releases the work, so a resolved record must stop constraining
// anything the moment it is settled.
func TestOnlyUnresolvedArtifactAndAmbiguousDirectivesPauseWork(t *testing.T) {
	t.Parallel()

	for _, kind := range []Kind{KindOperational, KindArtifact, KindAmbiguous} {
		recorded := operational()
		recorded.Kind = kind
		if kind.Pauses() {
			recorded.Unresolved = "what was meant"
		}
		if kind == KindArtifact {
			recorded.Artifact = "docs/product/brief.md"
		}
		if got := recorded.Pauses(); got != kind.Pauses() {
			t.Fatalf("%s Pauses() = %t, want %t", kind, got, kind.Pauses())
		}
		if !kind.Pauses() {
			continue
		}
		resolved, err := recorded.Resolve("the operator answered", recordedAt.Add(time.Hour))
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if resolved.Pauses() {
			t.Fatalf("a resolved %s directive still pauses work", kind)
		}
		if _, err := resolved.Resolve("again", recordedAt.Add(2*time.Hour)); err == nil {
			t.Fatal("Resolve() on a settled directive error = nil, want a refusal")
		}
	}
}

// An operational directive is settled by being carried out, which is the only
// account there is of what became of the commonest kind of directive: it takes
// effect the moment it is recorded and has nothing to resolve, so without an
// outcome it stands open forever and whoever asked for it is never told the work
// exists.
func TestAnOperationalDirectiveIsSettledByBeingCarriedOut(t *testing.T) {
	t.Parallel()

	recorded := operational()
	if recorded.Resolved() {
		t.Fatal("a directive nobody has settled reads as settled")
	}
	carried, err := recorded.CarryOut("admitted yoyodyne-ifd.170 to the backlog: stop opening those pull requests", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if !carried.Resolved() || carried.Pauses() {
		t.Fatalf("carried = %#v, want a settled directive that pauses nothing", carried)
	}
	// The outcome names the work, because a thread told its directive was acted
	// on is told nothing it can follow.
	if !strings.Contains(carried.Render(), "carried out") || !strings.Contains(carried.Render(), "yoyodyne-ifd.170") {
		t.Fatalf("Render() = %q, want it to say it was carried out and name what it became", carried.Render())
	}
	// One record, one account of what became of it.
	if _, err := carried.CarryOut("admitted again", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("CarryOut() on a settled directive error = nil, want a refusal")
	}
	if _, err := recorded.CarryOut("", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("CarryOut() with no outcome error = nil, want a refusal")
	}
}

// The two acts refuse each other's kinds. Resolving is somebody answering what
// held work up and carrying out is somebody having done what was asked, so an
// outcome written onto a pausing directive would lift its pause on the strength
// of something that never answered it.
func TestResolvingAndCarryingOutRefuseEachOthersKinds(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	if _, err := pausing.CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("CarryOut() on a directive that pauses work error = nil, want a refusal")
	}
	if _, err := operational().Resolve("the second reading", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("Resolve() on a directive that pauses nothing error = nil, want a refusal")
	}
}

// A reference is what somebody types to name a directive, and it is checked
// before anything is recorded against it. Whether any directive answers to it
// needs the records; this only refuses what was never going to name one.
func TestValidReferenceAcceptsAnIdentifierAndAnyPrefixOfOne(t *testing.T) {
	t.Parallel()

	full := "directive-" + strings.Repeat("0", 32)
	for _, valid := range []string{full, "directive-0", "directive-3f2a", "  " + full + "  "} {
		if !ValidReference(valid) {
			t.Fatalf("ValidReference(%q) = false, want a reference the store can look up", valid)
		}
	}
	for _, invalid := range []string{"", "directive-", "3f2a", "directive-zzzz", full + "0", "yoyodyne-ifd.170"} {
		if ValidReference(invalid) {
			t.Fatalf("ValidReference(%q) = true, want it refused", invalid)
		}
	}
}

// An unscoped directive reaches every item. It is the conservative reading and a
// deliberate one: a directive that rewrites the brief affects whatever was
// derived from the brief, and nothing here can yet say which work that is.
func TestAnUnscopedDirectiveAffectsEveryItemAndAScopedOneOnlyWhatItNames(t *testing.T) {
	t.Parallel()

	unscoped := operational()
	if !unscoped.Affects("yoyodyne-anything") {
		t.Fatal("an unscoped directive did not affect an item")
	}
	scoped := operational()
	scoped.Scope = []string{"yoyodyne-ifd.1", "yoyodyne-ifd.2"}
	if !scoped.Affects("yoyodyne-ifd.2") {
		t.Fatal("a scoped directive did not affect an item it named")
	}
	if scoped.Affects("yoyodyne-ifd.3") {
		t.Fatal("a scoped directive affected an item it did not name")
	}
}

// Pausing is the one question the run pipeline asks, so it has to select on both
// halves at once: the kind that pauses, and the item it reaches.
func TestPausingSelectsOnlyWhatHoldsOneItemUp(t *testing.T) {
	t.Parallel()

	ambiguous := operational()
	ambiguous.ID = "directive-" + strings.Repeat("a", 32)
	ambiguous.Kind = KindAmbiguous
	ambiguous.Unresolved = "what was meant"
	ambiguous.Scope = []string{"yoyodyne-ifd.1"}

	elsewhere := ambiguous
	elsewhere.ID = "directive-" + strings.Repeat("b", 32)
	elsewhere.Scope = []string{"yoyodyne-ifd.9"}

	pausing := Pausing([]Directive{operational(), ambiguous, elsewhere}, "yoyodyne-ifd.1")
	if len(pausing) != 1 || pausing[0].ID != ambiguous.ID {
		t.Fatalf("Pausing() = %#v, want only the unresolved directive scoped to the item", pausing)
	}
}

// What a paused run records about why has to name both the directive and what is
// unresolved: those are the two things somebody needs in order to lift it.
func TestSummaryNamesTheDirectiveAndWhatIsUnresolved(t *testing.T) {
	t.Parallel()

	recorded := operational()
	recorded.Kind = KindAmbiguous
	recorded.Unresolved = "which of the two readings was meant"
	summary := recorded.Summary()
	for _, wanted := range []string{recorded.ID, string(KindAmbiguous), recorded.Text, recorded.Unresolved} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("Summary() = %q, want it to mention %q", summary, wanted)
		}
	}
}

func operational() Directive {
	return Directive{
		SchemaVersion: SchemaVersion,
		ID:            "directive-" + strings.Repeat("0", 32),
		ProductID:     "yoyodyne",
		Kind:          KindOperational,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    recordedAt,
		Text:          "stop opening pull requests for documentation-only changes",
	}
}
