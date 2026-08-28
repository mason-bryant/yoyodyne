package beads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// conformanceTimeout is generous because the first bd call in a project starts
// its database engine. Nothing here reaches a network: both remotes are bare
// repositories on this machine.
const conformanceTimeout = 90 * time.Second

// TestSyncRemoteConformance checks the two things about bd this adapter assumes
// and a scripted runner can only restate: that `dolt remote list --json`
// answers with the fields SyncRemotes decodes, and that `dolt remote add` over
// a name the tracker already holds replaces it rather than refusing. The second
// is what `yoyo init --tracker-remote` rests on -- the flag exists to repoint a
// tracker that already has an origin -- so it is checked against bd itself.
//
// It is skipped where bd is not installed. bd is a required dependency of the
// harness rather than an optional integration, so that is a statement about the
// machine running the tests and not about the check being optional.
func TestSyncRemoteConformance(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd is not installed: %v", err)
	}

	root := t.TempDir()
	project := filepath.Join(root, "tracker")
	runCommand(t, root, "git", "init", "-q", "-b", "main", project)
	// bd init commits what it writes, which needs an identity the machine may
	// not have configured.
	runCommand(t, project, "git", "config", "user.email", "yoyodyne@example.invalid")
	runCommand(t, project, "git", "config", "user.name", "Yoyodyne Test")
	runCommand(t, project, "bd", "init")

	first := filepath.Join(root, "first.git")
	second := filepath.Join(root, "second.git")
	runCommand(t, root, "git", "init", "-q", "--bare", first)
	runCommand(t, root, "git", "init", "-q", "--bare", second)

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	configured, err := client.SetSyncRemote(ctx, "origin", first)
	if err != nil {
		t.Fatalf("SetSyncRemote() error = %v", err)
	}
	if configured.Name != "origin" || !strings.Contains(configured.URL, first) {
		t.Fatalf("SetSyncRemote() = %#v, want a remote at %s", configured, first)
	}

	// The case `--tracker-remote` exists for: a tracker that already has an
	// origin, pointed somewhere else.
	repointed, err := client.SetSyncRemote(ctx, "origin", second)
	if err != nil {
		t.Fatalf("SetSyncRemote() over an existing remote error = %v", err)
	}
	if !strings.Contains(repointed.URL, second) {
		t.Fatalf("SetSyncRemote() = %#v, want the remote repointed at %s", repointed, second)
	}

	remotes, err := client.SyncRemotes(ctx)
	if err != nil {
		t.Fatalf("SyncRemotes() error = %v", err)
	}
	if len(remotes) != 1 || remotes[0].Name != "origin" || !strings.Contains(remotes[0].URL, second) {
		t.Fatalf("SyncRemotes() = %#v, want one origin at %s", remotes, second)
	}
}

