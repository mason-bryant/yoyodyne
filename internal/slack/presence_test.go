package slack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSecretNamesCarryTheProduct is the whole of what makes a secret check mean
// anything on a machine running more than one harness. Under a generic name the
// question "is a Slack token stored" has one answer for every project on the
// machine, and it is the right answer for at most one of them.
func TestSecretNamesCarryTheProduct(t *testing.T) {
	t.Parallel()

	if BotSecret("yoyodyne") == BotSecret("sibling") || AppSecret("yoyodyne") == AppSecret("sibling") {
		t.Fatal("two products share a secret name, so neither can be checked for on its own")
	}
	if BotSecret("yoyodyne") == AppSecret("yoyodyne") {
		t.Fatal("the two tokens share a name, so a missing one reads as present")
	}
	for _, name := range []string{BotSecret("yoyodyne"), AppSecret("yoyodyne")} {
		if !strings.HasSuffix(name, ".yoyodyne") {
			t.Fatalf("secret name %q does not carry the product", name)
		}
	}
}

func TestPresenceSurvivesBeingWrittenAndRead(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, found, err := store.LoadPresence(); err != nil || found {
		t.Fatalf("LoadPresence() = %t, %v, want nothing recorded yet", found, err)
	}

	recorded := Presence{
		PID:             4242,
		Version:         "v1.2.3",
		Config:          "/p/.yoyodyne/config.yaml",
		Channel:         "C1",
		SecretNamespace: "yoyodyne",
		Team:            "Example",
		TeamID:          "T1",
		StartedAt:       time.Now().UTC().Truncate(time.Second),
	}
	if err := store.SavePresence(recorded); err != nil {
		t.Fatalf("SavePresence() error = %v", err)
	}
	read, found, err := store.LoadPresence()
	if err != nil || !found {
		t.Fatalf("LoadPresence() = %t, %v", found, err)
	}
	recorded.SchemaVersion = PresenceSchemaVersion
	if read != recorded {
		t.Fatalf("LoadPresence() = %#v, want %#v", read, recorded)
	}

	if err := store.ClearPresence(); err != nil {
		t.Fatalf("ClearPresence() error = %v", err)
	}
	if _, found, err := store.LoadPresence(); err != nil || found {
		t.Fatalf("LoadPresence() = %t, %v, want it forgotten", found, err)
	}
	// Forgetting what was never recorded is what a sink that failed to write one
	// does on its way out, and it is not a failure.
	if err := store.ClearPresence(); err != nil {
		t.Fatalf("ClearPresence() error = %v on a record that was already gone", err)
	}
}

// A record this build does not understand is refused rather than read as an
// empty one: presence answers "which build is running", and a default-valued
// answer to that question is worse than no answer.
func TestPresenceRefusesASchemaItDoesNotKnow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(store.Root(), "presence.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"version":"v9"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.LoadPresence(); err == nil {
		t.Fatal("LoadPresence() = nil, want a schema this build does not know refused")
	}
}

// TestARunningSinkRecordsWhatItIsAndForgetsItOnTheWayOut is what makes a stale
// sink findable. A sink is a long-lived process started from a binary that
// keeps moving underneath it, and nothing about that drift is visible in the
// channel: it posts what its own build knew how to post, and the milestones
// added since read as a quiet week.
func TestARunningSinkRecordsWhatItIsAndForgetsItOnTheWayOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sink := newTestSink(t, root, &fixedFeed{}, &recordedPosts{})
	sink.identity = Presence{Version: "v1.2.3", Config: "/p/.yoyodyne/config.yaml", SecretNamespace: "yoyodyne"}
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sink.Run(ctx) }()

	var recorded Presence
	waitFor(t, func() bool {
		presence, found, err := store.LoadPresence()
		if err != nil || !found {
			return false
		}
		recorded = presence
		return true
	})
	if recorded.Version != "v1.2.3" || recorded.SecretNamespace != "yoyodyne" || recorded.Channel != "C1" {
		cancel()
		t.Fatalf("recorded presence = %#v", recorded)
	}
	if recorded.PID != os.Getpid() {
		cancel()
		t.Fatalf("recorded pid = %d, want this process %d", recorded.PID, os.Getpid())
	}
	if recorded.StartedAt.IsZero() {
		cancel()
		t.Fatal("recorded presence has no start time, so nothing can say how long it has been wrong")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}

	// On the way out it forgets, so a stopped sink reads as stopped rather than
	// as one whose build somebody should go and check.
	if _, found, err := store.LoadPresence(); err != nil || found {
		t.Fatalf("LoadPresence() = %t, %v, want a stopped sink to have forgotten", found, err)
	}
}
