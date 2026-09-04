package humangate

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestAGateIsWhatWasWrittenAfterTheMarker(t *testing.T) {
	t.Parallel()

	declared := Declared("Flip the default once the soak is judged.\n\n" +
		DeclareMarker + " soak-reviewed — the operator has read a week of soak runs and is content\n")
	if len(declared) != 1 {
		t.Fatalf("Declared() = %#v, want one gate", declared)
	}
	if declared[0].Name != "soak-reviewed" {
		t.Fatalf("name = %q", declared[0].Name)
	}
	if declared[0].Statement != "the operator has read a week of soak runs and is content" {
		t.Fatalf("statement = %q", declared[0].Statement)
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
	if declared := Declared(prose); len(declared) != 0 {
		t.Fatalf("Declared() = %#v, want nothing inferred from prose", declared)
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
		declared := Declared(line)
		if len(declared) != 1 || declared[0].Name != "soak-reviewed" {
			t.Fatalf("Declared(%q) = %#v", line, declared)
		}
	}
}

// A declaration nobody can read must be reported rather than dropped. A dropped
// one is the original failure over again, with this package as the thing that
// skipped somebody's step.
func TestADeclarationNobodyCanReadIsReportedRatherThanDropped(t *testing.T) {
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

			problems := Problems(testCase.text)
			if len(problems) == 0 {
				t.Fatalf("Problems(%q) reported nothing", testCase.text)
			}
			if !strings.Contains(problems[0].Error(), testCase.want) {
				t.Fatalf("problem = %q, want it to mention %q", problems[0], testCase.want)
			}
		})
	}
}

func TestAWellFormedDeclarationIsNotAProblem(t *testing.T) {
	t.Parallel()

	text := DeclareMarker + " soak-reviewed — the operator has judged the soak\n" +
		DeclareMarker + " release-signed — the operator has signed the release off\n"
	if problems := Problems(text); len(problems) != 0 {
		t.Fatalf("Problems() = %v", problems)
	}
	if declared := Declared(text); len(declared) != 2 {
		t.Fatalf("Declared() = %#v", declared)
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
	gates := Of(item)
	if len(gates) != 1 || gates[0].Name != "soak-reviewed" {
		t.Fatalf("Of() = %#v, want only what the item's author wrote", gates)
	}
}

func TestPendingIsWhatNoRecordedActHasPassed(t *testing.T) {
	t.Parallel()

	gates := Declared(DeclareMarker + " soak-reviewed — the operator has judged the soak\n" +
		DeclareMarker + " release-signed — the operator has signed the release off\n")
	pending := Pending(gates, []string{"release-signed"})
	if len(pending) != 1 || pending[0].Name != "soak-reviewed" {
		t.Fatalf("Pending() = %#v", pending)
	}
	if remaining := Pending(gates, []string{"soak-reviewed", "release-signed"}); len(remaining) != 0 {
		t.Fatalf("Pending() = %#v, want nothing left once both acts are on the record", remaining)
	}
	// A caller that knows of no recorded act holds every gate, which is the
	// direction this fails in on purpose.
	if none := Pending(gates, nil); len(none) != 2 {
		t.Fatalf("Pending() = %#v, want every gate held when nothing is known to be discharged", none)
	}
}

// The sentence every surface prints has one job, and a surface that printed a
// gate without saying that closure does not pass it would be describing a wait.
func TestWhatASurfaceSaysNamesTheActAndRulesOutMachinery(t *testing.T) {
	t.Parallel()

	described := Describe(Declared(DeclareMarker + " soak-reviewed — the operator has judged the soak\n"))
	for _, want := range []string{"waiting on a person", "soak-reviewed", "closing an item", "yoyo gate record"} {
		if !strings.Contains(described, want) {
			t.Fatalf("Describe() = %q, want it to mention %q", described, want)
		}
	}
	if Describe(nil) != "" {
		t.Fatalf("Describe(nil) = %q, want nothing said about no gates", Describe(nil))
	}
}

func TestTheInstructionNamesTheMarkerItTellsAnAuthorToUse(t *testing.T) {
	t.Parallel()

	if !strings.Contains(DeclareInstruction, DeclareMarker) {
		t.Fatalf("DeclareInstruction does not name the marker:\n%s", DeclareInstruction)
	}
}
