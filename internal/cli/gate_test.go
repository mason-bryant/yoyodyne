package cli

import (
	"strings"
	"testing"

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

	stdout, stderr, code := runCLI(t, "gate", "record", "soak-reviewed",
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
	act, recorded, err := store.HumanAct("soak-reviewed")
	if err != nil || !recorded {
		t.Fatalf("HumanAct() = %t, %v", recorded, err)
	}
	if act.Person != "Mason" || !strings.Contains(act.Statement, "diverge nowhere") {
		t.Fatalf("act = %#v", act)
	}

	// A second act is refused rather than overwriting whose signature it was.
	_, stderr, code = runCLI(t, "gate", "record", "soak-reviewed",
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

	_, stderr, code := runCLI(t, "gate", "record", "soak-reviewed", "--by", "Mason", "--config", configPath)
	if code == 0 {
		t.Fatal("an act with no account of what was done was accepted")
	}
	if !strings.Contains(stderr, "--did") {
		t.Fatalf("stderr = %q", stderr)
	}

	_, stderr, code = runCLI(t, "gate", "record", "Soak Reviewed", "--by", "Mason", "--did", "judged it", "--config", configPath)
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

	if _, stderr, code := runCLI(t, "gate", "record", "soak-reviewed",
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
