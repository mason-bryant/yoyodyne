package runstate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recordedAct(gate, person string) HumanAct {
	return HumanAct{
		SchemaVersion: HumanActSchemaVersion,
		ProductID:     "yoyodyne",
		Gate:          gate,
		Person:        person,
		Statement:     "read a week of soak runs; the declarative and legacy paths diverge nowhere",
		RecordedAt:    time.Date(2026, 9, 4, 17, 30, 0, 0, time.UTC),
	}
}

func TestARecordedActSurvivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStoreAt(t, root)
	if _, recorded, err := store.HumanAct("soak-reviewed"); err != nil || recorded {
		t.Fatalf("HumanAct() = %t, %v, want no act recorded", recorded, err)
	}
	if passed, err := store.HumanActRecorded("soak-reviewed"); err != nil || passed {
		t.Fatalf("HumanActRecorded() = %t, %v, want the gate still holding", passed, err)
	}

	act := recordedAct("soak-reviewed", "Mason")
	if err := store.RecordHumanAct(act); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}
	// Read back by a second store over the same root, which is what a later
	// process is: the gate is passed for everything that reads it, not only for
	// the process that wrote it.
	read := newStoreAt(t, root)
	stored, recorded, err := read.HumanAct("soak-reviewed")
	if err != nil || !recorded {
		t.Fatalf("HumanAct() = %t, %v", recorded, err)
	}
	if stored.Person != "Mason" || stored.Statement != act.Statement {
		t.Fatalf("stored = %#v", stored)
	}
	if !stored.RecordedAt.Equal(act.RecordedAt) {
		t.Fatalf("recorded at = %s, want %s", stored.RecordedAt, act.RecordedAt)
	}
	if discharged, err := read.DischargedGates(); err != nil || len(discharged) != 1 || discharged[0] != "soak-reviewed" {
		t.Fatalf("DischargedGates() = %v, %v", discharged, err)
	}
}

// The record is who passed the gate. A second write would afterwards report it
// as having been passed by whoever typed most recently, which is the one thing
// this record exists to say.
func TestAGateAlreadyPassedIsRefusedRatherThanOverwritten(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	if err := store.RecordHumanAct(recordedAct("soak-reviewed", "Mason")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}
	err := store.RecordHumanAct(recordedAct("soak-reviewed", "Somebody else"))
	if err == nil {
		t.Fatal("a second act replaced the first, so the record no longer says whose it was")
	}
	if !strings.Contains(err.Error(), "Mason") {
		t.Fatalf("refusal = %v, want it to name who actually passed the gate", err)
	}
	stored, _, readErr := store.HumanAct("soak-reviewed")
	if readErr != nil || stored.Person != "Mason" {
		t.Fatalf("stored = %#v, %v", stored, readErr)
	}
}

func TestAnActThatSaysNothingOrNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	for _, testCase := range []struct {
		name string
		act  HumanAct
		want string
	}{
		{
			name: "nobody took it",
			act:  func() HumanAct { act := recordedAct("soak-reviewed", ""); return act }(),
			want: "names nobody",
		},
		{
			name: "nothing said about it",
			act: func() HumanAct {
				act := recordedAct("soak-reviewed", "Mason")
				act.Statement = "  "
				return act
			}(),
			want: "says nothing about what was done",
		},
		{
			name: "a gate name nothing could be filed under",
			act:  recordedAct("../elsewhere", "Mason"),
			want: "not a gate name",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := store.RecordHumanAct(testCase.act)
			if err == nil {
				t.Fatalf("RecordHumanAct(%#v) was accepted", testCase.act)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("refusal = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// Nothing about a run reaches this record. The store offers no verb that
// derives one from a run's state, a work item, or anything else it keeps, which
// is what makes "only a person passes a gate" a property of the code rather
// than a rule somebody has to remember.
func TestNothingAboutARunEverPassesAGate(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The run is completed, which is the closest thing the harness has to the
	// item-closed dependency that used to stand in for a person's step.
	state.Status = StatusSucceeded
	state.CompletedAt = &state.StartedAt
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if passed, err := store.HumanActRecorded("soak-reviewed"); err != nil || passed {
		t.Fatalf("HumanActRecorded() = %t, %v, want a finished run to have passed nothing", passed, err)
	}
	if discharged, err := store.DischargedGates(); err != nil || len(discharged) != 0 {
		t.Fatalf("DischargedGates() = %v, %v, want nothing passed by a run", discharged, err)
	}
}

// recordingPackages are the packages allowed to write a human act: this one,
// which holds the record, and the command line, which is where a person types.
// Anything else is a second door into a record whose whole value is that there
// is one.
var recordingPackages = map[string]bool{
	"internal/runstate": true,
	"internal/cli":      true,
}

// The rule this package's comment states, held mechanically rather than by
// anybody remembering it.
//
// A gate is satisfiable only by a recorded human act, and that sentence is worth
// exactly as much as the number of things that can write the record. A pipeline
// step, a registered action, or a work-item closure that could write one would
// pass a person's step on their behalf, which is the failure gates exist to end
// — and it would do it without anything in the diff looking wrong.
func TestOnlyTheCommandLineWritesARecordedHumanAct(t *testing.T) {
	t.Parallel()

	root := "../.."
	var writers []string
	// Counted so a walk that found nothing fails rather than passing: a scan
	// looking in the wrong place reports no second door exactly as a repository
	// with none does.
	found := 0
	err := filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		content, err := os.ReadFile(candidate)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), "RecordHumanAct(") {
			return nil
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return err
		}
		found++
		if recordingPackages[filepath.ToSlash(filepath.Dir(relative))] {
			return nil
		}
		writers = append(writers, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	if found < len(recordingPackages) {
		t.Fatalf("the walk found %d file(s) writing a human act, and the record has %d doors; it is looking in the wrong place",
			found, len(recordingPackages))
	}
	for _, writer := range writers {
		t.Errorf("%s writes a human act; a gate is passed by a person at the command line and by nothing else", writer)
	}
}
