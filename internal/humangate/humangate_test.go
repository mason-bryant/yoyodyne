package humangate

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestAGateIsWhatWasWrittenAfterTheMarker(t *testing.T) {
	t.Parallel()

	reading := Read("Flip the default once the soak is judged.\n\n" +
		DeclareMarker + " soak-reviewed — the operator has read a week of soak runs and is content\n")
	if len(reading.Gates) != 1 || len(reading.Unreadable) != 0 {
		t.Fatalf("Read() = %#v, want one gate and nothing unreadable", reading)
	}
	if reading.Gates[0].Name != "soak-reviewed" {
		t.Fatalf("name = %q", reading.Gates[0].Name)
	}
	if reading.Gates[0].Statement != "the operator has read a week of soak runs and is content" {
		t.Fatalf("statement = %q", reading.Gates[0].Statement)
	}
	if !reading.Holds() {
		t.Fatal("a declared gate nobody has passed does not hold the work")
	}
}

// The whole cost model of this package: a surface missed costs a race the
// harness handles, and a gate invented from a sentence stops work nobody meant
// to stop. So prose about gates declares none.
func TestProseAboutGatesDeclaresNoGate(t *testing.T) {
	t.Parallel()

	prose := "This item is about the human gate machinery. A gate is a step only a person can take, " +
		"and the working rule until it exists is that an operator step gets its own gating item. " +
		"Nothing here is a human-gate: declaration, because the marker has to begin the line."
	if reading := Read(prose); reading.Holds() {
		t.Fatalf("Read() = %#v, want nothing inferred from prose", reading)
	}
}

func TestAMarkerAfterMarkdownDecorationStillDeclares(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"- " + DeclareMarker + " soak-reviewed — the operator has judged the soak",
		"> " + DeclareMarker + " soak-reviewed — the operator has judged the soak",
		"  `" + DeclareMarker + "` soak-reviewed — the operator has judged the soak",
		strings.ToUpper(DeclareMarker) + " SOAK-REVIEWED — the operator has judged the soak",
	} {
		reading := Read(line)
		if len(reading.Gates) != 1 || reading.Gates[0].Name != "soak-reviewed" {
			t.Fatalf("Read(%q) = %#v", line, reading)
		}
	}
}

// A declaration nobody can read holds the work exactly as a gate does, and says
// what is wrong with it.
//
// This is the failure mode that would otherwise reintroduce the whole regression
// through the parser: an author writes a line meaning to reserve their own step,
// mistypes it, and the work is pulled past a step nobody was ever asked to take.
// The typo has to stop the work, not the reservation.
func TestADeclarationNobodyCanReadHoldsTheWork(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		text string
		want string
	}{
		{name: "no name at all", text: DeclareMarker + "\n", want: "has no name"},
		{name: "a name nothing could record", text: DeclareMarker + " Soak Reviewed!! — the operator has judged it\n", want: "is not a gate name"},
		{name: "nothing said about the act", text: DeclareMarker + " soak-reviewed\n", want: "does not say what the person has to do"},
		{
			name: "two gates of one name saying different things",
			text: DeclareMarker + " soak-reviewed — the operator has read the soak\n" +
				DeclareMarker + " soak-reviewed — somebody else has read the soak\n",
			want: "declared twice",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reading := Read(testCase.text)
			if !reading.Holds() {
				t.Fatalf("Read(%q) = %#v, want the unreadable declaration to hold the work", testCase.text, reading)
			}
			if len(reading.Unreadable) == 0 {
				t.Fatalf("Read(%q) = %#v, want the declaration reported as unreadable", testCase.text, reading)
			}
			if !strings.Contains(reading.Unreadable[0], testCase.want) {
				t.Fatalf("unreadable = %q, want it to mention %q", reading.Unreadable[0], testCase.want)
			}
			// Nothing discharges one: there is no name to have recorded an act
			// against, so a store full of acts leaves it holding.
			if !reading.Pending([]string{"soak-reviewed", "soak"}).Holds() {
				t.Fatalf("a recorded act passed a declaration nothing could read: %#v", reading)
			}
			// And what it says names the author rather than the command, because
			// typing a command would not fix it.
			if !strings.Contains(reading.Describe(), "correcting the line") {
				t.Fatalf("Describe() = %q, want it to say what actually clears this", reading.Describe())
			}
		})
	}
}

func TestAWellFormedDeclarationIsNotAProblem(t *testing.T) {
	t.Parallel()

	text := DeclareMarker + " soak-reviewed — the operator has judged the soak\n" +
		DeclareMarker + " release-signed — the operator has signed the release off\n"
	reading := Read(text)
	if len(reading.Unreadable) != 0 {
		t.Fatalf("unreadable = %v", reading.Unreadable)
	}
	if len(reading.Gates) != 2 {
		t.Fatalf("gates = %#v", reading.Gates)
	}
	// One line said twice is one step, not two: an author who repeated their own
	// declaration meant to reserve one act.
	repeated := Read(text, text)
	if len(repeated.Gates) != 2 || len(repeated.Unreadable) != 0 {
		t.Fatalf("Read() over a repeated declaration = %#v", repeated)
	}
}

// The notes are where the harness appends every run's own record, so a gate read
// from them would be a gate declared on the item after the fact, by a run rather
// than by the person whose step it is.
func TestAGateIsNotReadFromTheNotesTheHarnessWritesInto(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:          "yoyodyne-ifd.209.7",
		Title:       "Declarative becomes the default",
		Description: DeclareMarker + " soak-reviewed — the operator has judged the soak\n",
		Notes:       DeclareMarker + " invented-by-a-run — a summary that happened to name one\n",
	}
	reading := Of(item)
	if len(reading.Gates) != 1 || reading.Gates[0].Name != "soak-reviewed" {
		t.Fatalf("Of() = %#v, want only what the item's author wrote", reading)
	}
}

func TestPendingIsWhatNoRecordedActHasPassed(t *testing.T) {
	t.Parallel()

	reading := Read(DeclareMarker + " soak-reviewed — the operator has judged the soak\n" +
		DeclareMarker + " release-signed — the operator has signed the release off\n")
	pending := reading.Pending([]string{"release-signed"})
	if len(pending.Gates) != 1 || pending.Gates[0].Name != "soak-reviewed" {
		t.Fatalf("Pending() = %#v", pending)
	}
	if remaining := reading.Pending([]string{"soak-reviewed", "release-signed"}); remaining.Holds() {
		t.Fatalf("Pending() = %#v, want nothing left once both acts are on the record", remaining)
	}
	// A caller that knows of no recorded act holds every gate, which is the
	// direction this fails in on purpose.
	if none := reading.Pending(nil); len(none.Gates) != 2 {
		t.Fatalf("Pending() = %#v, want every gate held when nothing is known to be discharged", none)
	}
}

// The sentence every surface prints has one job, and a surface that printed a
// gate without saying that closure does not pass it would be describing a wait.
func TestWhatASurfaceSaysNamesTheActAndRulesOutMachinery(t *testing.T) {
	t.Parallel()

	described := Read(DeclareMarker + " soak-reviewed — the operator has judged the soak\n").Describe()
	for _, want := range []string{"waiting on a person", "soak-reviewed", "closing an item", "yoyo gate record"} {
		if !strings.Contains(described, want) {
			t.Fatalf("Describe() = %q, want it to mention %q", described, want)
		}
	}
	if empty := (Reading{}).Describe(); empty != "" {
		t.Fatalf("Describe() = %q, want nothing said where nothing holds", empty)
	}
}
