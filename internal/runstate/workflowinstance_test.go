package runstate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// recordedInstance is a well-formed instance for a test to spoil one thing about.
func recordedInstance() WorkflowInstance {
	at := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	return WorkflowInstance{
		SchemaVersion:    WorkflowInstanceSchemaVersion,
		InstanceID:       "delivery",
		WorkflowID:       "delivery",
		DefinitionSchema: 1,
		Digest:           "wf-" + strings.Repeat("a", 64),
		State:            "develop",
		Checkpoints: []WorkflowCheckpoint{
			{Sequence: 0, State: "claim", At: at},
			{Sequence: 1, State: "develop", From: "claim", Outcome: "claimed", At: at.Add(time.Minute)},
		},
	}
}

func instanceStore(t *testing.T) *Store {
	t.Helper()

	store, err := NewStore(t.TempDir(), "fixtures")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

// TestAWorkflowInstanceSurvivesBeingWrittenAndReadBack is the whole of what a
// checkpoint rests on: what a process wrote is what the next process reads.
func TestAWorkflowInstanceSurvivesBeingWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	store := instanceStore(t)
	instance := recordedInstance()
	if err := store.CreateWorkflowInstance(instance); err != nil {
		t.Fatalf("CreateWorkflowInstance() error = %v", err)
	}
	read, err := store.LoadWorkflowInstance(instance.InstanceID)
	if err != nil {
		t.Fatalf("LoadWorkflowInstance() error = %v", err)
	}
	if read.Digest != instance.Digest || read.State != instance.State || read.Done() {
		t.Errorf("LoadWorkflowInstance() = %+v, want the record that was written", read)
	}
	if !slices.Equal(read.Path(), []string{"claim", "develop"}) {
		t.Errorf("Path() = %v, want the boundaries that were written", read.Path())
	}
	if !read.Checkpoints[1].At.Equal(instance.Checkpoints[1].At) {
		t.Errorf("checkpoint 1 was recorded at %s, want %s", read.Checkpoints[1].At, instance.Checkpoints[1].At)
	}
	if last, standing := read.Position(); !standing || last.State != "develop" {
		t.Errorf("Position() = %+v (standing %t), want the state it stands in", last, standing)
	}

	// A record is created once. Two instances under one identifier are two
	// positions in one workflow, and a resume would not say which it meant.
	if err := store.CreateWorkflowInstance(instance); err == nil {
		t.Error("CreateWorkflowInstance() created a second instance under one identifier")
	}

	instance.State = "publish"
	instance.Checkpoints = append(instance.Checkpoints, WorkflowCheckpoint{Sequence: 2, State: "publish", From: "develop", Outcome: "produced", At: time.Now().UTC()})
	if err := store.SaveWorkflowInstance(instance); err != nil {
		t.Fatalf("SaveWorkflowInstance() error = %v", err)
	}
	read, err = store.LoadWorkflowInstance(instance.InstanceID)
	if err != nil {
		t.Fatalf("LoadWorkflowInstance() error = %v", err)
	}
	if read.State != "publish" || len(read.Checkpoints) != 3 {
		t.Errorf("LoadWorkflowInstance() = %+v, want the saved position and its three boundaries", read)
	}

	// An instance sits beside the runs rather than among them, so nothing that
	// walks the runs reads one as a run.
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Errorf("Recorded() = %+v, want no runs; only an instance was written", recorded)
	}
}

