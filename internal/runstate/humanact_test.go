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
	return actOn("yoyodyne-ifd.209.7", gate, person)
}

func actOn(subject, gate, person string) HumanAct {
	return HumanAct{
		SchemaVersion: HumanActSchemaVersion,
		ProductID:     "yoyodyne",
		Subject:       subject,
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
	if _, recorded, err := store.HumanAct("yoyodyne-ifd.209.7", "soak-reviewed"); err != nil || recorded {
		t.Fatalf("HumanAct() = %t, %v, want no act recorded", recorded, err)
	}
	if passed, err := store.HumanActRecorded("yoyodyne-ifd.209.7", "soak-reviewed"); err != nil || passed {
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
	stored, recorded, err := read.HumanAct("yoyodyne-ifd.209.7", "soak-reviewed")
	if err != nil || !recorded {
		t.Fatalf("HumanAct() = %t, %v", recorded, err)
	}
	if stored.Person != "Mason" || stored.Statement != act.Statement {
		t.Fatalf("stored = %#v", stored)
	}
	if !stored.RecordedAt.Equal(act.RecordedAt) {
		t.Fatalf("recorded at = %s, want %s", stored.RecordedAt, act.RecordedAt)
	}
	discharged, err := read.DischargedGates()
	if err != nil {
		t.Fatalf("DischargedGates() error = %v", err)
	}
	if gates := discharged["yoyodyne-ifd.209.7"]; len(gates) != 1 || gates[0] != "soak-reviewed" {
		t.Fatalf("DischargedGates() = %v", discharged)
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
	stored, _, readErr := store.HumanAct("yoyodyne-ifd.209.7", "soak-reviewed")
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
		{
			name: "nothing it was recorded against",
			act:  actOn("", "soak-reviewed", "Mason"),
			want: "recorded against",
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
	if passed, err := store.HumanActRecorded("yoyodyne-ifd.209.7", "soak-reviewed"); err != nil || passed {
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

// A gate name is a word somebody chose, and the useful words recur. An act
// passes the gate on the thing that declared it, so the same name declared by
// later work is a step somebody still has to take.
//
// Without this the first `release-signed` ever recorded would pass every release
// sign-off afterwards, with nobody having signed anything — and the operator
// could not even record the new act, because the gate would already read as
// passed. That is this mechanism's own failure arriving through the namespace
// rather than through the tracker.
func TestARecordedActPassesItsOwnSubjectAndNothingElse(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	if err := store.RecordHumanAct(actOn("yoyodyne-ifd.300", "release-signed", "Mason")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}

	// The next release declares the same name. Nobody has signed it off.
	passed, err := store.HumanActRecorded("yoyodyne-ifd.400", "release-signed")
	if err != nil {
		t.Fatalf("HumanActRecorded() error = %v", err)
	}
	if passed {
		t.Fatal("an act recorded against one item passed the same gate name on another, so a step nobody took reads as taken")
	}

	// And the operator can record that act, rather than being told the gate is
	// already passed by somebody who passed a different one.
	if err := store.RecordHumanAct(actOn("yoyodyne-ifd.400", "release-signed", "Mason")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v, want the later release's own act to be recordable", err)
	}
	discharged, err := store.DischargedGates()
	if err != nil {
		t.Fatalf("DischargedGates() error = %v", err)
	}
	if len(discharged) != 2 {
		t.Fatalf("DischargedGates() = %v, want each subject's own act", discharged)
	}
	for _, subject := range []string{"yoyodyne-ifd.300", "yoyodyne-ifd.400"} {
		if gates := discharged[subject]; len(gates) != 1 || gates[0] != "release-signed" {
			t.Fatalf("DischargedGates()[%q] = %v", subject, gates)
		}
	}
}

// Two subjects that render alike still get their own record. The file name is a
// rendering plus a digest for exactly this reason: a tracker identifier is not a
// file name, and two acts landing in one file would be one of them passing the
// other's gate.
func TestSubjectsThatRenderAlikeKeepTheirOwnAct(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	if err := store.RecordHumanAct(actOn("yoyodyne-ifd.20.9", "soak-reviewed", "Mason")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}
	if err := store.RecordHumanAct(actOn("yoyodyne-ifd/20-9", "soak-reviewed", "Mason")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v, want a second subject to get its own record", err)
	}
	acts, err := store.HumanActs()
	if err != nil {
		t.Fatalf("HumanActs() error = %v", err)
	}
	if len(acts) != 2 {
		t.Fatalf("HumanActs() = %#v, want one act per subject", acts)
	}
}
