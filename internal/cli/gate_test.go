package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The command is the act. A gate is passed by a person naming themselves and
// saying what they did, and by nothing else — so this is what the queue and the
// executor read before anything proceeds.
func TestGateRecordIsTheOnlyThingThatPassesAGate(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "gate", "record", "soak-reviewed", "--for", "yoyodyne-ifd.209.7",
		"--by", "Mason", "--did", "read a week of soak runs; they diverge nowhere", "--config", configPath)
	if code != 0 {
		t.Fatalf("gate record code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"Mason", "soak-reviewed", "not replaceable"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to mention %q", stdout, want)
		}
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	act, recorded, err := store.HumanAct("yoyodyne-ifd.209.7", "soak-reviewed")
	if err != nil || !recorded {
		t.Fatalf("HumanAct() = %t, %v", recorded, err)
	}
	if act.Person != "Mason" || !strings.Contains(act.Statement, "diverge nowhere") {
		t.Fatalf("act = %#v", act)
	}

	// A second act is refused rather than overwriting whose signature it was.
	_, stderr, code = runCLI(t, "gate", "record", "soak-reviewed", "--for", "yoyodyne-ifd.209.7",
		"--by", "Somebody else", "--did", "signed it off too", "--config", configPath)
	if code == 0 {
		t.Fatal("a second act was accepted, so the record no longer says whose it was")
	}
	if !strings.Contains(stderr, "Mason") {
		t.Fatalf("stderr = %q, want the refusal to name who passed it", stderr)
	}
}

// An act with nobody's name on it, or with no account of what was done, is a
// flag — which is what was already there and what did not hold.
func TestGateRecordRefusesAnActNobodyDescribed(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	_, stderr, code := runCLI(t, "gate", "record", "soak-reviewed", "--for", "yoyodyne-ifd.209.7", "--by", "Mason", "--config", configPath)
	if code == 0 {
		t.Fatal("an act with no account of what was done was accepted")
	}
	if !strings.Contains(stderr, "--did") {
		t.Fatalf("stderr = %q", stderr)
	}

	_, stderr, code = runCLI(t, "gate", "record", "Soak Reviewed", "--for", "yoyodyne-ifd.209.7", "--by", "Mason", "--did", "judged it", "--config", configPath)
	if code == 0 {
		t.Fatal("a gate name nothing could be filed under was accepted")
	}
	if !strings.Contains(stderr, "not a gate name") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// The listing says what a person still has to do and how to record it, and it
// says what they have already done. Both halves are the point: a list of only
// outstanding gates loses the operator's own signatures.
func TestGateListSaysWhatIsWaitingAndWhatWasDone(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	if _, stderr, code := runCLI(t, "gate", "record", "soak-reviewed", "--for", "yoyodyne-ifd.209.7",
		"--by", "Mason", "--did", "read a week of soak runs", "--config", configPath); code != 0 {
		t.Fatalf("gate record code = %d, stderr = %q", code, stderr)
	}
	stdout, stderr, code := runCLI(t, "gate", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("gate list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"soak-reviewed", "passed by Mason", "read a week of soak runs"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to mention %q", stdout, want)
		}
	}
	// There is no tracker in this fixture, so the declarations could not be read.
	// The listing says so rather than reporting the acts as the whole answer: a
	// gate somebody has yet to record is exactly what would be missing.
	if !strings.Contains(stdout, "could not be listed") {
		t.Fatalf("stdout = %q, want the unread declarations stated", stdout)
	}
}

// The listing is where the author of a declaration nothing could read finds out
// why their item is held. Dropping it would show every gate but the one somebody
// has to fix.
func TestGateListNamesADeclarationNothingCouldRead(t *testing.T) {
	t.Parallel()

	queue := backlog.Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.209.7", Status: "open",
			Description: humangate.DeclareMarker + " soak-reviewed — the operator has judged the soak\n",
		},
		{
			ID: "yoyodyne-ifd.209.8", Status: "open",
			Description: humangate.DeclareMarker + " Soak Reviewed — the operator has judged the soak\n",
		},
	}, []string{"yoyodyne-ifd.209.7", "yoyodyne-ifd.209.8"}, backlog.ReadHolds(nil), nil)
	entries, unreadable := gateListing(queue, nil)

	if len(entries) != 1 || entries[0].Subject != "yoyodyne-ifd.209.7" || entries[0].Name != "soak-reviewed" || entries[0].Act != nil {
		t.Fatalf("entries = %#v", entries)
	}
	if len(unreadable) != 1 {
		t.Fatalf("unreadable = %#v", unreadable)
	}
	if unreadable[0].WorkItemID != "yoyodyne-ifd.209.8" {
		t.Fatalf("unreadable names %q", unreadable[0].WorkItemID)
	}
	if !strings.Contains(unreadable[0].Problem, "not a gate name") {
		t.Fatalf("problem = %q", unreadable[0].Problem)
	}
}

// A gate somebody has passed is shown as passed and not as waiting, and it is
// shown on the subject the act names and nowhere else: the same name on another
// item is still a step somebody has to take.
func TestGateListShowsAPassedGateOnItsOwnSubjectOnly(t *testing.T) {
	t.Parallel()

	declare := humangate.DeclareMarker + " release-signed — the operator has signed this release off\n"
	items := []beads.WorkItem{
		{ID: "yoyodyne-ifd.300", Status: "open", Description: declare},
		{ID: "yoyodyne-ifd.400", Status: "open", Description: declare},
	}
	act := runstate.HumanAct{Subject: "yoyodyne-ifd.300", Gate: "release-signed", Person: "Mason", Statement: "signed v0.3.0 off"}
	// The queue the read model would assemble: the recorded act is already
	// subtracted from the item it was recorded against.
	queue := backlog.Order(items, []string{"yoyodyne-ifd.300", "yoyodyne-ifd.400"}, backlog.ReadHolds(nil),
		map[string][]string{"yoyodyne-ifd.300": {"release-signed"}})
	entries, unreadable := gateListing(queue, []runstate.HumanAct{act})

	if len(unreadable) != 0 || len(entries) != 2 {
		t.Fatalf("entries = %#v, unreadable = %#v", entries, unreadable)
	}
	if passed := entries[0]; passed.Subject != "yoyodyne-ifd.300" || passed.Act == nil || passed.Act.Person != "Mason" {
		t.Fatalf("the signed release reads as %#v, want it shown as passed by Mason", passed)
	}
	if waiting := entries[1]; waiting.Subject != "yoyodyne-ifd.400" || waiting.Act != nil {
		t.Fatalf("the next release reads as %#v, want it still waiting on a person", waiting)
	}
}

// The listing's outstanding half is a projection of the read model's queue
// rather than an assembly of its own. It used to read the tracker itself over a
// status set wider than the queue's, which meant an item could be reported as
// waiting on a person here and by nothing else — two operator surfaces giving
// different answers to one question, which is what a single derivation exists to
// prevent — and narrowing the set left the two agreeing only for as long as
// nobody changed one of them.
func TestGateListProjectsTheReadModelsQueue(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("gate.go")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(source), "readmodel.Queue(") {
		t.Fatal("the gate listing does not read the read model's queue, so it can report a hold the status does not")
	}
	for _, own := range []string{"tracker.List(", `"in_progress"`, `"open"`, `"blocked"`, "AdmittedStatuses"} {
		if strings.Contains(string(source), own) {
			t.Fatalf("the gate listing reads the tracker for itself (%s), so it and the queue can drift apart", own)
		}
	}
}
