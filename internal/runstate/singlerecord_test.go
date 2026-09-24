package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A surface that fetches one run by id to say something about it is a reader
// like a listing is, and a run record a newer build wrote reaches it through
// Read with what this build knows kept. Load, beside it, still refuses: the
// caller behind it saves the record back.
func TestOneRunReadByIDKeepsWhatItKnowsOfARecordWithANewField(t *testing.T) {
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

	read, err := store.Read(state.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v, want the record read without the field it does not know", err)
	}
	if read.RunID != state.RunID || read.WorkItemID != state.WorkItemID || read.Status != state.Status {
		t.Fatalf("Read() = %#v, want the fields this build knows kept", read)
	}
	if !strings.Contains(said.String(), "a_field_a_newer_build_added") {
		t.Fatalf("notices = %q, want the field it stepped over named", said.String())
	}
	if _, err := store.Load(state.RunID); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Load() error = %v, want the unknown field refused and named", err)
	}
}

func TestOneConversationReadByIdentityKeepsWhatItKnowsOfARecordWithANewField(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newConversationStore(t, t.TempDir())
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

	read, err := store.Read(identity)
	if err != nil {
		t.Fatalf("Read() error = %v, want the conversation read without the field it does not know", err)
	}
	if read.ConversationID != conversation.ConversationID || read.Role != conversation.Role {
		t.Fatalf("Read() = %#v, want the fields this build knows kept", read)
	}
	if _, err := store.Load(identity); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Load() error = %v, want the unknown field refused and named", err)
	}
}

// The dashboard's services panel reads the supervisor's record every pass, and
// nothing reads the record to write it back, so a supervisor on a newer build is
// read rather than blanking the panel.
func TestTheSupervisionRecordWithANewFieldIsRead(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	store := newSupervisionStoreAt(t, t.TempDir())
	if err := store.Save(recordedSupervision()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, store.path(), "a_field_a_newer_build_added")

	loaded, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v, want the record read without the field it does not know", found, err)
	}
	if loaded.PID != 4242 || len(loaded.Children) != 4 {
		t.Fatalf("Load() = %+v, want the fields this build knows kept", loaded)
	}
	if !strings.Contains(said.String(), "a_field_a_newer_build_added") {
		t.Fatalf("notices = %q, want the field it stepped over named", said.String())
	}
}

// An exchange is priced and listed through Read, so one a newer build wrote is
// still in the spend figure and the listing; answering or settling it goes
// through Load, which refuses it.
func TestAnExchangeWithANewFieldIsReadAndPricedButRefusedToWhoeverActsOnIt(t *testing.T) {
	captureUnknownFieldNotices(t)

	root := t.TempDir()
	store := newTestExchangeStore(t, root)
	recorded := testExchange("a")
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	path, err := store.path(recorded.ID)
	if err != nil {
		t.Fatalf("path() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")

	if read, err := store.Read(recorded.ID); err != nil || read.ID != recorded.ID {
		t.Fatalf("Read() = %v, %v, want the exchange read without the field it does not know", read.ID, err)
	}
	if listed, err := store.List(); err != nil || len(listed) != 1 {
		t.Fatalf("List() = %d, %v, want the exchange listed", len(listed), err)
	}
	if _, err := store.Load(recorded.ID); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Load() error = %v, want the unknown field refused and named", err)
	}
}

