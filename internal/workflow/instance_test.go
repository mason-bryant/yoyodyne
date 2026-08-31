package workflow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// recorded is a well-formed instance for a test to spoil one thing about.
func recorded() Instance {
	at := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	return Instance{
		ID:         "delivery",
		WorkflowID: "delivery",
		Schema:     SchemaVersion,
		Digest:     DigestPrefix + strings.Repeat("a", 64),
		State:      "develop",
		Checkpoints: []Checkpoint{
			{Sequence: 0, State: "claim", At: at},
			{Sequence: 1, State: "develop", From: "claim", Outcome: "claimed", At: at.Add(time.Minute)},
		},
	}
}

func instanceStore(t *testing.T) *InstanceStore {
	t.Helper()

	store, err := NewInstanceStore(filepath.Join(t.TempDir(), "instances"))
	if err != nil {
		t.Fatalf("NewInstanceStore() error = %v", err)
	}
	return store
}

// TestARecordSurvivesBeingWrittenAndReadBack is the whole of what a checkpoint
// rests on: what a process wrote is what the next process reads.
func TestARecordSurvivesBeingWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	store := instanceStore(t)
	instance := recorded()
	if err := store.Create(instance); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	read, err := store.Load(instance.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if read.Digest != instance.Digest || read.State != instance.State || read.Terminal {
		t.Errorf("Load() = %+v, want the record that was written", read)
	}
	if !slices.Equal(read.Path(), []string{"claim", "develop"}) {
		t.Errorf("Path() = %v, want the boundaries that were written", read.Path())
	}
	if !read.Checkpoints[1].At.Equal(instance.Checkpoints[1].At) {
		t.Errorf("checkpoint 1 was recorded at %s, want %s", read.Checkpoints[1].At, instance.Checkpoints[1].At)
	}

	instance.State = "publish"
	instance.Checkpoints = append(instance.Checkpoints, Checkpoint{Sequence: 2, State: "publish", From: "develop", Outcome: "produced", At: time.Now().UTC()})
	if err := store.Save(instance); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	read, err = store.Load(instance.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if read.State != "publish" || len(read.Checkpoints) != 3 {
		t.Errorf("Load() = %+v, want the saved position and its three boundaries", read)
	}
}

// TestSavingAnInstanceNothingCreatedIsRefused. Save replaces a record, and a
// replacement of something that was never there is a record with no beginning.
func TestSavingAnInstanceNothingCreatedIsRefused(t *testing.T) {
	t.Parallel()

	if err := instanceStore(t).Save(recorded()); err == nil {
		t.Fatal("Save() wrote a record nothing created")
	}
}

// TestARecordThisBuildDoesNotRecognizeIsRefused. An unknown field is a record
// written by different code, and reading it as though the field were not there
// is resuming an instance whose state was partly ignored.
func TestARecordThisBuildDoesNotRecognizeIsRefused(t *testing.T) {
	t.Parallel()

	store := instanceStore(t)
	if err := store.Create(recorded()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := filepath.Join(store.Root(), "delivery.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	spoiled := strings.Replace(string(content), `"id": "delivery"`, `"id": "delivery",`+"\n"+`  "budget": 3`, 1)
	if spoiled == string(content) {
		t.Fatal("the record does not look the way this test assumes")
	}
	if err := os.WriteFile(path, []byte(spoiled), 0o600); err != nil {
		t.Fatalf("write the spoiled record: %v", err)
	}
	if _, err := store.Load("delivery"); err == nil {
		t.Fatal("Load() read a record carrying a field this build does not know")
	}
}

// TestARecordWhosePositionAndHistoryDisagreeIsRefused. The two are one fact
// written twice, and an instance whose two disagree would resume somewhere
// neither of them says.
func TestARecordWhosePositionAndHistoryDisagreeIsRefused(t *testing.T) {
	t.Parallel()

	for _, spoil := range []struct {
		name     string
		spoiling func(instance *Instance)
		says     string
	}{
		{
			name:     "a position no checkpoint stands in",
			spoiling: func(instance *Instance) { instance.State = "publish" },
			says:     "last checkpoint",
		},
		{
			name:     "a history that skips a boundary",
			spoiling: func(instance *Instance) { instance.Checkpoints[1].From = "publish" },
			says:     "skips a boundary",
		},
		{
			name:     "checkpoints out of order",
			spoiling: func(instance *Instance) { instance.Checkpoints[1].Sequence = 7 },
			says:     "numbered",
		},
		{
			name:     "an initial checkpoint something transitioned into",
			spoiling: func(instance *Instance) { instance.Checkpoints[0].From = "nowhere" },
			says:     "nothing transitioned into it",
		},
		{
			name:     "no checkpoints at all",
			spoiling: func(instance *Instance) { instance.Checkpoints = nil },
			says:     "no checkpoints",
		},
		{
			name:     "a pin that is not a digest",
			spoiling: func(instance *Instance) { instance.Digest = "delivery-as-of-tuesday" },
			says:     "not a workflow digest",
		},
		{
			name:     "an identifier that is not one",
			spoiling: func(instance *Instance) { instance.ID = "../delivery" },
			says:     "workflow instance id",
		},
	} {
		t.Run(spoil.name, func(t *testing.T) {
			t.Parallel()

			instance := recorded()
			spoil.spoiling(&instance)
			err := instance.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %s", spoil.name)
			}
			if !strings.Contains(err.Error(), spoil.says) {
				t.Errorf("Validate() error = %v, want it to say %q", err, spoil.says)
			}
			if err := instanceStore(t).Create(instance); err == nil {
				t.Fatalf("Create() wrote a record with %s", spoil.name)
			}
		})
	}
}

// TestAStoreRootedNowhereIsRefused. A store rooted at a relative path is rooted
// wherever a process happened to be standing when it was built.
func TestAStoreRootedNowhereIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := NewInstanceStore("instances"); err == nil {
		t.Fatal("NewInstanceStore() accepted a relative root")
	}
	if _, err := instanceStore(t).Load("delivery"); err == nil {
		t.Fatal("Load() read an instance nothing recorded")
	}
}
