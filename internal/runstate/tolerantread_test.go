package runstate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// addAFieldThisBuildDoesNotKnow rewrites a record on disk with one extra
// top-level key, which is what a landing on a newer build does to every record
// written after it. The record is rewritten from what the store itself wrote
// rather than assembled here, so this keeps saying the same thing as the schema
// moves under it.
func addAFieldThisBuildDoesNotKnow(t *testing.T, path, field string) {
	t.Helper()

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, withExtraField(t, encoded, field), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}

func withExtraField(t *testing.T, encoded []byte, field string) []byte {
	t.Helper()

	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatalf("read the record back as JSON: %v", err)
	}
	carried[field] = json.RawMessage(`["a value a newer build wrote"]`)
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatalf("rewrite the record: %v", err)
	}
	return rewritten
}

// captureUnknownFieldNotices sends the notices somewhere a test can read them
// and forgets what has already been said, so one test's notices are not another
// test's silence. It is deliberately not parallel-safe: the notices are
// process-wide because the processes that read them open a fresh store per pass,
// and a test that reads them has to be the only one doing so.
func captureUnknownFieldNotices(t *testing.T) *bytes.Buffer {
	t.Helper()

	said := &bytes.Buffer{}
	unknownFieldNotices.Lock()
	previousWriter, previousSaid := unknownFieldNotices.to, unknownFieldNotices.said
	unknownFieldNotices.to, unknownFieldNotices.said = said, map[string]bool{}
	unknownFieldNotices.Unlock()
	t.Cleanup(func() {
		unknownFieldNotices.Lock()
		unknownFieldNotices.to, unknownFieldNotices.said = previousWriter, previousSaid
		unknownFieldNotices.Unlock()
	})
	return said
}

// A run record carrying a field this build does not know is read by the listings
// and refused by the loader, which is the whole of the split: the dashboard and
// the Slack sink go through the listings and keep reporting across a rebuild,
// and whoever is about to save the record back is stopped rather than quietly
// dropping what the newer build recorded.
func TestARunRecordWithANewFieldIsListedAndRefusedByTheLoader(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.WorkItemID = "yoyodyne-test"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")

	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v, want the record read without the field it does not know", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("Recorded() = %d runs, want the one that was written", len(recorded))
	}
	if recorded[0].RunID != state.RunID || recorded[0].WorkItemID != state.WorkItemID || recorded[0].Status != state.Status {
		t.Fatalf("listed run = %#v, want the fields this build knows kept", recorded[0])
	}
	if !strings.Contains(said.String(), "a_field_a_newer_build_added") {
		t.Fatalf("notices = %q, want the field it stepped over named", said.String())
	}

	// The loader is the door a caller goes through before it saves, and it refuses
	// rather than handing back a record it would write out short.
	if _, err := store.Load(state.RunID); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Load() error = %v, want the unknown field refused and named", err)
	}
}

// The notice is worth one line and not one line a pass. Every reader that hit
// this opens a fresh store for every pass it makes, so the record of what has
// been said cannot live on the store.
func TestTheNoticeAboutAnUnknownFieldIsSaidOnceAndNotOncePerPass(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")

	for pass := 0; pass < 5; pass++ {
		// A fresh store each pass, which is what the dashboard does: it opens one
		// per request, so a record of what has already been said that lived on the
		// store would say it five times.
		perPass, err := NewStore(root, "yoyodyne")
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		if _, err := perPass.Recorded(); err != nil {
			t.Fatalf("Recorded() on pass %d error = %v", pass, err)
		}
	}
	if lines := strings.Count(strings.TrimSpace(said.String()), "\n") + 1; lines != 1 {
		t.Fatalf("notices = %q, want one line across five passes", said.String())
	}
}

// A field added inside a record rather than at its root is named with the path
// it sits at, because "stale_block_clear" and "summary.stale_block_clear" are
// different things to go and look at.
func TestAnUnknownFieldIsNamedWithThePathItSitsAt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusFailed)
	state.StaleBlockClear = &StaleBlockClear{Outcome: domain.StaleBlockClearConfirmed, Reads: 2, Status: "blocked"}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatalf("read the record back as JSON: %v", err)
	}
	carried["stale_block_clear"] = withExtraField(t, carried["stale_block_clear"], "how_late")
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatalf("rewrite the record: %v", err)
	}

	var read State
	unknown, err := decodeTolerating(rewritten, &read)
	if err != nil {
		t.Fatalf("decodeTolerating() error = %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "stale_block_clear.how_late" {
		t.Fatalf("unknown fields = %v, want the nested field named with its path", unknown)
	}
	if read.StaleBlockClear == nil || read.StaleBlockClear.Outcome != domain.StaleBlockClearConfirmed {
		t.Fatalf("read clear = %#v, want the rest of the nested record kept", read.StaleBlockClear)
	}
}

