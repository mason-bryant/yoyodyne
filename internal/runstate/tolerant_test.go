package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failure this exists for: a field added to run state by a build newer than
// the reader's, which is what `work_item_title` was to the sink that starved on
// it overnight and what the next optional key will be to the sink after that. A
// process that acts on the record still refuses it, because a key it does not
// understand is a key it would act without; a process that only reads it takes
// what it understands and carries on.
func TestAReaderTakesARunRecordANewerBuildWroteAndAnActorStillRefusesIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	runID := writeRunRecord(t, store, "run-22ce30530a5cd7ac66414f2095dcede1", `"work_item_summary": "a key no build here has heard of yet",`)

	if _, err := store.Load(runID); err == nil {
		t.Fatal("Load() error = nil, want a strict reader to refuse a key it does not understand")
	}

	states, err := store.Tolerant(nil).Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v, want the unknown key read past", err)
	}
	if len(states) != 1 || states[0].RunID != runID {
		t.Fatalf("Recorded() = %#v, want the run itself", states)
	}
	if states[0].WorkItemID != "yoyodyne-ifd.2.5" {
		t.Fatalf("work item = %q, want everything this build does understand kept", states[0].WorkItemID)
	}
}

// A record that will not decode at all must not hold up every record beside it:
// that is the same outage in slower motion. It is skipped, said out loud, and
// the rest of the product is still listed.
func TestAReaderSkipsARecordItCannotDecodeAndSaysWhichOne(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	readable := testState(t, StatusRunning)
	if err := store.Create(readable); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A schema version from a build that broke compatibility deliberately, which
	// is what the version is for and what tolerance must not paper over.
	unreadable := writeRunRecord(t, store, "run-4c1d9f2a5b6e70318294adcb5e7f0d11", `"schema_version": 99,`)

	var skipped []string
	states, err := store.Tolerant(func(record string, err error) {
		skipped = append(skipped, record)
	}).Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v, want the undecodable record read past", err)
	}
	if len(states) != 1 || states[0].RunID != readable.RunID {
		t.Fatalf("Recorded() = %#v, want the record that reads and nothing else", states)
	}
	if len(skipped) != 1 || skipped[0] != unreadable {
		t.Fatalf("skipped = %v, want the record named so the gap is not a silent one", skipped)
	}

	// The strict reading is unchanged: an actor that cannot read one record has
	// not got a listing it can act on.
	if _, err := store.Recorded(); err == nil {
		t.Fatal("Recorded() error = nil, want a strict reader to fail on a record it cannot decode")
	}
}

// Conversations are read the same way and for the same reason: the sink lists
// them to notice the backlog moving, and one record a newer build wrote must not
// stop it noticing anything.
func TestAReaderTakesConversationRecordsANewerBuildWrote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	unreadable := filepath.Join(store.Root(), "architect.json")
	if err := os.WriteFile(unreadable, []byte(`{"schema_version": 99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	newer := filepath.Join(store.Root(), "development-manager.json")
	document := `{
  "schema_version": 1,
  "conversation_id": "chat-0123456789abcdef0123456789abcdef",
  "product_id": "yoyodyne",
  "repository_id": "yoyodyne",
  "agent": "development-manager",
  "role": "development-manager",
  "backend": "claude-code",
  "started_at": "2026-08-19T10:00:00Z",
  "updated_at": "2026-08-19T10:00:00Z",
  "a_key_this_build_has_never_heard_of": true
}
`
	if err := os.WriteFile(newer, []byte(document), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var skipped []string
	recorded, err := store.Tolerant(func(record string, err error) {
		skipped = append(skipped, record)
	}).Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v, want the newer record read and the broken one read past", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("Recorded() = %#v, want both records that decode", recorded)
	}
	if len(skipped) != 1 || skipped[0] != "architect.json" {
		t.Fatalf("skipped = %v, want the one record that would not decode", skipped)
	}
}

// The operator's switches are read tolerantly too. There is no skip: one record
// answers one question, and what to do about a hold nobody can read differs by
// who is asking, so the store hands the failure back rather than deciding.
func TestATolerantReaderTakesAHoldRecordANewerBuildWrote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	intake, err := NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold("reordering the queue", time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	path := filepath.Join(intake.Root(), "intake-hold.json")
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	newer := strings.Replace(string(stored), "{", `{
  "a_key_this_build_has_never_heard_of": true,`, 1)
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, _, err := intake.Held(); err == nil {
		t.Fatal("Held() error = nil, want a strict reader to refuse a key it does not understand")
	}
	held, found, err := intake.Tolerant().Held()
	if err != nil {
		t.Fatalf("Held() error = %v, want the unknown key read past", err)
	}
	if !found || held.Reason != "reordering the queue" {
		t.Fatalf("Held() = %#v, %v, want the hold as it was recorded", held, found)
	}
}

// writeRunRecord puts one run state file on disk with an extra line spliced into
// it, which is how a record written by another build is reproduced without a
// second build to write it.
func writeRunRecord(t *testing.T, store *Store, runID, extra string) string {
	t.Helper()
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	document := `{
  "schema_version": 1,
  ` + extra + `
  "run_id": "` + runID + `",
  "product_id": "yoyodyne",
  "repository_id": "yoyodyne",
  "work_item_id": "yoyodyne-ifd.2.5",
  "backend": "claude-code",
  "status": "running",
  "phase": "developing",
  "started_at": "2026-08-15T22:47:57.459654Z",
  "updated_at": "2026-08-15T22:50:45.953777Z"
}
`
	if err := os.WriteFile(filepath.Join(store.Root(), runID+".json"), []byte(document), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return runID
}