// RecordedReadable is the listing for a reader that reports on each run on its
// own: a record that will not decode at all is handed back beside the listing
// rather than refusing every run in the directory, and Recorded, beside it,
// still refuses.
func TestARunRecordThatWillNotDecodeIsReadPastByTheReadableListing(t *testing.T) {
	store := newTestStore(t)
	readable := testState(t, StatusRunning)
	if err := store.Create(readable); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	broken := testState(t, StatusRunning)
	broken.RunID = "run-" + strings.Repeat("b", 32)
	if err := store.Create(broken); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(broken.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"run_id":`), 0o600); err != nil {
		t.Fatalf("tear the record: %v", err)
	}

	states, unreadable, err := store.RecordedReadable()
	if err != nil {
		t.Fatalf("RecordedReadable() error = %v, want the torn record read past", err)
	}
	if len(states) != 1 || states[0].RunID != readable.RunID {
		t.Fatalf("RecordedReadable() = %#v, want the readable run alone", states)
	}
	if len(unreadable) != 1 || unreadable[0].Record != broken.RunID || unreadable[0].Err == nil {
		t.Fatalf("unreadable = %#v, want the torn run named with what refused it", unreadable)
	}
	if _, err := store.Recorded(); err == nil {
		t.Fatal("Recorded() read past a torn record, want the refusal that says the set is incomplete")
	}
}

func TestAConversationRecordThatWillNotDecodeIsReadPastByTheReadableListing(t *testing.T) {
	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	conversation.Agent = "product-manager"
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.root, "architect.json"), []byte(`{"conversation_id":`), 0o600); err != nil {
		t.Fatalf("write a torn record: %v", err)
	}

	conversations, unreadable, err := store.RecordedReadable()
	if err != nil {
		t.Fatalf("RecordedReadable() error = %v, want the torn record read past", err)
	}
	if len(conversations) != 1 || conversations[0].ConversationID != conversation.ConversationID {
		t.Fatalf("RecordedReadable() = %#v, want the readable conversation alone", conversations)
	}
	if len(unreadable) != 1 || unreadable[0].Record != "architect.json" {
		t.Fatalf("unreadable = %#v, want the torn record named by its file", unreadable)
	}
	if _, err := store.Recorded(); err == nil {
		t.Fatal("Recorded() read past a torn record, want the refusal")
	}
}

// The watch holder's stamp is read by the supervisor naming the scheduler it
// adopts, which is a process that meets a session on a newer build than its
// own; the stamp is never written back, so it is read without what this build
// does not know.
func TestAWatchHolderStampWithANewFieldIsRead(t *testing.T) {
	captureUnknownFieldNotices(t)

	store, err := NewWatchStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	if err := os.MkdirAll(store.root, 0o700); err != nil {
		t.Fatalf("create the watch directory: %v", err)
	}
	stamp := []byte(`{"session_id":"session-a","pid":4242,"held_at":"2026-09-23T10:00:00Z","a_field_a_newer_build_added":true}`)
	if err := os.WriteFile(filepath.Join(store.root, watchHolderFile), stamp, 0o600); err != nil {
		t.Fatalf("write the stamp: %v", err)
	}

	holder, found, err := store.Holder()
	if err != nil || !found || holder.PID != 4242 || holder.SessionID != "session-a" {
		t.Fatalf("Holder() = %#v, %t, %v, want the stamp read without the field it does not know", holder, found, err)
	}
}

// A directive is listed by the read model and the reporting sink, which write
// none of them back, so one a newer build recorded is listed. Settling one finds
// it and saves it, and whether work may proceed is decided from what Pausing
// returns: both of those refuse it.
func TestADirectiveWithANewFieldIsListedButRefusedToWhatSettlesOrPausesOnIt(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newDirectives(t, t.TempDir())
	recorded := ambiguousDirective(t, "which of the two branches", []string{"yoyodyne-ifd.1"})
	if err := store.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	path, err := store.path(recorded.ID)
	if err != nil {
		t.Fatalf("path() error = %v", err)
	}
	addAFieldThisBuildDoesNotKnow(t, path, "a_field_a_newer_build_added")

	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].ID != recorded.ID {
		t.Fatalf("List() = %#v, %v, want the directive listed", listed, err)
	}
	if _, err := store.Pausing("yoyodyne-ifd.1"); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Pausing() error = %v, want the unknown field refused rather than work judged on part of the record", err)
	}
	if _, err := store.Resolve(recorded.ID, "the first one", recorded.ReceivedAt); err == nil || !strings.Contains(err.Error(), "a_field_a_newer_build_added") {
		t.Fatalf("Resolve() error = %v, want the unknown field refused rather than saved away", err)
	}
	if _, err := store.Load(recorded.ID); err == nil {
		t.Fatal("Load() read a directive with a field this build does not know, want it refused")
	}
}