// A record that is malformed, or that says the wrong sort of thing, is still
// refused by the tolerant door. Stepping over a field this build does not know
// is the one thing it steps over.
func TestTheTolerantDoorStillRefusesARecordThatIsWrongRatherThanNew(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		encoded string
	}{
		{name: "malformed", encoded: `{"run_id": `},
		{name: "wrong type", encoded: `{"run_id": 7, "unknown_field": 1}`},
		{name: "two values", encoded: `{"unknown_field": 1}{"unknown_field": 2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var read State
			if _, err := decodeTolerating([]byte(tc.encoded), &read); err == nil {
				t.Fatalf("decodeTolerating(%q) error = nil, want the record refused", tc.encoded)
			}
		})
	}
}

// The conversation records take the same split: the listing every surface reads
// keeps a record a newer build wrote, and the agent about to resume its own
// conversation and write it back at the end of the turn is refused.
func TestAConversationWithANewFieldIsListedAndRefusedByTheResumingAgent(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	conversation.Agent = "product-manager"
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	identity := conversation.Identity()
	path, err := store.statePathFor(identity)
	if err != nil {
		t.Fatalf("statePathFor() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")

	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v, want the conversation read without the field it does not know", err)
	}
	if len(recorded) != 1 || recorded[0].ConversationID != conversation.ConversationID {
		t.Fatalf("Recorded() = %#v, want the recorded conversation kept", recorded)
	}
	if !strings.Contains(said.String(), "a_field_a_newer_build_added") {
		t.Fatalf("notices = %q, want the field it stepped over named", said.String())
	}

	if _, err := store.Load(identity); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Load() error = %v, want the unknown field refused and named", err)
	}
}

// A conversation holder is read to decide whether somebody is mid-turn, so it
// stays on the strict door: the refusal reaches the caller rather than an answer
// about who is talking to the agent being invented from a record this build can
// only read part of.
func TestAConversationHolderWithANewFieldIsRefusedRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := store.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	path, err := store.holderFile(identity)
	if err != nil {
		t.Fatalf("holderFile() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")
	if _, err := store.InFlight(identity); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("InFlight() error = %v, want the unknown field refused and named", err)
	}
	hold.Release()
}

// The recurring-task log is only ever listed, so a line a newer build wrote is
// read without the field this build does not know rather than set aside as
// unreadable — which is what would have hidden every pass recorded after a
// landing from the only surface they are read from.
func TestARecurringTaskRecordWithANewFieldIsStillListed(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	store := newSweepStore(t)
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	recorded := Sweep{
		SchemaVersion: SweepSchemaVersion,
		ProductID:     "example",
		Task:          "a-sweep",
		Role:          domain.RoleProductManager,
		StartedAt:     at,
		EndedAt:       at.Add(time.Minute),
		Turns:         1,
		Problem:       "the pass spent a turn and produced no account",
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnowToEachLine(t, store.Path(), "a_field_a_newer_build_added")

	listed, unreadable, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(unreadable) != 0 {
		t.Fatalf("unreadable = %#v, want a record a newer build wrote read rather than set aside", unreadable)
	}
	if len(listed) != 1 || listed[0].Task != recorded.Task || listed[0].Turns != recorded.Turns {
		t.Fatalf("List() = %#v, want the fields this build knows kept", listed)
	}
	if !strings.Contains(said.String(), "a_field_a_newer_build_added") {
		t.Fatalf("notices = %q, want the field it stepped over named", said.String())
	}
}

// A recurring task's claim is read to decide whether the task is due and then
// written back with the firing counted on it, so it stays on the strict door and
// the cadence stops visibly rather than losing what a newer build recorded.
func TestARecurringTaskClaimWithANewFieldIsRefused(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, at); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, store.path("a-sweep"), "a_field_a_newer_build_added")
	_, err := store.Claim(context.Background(), "a-sweep", time.Hour, at.Add(2*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Claim() error = %v, want the unknown field refused and named", err)
	}
}

// A workflow instance is only ever read to be advanced and saved back, so every
// read of it precedes a write and there is no tolerant door beside the strict
// one. The refusal names the record, which is what keeps it from being mistaken
// for an instance with nothing in it.
func TestAWorkflowInstanceWithANewFieldIsRefused(t *testing.T) {
	t.Parallel()

	store := instanceStore(t)
	instance := recordedInstance()
	if err := store.CreateWorkflowInstance(instance); err != nil {
		t.Fatalf("CreateWorkflowInstance() error = %v", err)
	}
	path, err := store.workflowInstancePath(instance.InstanceID)
	if err != nil {
		t.Fatalf("workflowInstancePath() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")
	if _, err := store.LoadWorkflowInstance(instance.InstanceID); err == nil ||
		!strings.Contains(err.Error(), "a_field_a_newer_build_added") ||
		!strings.Contains(err.Error(), instance.InstanceID) {
		t.Fatalf("LoadWorkflowInstance() error = %v, want the unknown field refused and the record named", err)
	}
}

// addAFieldThisBuildDoesNotKnowToEachLine is the same rewrite for a log that
// holds one record a line.
func addAFieldThisBuildDoesNotKnowToEachLine(t *testing.T, path, field string) {
	t.Helper()

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rewritten := &bytes.Buffer{}
	for _, line := range strings.Split(strings.TrimRight(string(encoded), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rewritten.Write(withExtraField(t, []byte(line), field))
		rewritten.WriteByte('\n')
	}
	if err := os.WriteFile(path, rewritten.Bytes(), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}
