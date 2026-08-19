package beads

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestClientConfiguresTheSyncRemoteAndReadsItBack(t *testing.T) {
	t.Parallel()

	// bd stores the URL it is given in its own spelling, so what is read back is
	// deliberately not the string that was written.
	runner := &fakeRunner{responses: []string{
		`Added remote "origin"`,
		`[{"name":"origin","url":"git+ssh://git@github.com/acme/thing.git","status":"ok"}]`,
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	remote, err := client.SetSyncRemote(context.Background(), "origin", "git@github.com:acme/thing.git")
	if err != nil {
		t.Fatalf("SetSyncRemote() error = %v", err)
	}
	if remote.Name != "origin" || remote.URL != "git+ssh://git@github.com/acme/thing.git" {
		t.Fatalf("SetSyncRemote() = %#v, want the remote as bd holds it", remote)
	}
	wantArgs := [][]string{
		{"dolt", "remote", "add", "origin", "git@github.com:acme/thing.git"},
		{"dolt", "remote", "list", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}
}

// A remote that was not actually stored must not read as configured: a tracker
// reported as syncing and a tracker that syncs nowhere are the whole difference
// this step exists to make.
func TestClientRefusesASyncRemoteThatWasNotStored(t *testing.T) {
	t.Parallel()

	unstored := &fakeRunner{responses: []string{`Added remote "origin"`, `[]`}}
	if _, err := (Client{Runner: unstored}).SetSyncRemote(context.Background(), "origin", "https://example.invalid/acme/thing.git"); err == nil ||
		!strings.Contains(err.Error(), "no sync remote origin") {
		t.Fatalf("SetSyncRemote() unstored error = %v", err)
	}

	blank := &fakeRunner{responses: []string{`Added remote "origin"`, `[{"name":"origin","url":""}]`}}
	if _, err := (Client{Runner: blank}).SetSyncRemote(context.Background(), "origin", "https://example.invalid/acme/thing.git"); err == nil ||
		!strings.Contains(err.Error(), "no URL") {
		t.Fatalf("SetSyncRemote() blank error = %v", err)
	}
}

func TestClientRefusesASyncRemoteItCannotAskFor(t *testing.T) {
	t.Parallel()

	for name, remote := range map[string]struct{ name, url string }{
		"an unnamed remote":    {name: "", url: "https://example.invalid/acme/thing.git"},
		"a remote name flag":   {name: "--force", url: "https://example.invalid/acme/thing.git"},
		"no URL at all":        {name: "origin", url: "  "},
		"a URL read as flag":   {name: "origin", url: "--upload-pack=touch"},
		"a URL with a space":   {name: "origin", url: "https://example.invalid/a b.git"},
		"a URL with a newline": {name: "origin", url: "https://example.invalid/a.git\nrm -rf /"},
	} {
		runner := &fakeRunner{}
		if _, err := (Client{Runner: runner}).SetSyncRemote(context.Background(), remote.name, remote.url); err == nil {
			t.Errorf("SetSyncRemote() accepted %s", name)
		}
		if len(runner.args) != 0 {
			t.Errorf("SetSyncRemote() ran bd for %s: %v", name, runner.args)
		}
	}
}

// An unconfigured tracker is a state the caller acts on rather than a failure,
// so reading no remotes succeeds and answers with none.
func TestClientReadsAnUnconfiguredTrackerAsHavingNoSyncRemote(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{`[]`}}
	remotes, err := (Client{Runner: runner}).SyncRemotes(context.Background())
	if err != nil || len(remotes) != 0 {
		t.Fatalf("SyncRemotes() = %#v, %v", remotes, err)
	}

	failed := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "no beads database here"}}}
	if _, err := (Client{Runner: failed}).SyncRemotes(context.Background()); err == nil || !strings.Contains(err.Error(), "no beads database here") {
		t.Fatalf("SyncRemotes() error = %v, want bd's own words", err)
	}
}