// TestExecutorMetadataConformance checks the assumption both executor writes
// rest on and a scripted runner can only restate: that bd stores the marker and
// gives it back, in each of the two spellings it takes — the whole metadata
// object a creation carries, and the single key an update sets.
//
// It is here rather than only in the fake because both write paths now refuse a
// marker bd did not store, and a refusal is only correct if bd's answer actually
// carries what it stored. If bd stopped echoing metadata on creation, every
// conversation-executed admission would fail rather than silently go unmarked —
// a loud failure rather than the quiet one, but a failure the fakes could never
// show. The third assertion is the other half: a creation given no metadata
// omits the key, which is what makes a missing executor unambiguously one that
// was not stored rather than one that was never asked for.
//
// It is skipped where bd is not installed, which is a statement about the
// machine running the tests rather than about the check being optional.
func TestExecutorMetadataConformance(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd is not installed: %v", err)
	}

	project := filepath.Join(t.TempDir(), "tracker")
	runCommand(t, t.TempDir(), "git", "init", "-q", "-b", "main", project)
	runCommand(t, project, "git", "config", "user.email", "yoyodyne@example.invalid")
	runCommand(t, project, "git", "config", "user.name", "Yoyodyne Test")
	runCommand(t, project, "bd", "init")

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	// A creation carrying the marker gets it back, which is what lets admission
	// refuse a marker that was not stored.
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Promote the brief",
		Description: "The architect promotes it in conversation.",
		Type:        "task",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	})
	if err != nil {
		t.Fatalf("Create() with an executor error = %v", err)
	}
	if created.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Create() executor = %q, want bd to echo the marker it stored", created.Executor)
	}
	// And it survives being read back separately, which is what selection does.
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Show() executor = %q, want the stored marker", shown.Executor)
	}

	// The other spelling: one key set on an item that already exists, which is how
	// work admitted before the marker existed acquires one.
	ordinary, err := client.Create(ctx, NewWorkItem{Title: "Ordinary work", Description: "A run carries it.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A creation given no metadata carries no key at all, so an absent executor is
	// unambiguous rather than a default that might have been applied.
	if !ordinary.Executor.DeveloperRun() {
		t.Fatalf("Create() executor = %q, want ordinary work to carry none", ordinary.Executor)
	}
	marked, err := client.Update(ctx, ordinary.ID, WorkItemChange{Executor: domain.ConversationWith(domain.RoleArchitect)})
	if err != nil {
		t.Fatalf("Update() with an executor error = %v", err)
	}
	if marked.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Update() executor = %q, want bd to echo the marker it set", marked.Executor)
	}
}

// TestExportConformance checks what the run-start refresh may assume about bd
// and a scripted runner can only restate: that `export` is a command bd has and
// completes, and that where it does produce the dump at ExportPath, the dump
// carries what the tracker holds right now.
//
// The second half is conditional because the first version of this test asserted
// it outright and bd refuted it: in a project freshly `bd init`ed, `bd export`
// exits zero and no file appears at ExportPath. The JSONL dump is optional in
// beads — `.beads/config.yaml` ships it commented out and calls it "Disabled by
// default" — so a project that has not enabled it has no dump for an export to
// rewrite, and bd is under no obligation to invent one.
//
// That refutation is the reason the harness stats the file and reports how old
// it is rather than treating a clean export as proof of freshness. This test is
// what would tell us the world had changed: if bd starts writing the dump
// unasked, the branch below stops being skipped and starts holding the stronger
// claim.
//
// It is skipped where bd is not installed, which is a statement about the
// machine running the tests rather than about the check being optional.
func TestExportConformance(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd is not installed: %v", err)
	}

	project := filepath.Join(t.TempDir(), "tracker")
	runCommand(t, t.TempDir(), "git", "init", "-q", "-b", "main", project)
	runCommand(t, project, "git", "config", "user.email", "yoyodyne@example.invalid")
	runCommand(t, project, "git", "config", "user.name", "Yoyodyne Test")
	runCommand(t, project, "bd", "init")

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	admitted, err := client.Create(ctx, NewWorkItem{
		Title:       "Read a fresh export",
		Description: "The item must be in the dump the export writes.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The command itself is unconditional: a run asks for this on every start, and
	// a bd that refused it would put every run on the no-current-view path.
	if err := client.Export(ctx); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	dump, err := os.ReadFile(filepath.Join(project, ExportPath))
	if errors.Is(err, os.ErrNotExist) {
		t.Logf("bd export wrote no dump at %s in a default project, so the harness is right not to assume one", ExportPath)
		return
	}
	if err != nil {
		t.Fatalf("read %s after an export: %v", ExportPath, err)
	}
	// The item was admitted after bd init and before the export, so a dump without
	// it is one written from something other than what the tracker holds now.
	if !strings.Contains(string(dump), admitted.ID) {
		t.Fatalf("the export does not carry %s, so it is not the tracker as it stands: %s", admitted.ID, dump)
	}
}

func runCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v error = %v: %s", name, args, err, output)
	}
}