// TestAWorkflowInstanceThisBuildDoesNotRecognizeIsRefused. An unknown field is a
// record written by different code, and reading it as though the field were not
// there is resuming an instance whose state was partly ignored.
func TestAWorkflowInstanceThisBuildDoesNotRecognizeIsRefused(t *testing.T) {
	t.Parallel()

	store := instanceStore(t)
	if err := store.CreateWorkflowInstance(recordedInstance()); err != nil {
		t.Fatalf("CreateWorkflowInstance() error = %v", err)
	}
	path := filepath.Join(store.Root(), "delivery.instance.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	spoiled := strings.Replace(string(content), `"instance_id": "delivery"`, `"instance_id": "delivery",`+"\n"+`  "budget": 3`, 1)
	if spoiled == string(content) {
		t.Fatal("the record does not look the way this test assumes")
	}
	if err := os.WriteFile(path, []byte(spoiled), 0o600); err != nil {
		t.Fatalf("write the spoiled record: %v", err)
	}
	if _, err := store.LoadWorkflowInstance("delivery"); err == nil {
		t.Fatal("LoadWorkflowInstance() read a record carrying a field this build does not know")
	}
}

// TestAWorkflowInstanceWhosePositionAndHistoryDisagreeIsRefused. The two are one
// fact written twice, and an instance whose two disagree would resume somewhere
// neither of them says.
func TestAWorkflowInstanceWhosePositionAndHistoryDisagreeIsRefused(t *testing.T) {
	t.Parallel()

	for _, spoil := range []struct {
		name     string
		spoiling func(instance *WorkflowInstance)
		says     string
	}{
		{
			name:     "a position no checkpoint stands in",
			spoiling: func(instance *WorkflowInstance) { instance.State = "publish" },
			says:     "last checkpoint",
		},
		{
			name:     "a history that skips a boundary",
			spoiling: func(instance *WorkflowInstance) { instance.Checkpoints[1].From = "publish" },
			says:     "skips a boundary",
		},
		{
			name:     "checkpoints out of order",
			spoiling: func(instance *WorkflowInstance) { instance.Checkpoints[1].Sequence = 7 },
			says:     "numbered",
		},
		{
			name:     "an initial checkpoint something transitioned into",
			spoiling: func(instance *WorkflowInstance) { instance.Checkpoints[0].From = "nowhere" },
			says:     "nothing transitioned into it",
		},
		{
			name:     "no checkpoints at all",
			spoiling: func(instance *WorkflowInstance) { instance.Checkpoints = nil },
			says:     "no checkpoints",
		},
		{
			name:     "a pin that is not a digest",
			spoiling: func(instance *WorkflowInstance) { instance.Digest = "delivery-as-of-tuesday" },
			says:     "not a workflow digest",
		},
		{
			name:     "an identifier that is not one",
			spoiling: func(instance *WorkflowInstance) { instance.InstanceID = "../delivery" },
			says:     "workflow instance id",
		},
		{
			name:     "a record schema this build does not write",
			spoiling: func(instance *WorkflowInstance) { instance.SchemaVersion = 2 },
			says:     "schema version 2 is not supported",
		},
		{
			name:     "no definition schema at all",
			spoiling: func(instance *WorkflowInstance) { instance.DefinitionSchema = 0 },
			says:     "schema version 0",
		},
	} {
		t.Run(spoil.name, func(t *testing.T) {
			t.Parallel()

			instance := recordedInstance()
			spoil.spoiling(&instance)
			err := instance.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %s", spoil.name)
			}
			if !strings.Contains(err.Error(), spoil.says) {
				t.Errorf("Validate() error = %v, want it to say %q", err, spoil.says)
			}
			if err := instanceStore(t).CreateWorkflowInstance(instance); err == nil {
				t.Fatalf("CreateWorkflowInstance() wrote a record with %s", spoil.name)
			}
		})
	}
}

// TestAnInstanceWithNoRoomLeftSaysSoBeforeTheStepThatWouldFill it. A record is
// bounded and a history only grows, so the question has to be answerable before
// the boundary is produced rather than when it is being written.
func TestAnInstanceWithNoRoomLeftSaysSoBeforeTheStepThatWouldFill(t *testing.T) {
	t.Parallel()

	instance := recordedInstance()
	next := func() WorkflowCheckpoint {
		return WorkflowCheckpoint{
			Sequence: len(instance.Checkpoints),
			State:    "develop",
			From:     "develop",
			Outcome:  "changes-requested",
			At:       time.Now().UTC(),
		}
	}
	// How many boundaries fit is worked out rather than discovered one at a time:
	// the record is most of a megabyte by the end, and encoding it once per
	// checkpoint to find that out would encode a great deal more than that.
	measure := func(measured WorkflowInstance) int {
		encoded, err := encodeRecord("workflow instance", measured)
		if err != nil {
			t.Fatalf("encodeRecord() error = %v", err)
		}
		return len(encoded)
	}
	grown := instance
	grown.Checkpoints = append(slices.Clone(instance.Checkpoints), next())
	// The per-checkpoint cost is rounded up, so the estimate stops short of the
	// limit rather than over it and the loop below finishes the job exactly.
	each := measure(grown) - measure(instance) + 8
	for fits := (maxEncodedStateBytes - measure(instance)) / each; fits > 0; fits-- {
		instance.Checkpoints = append(instance.Checkpoints, next())
	}
	for instance.RoomForAnotherCheckpoint(next()) == nil {
		instance.Checkpoints = append(instance.Checkpoints, next())
	}
	if len(instance.Checkpoints) < 100 {
		t.Fatalf("the record filled after %d checkpoints, which is not the bound this test is about", len(instance.Checkpoints))
	}

	store := instanceStore(t)
	if err := store.CreateWorkflowInstance(instance); err != nil {
		t.Fatalf("CreateWorkflowInstance() refused a record that still fits: %v", err)
	}
	err := instance.RoomForAnotherCheckpoint(next())
	if err == nil {
		t.Fatal("RoomForAnotherCheckpoint() found room in a record that is full")
	}
	if !strings.Contains(err.Error(), "cannot be stepped any further") {
		t.Errorf("RoomForAnotherCheckpoint() error = %v, want it to say the instance can go no further", err)
	}

	// And the write it was asked ahead of is the one that would have refused.
	instance.Checkpoints = append(instance.Checkpoints, next())
	instance.State = instance.Checkpoints[len(instance.Checkpoints)-1].State
	if err := store.SaveWorkflowInstance(instance); err == nil {
		t.Error("SaveWorkflowInstance() wrote a record over the limit it is held to")
	}
}
