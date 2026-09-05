package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
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
func TestSyncRemoteConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)

	root := t.TempDir()
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
func TestExecutorMetadataConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
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

// TestParkingMetadataConformance checks the one assumption a scripted runner
// cannot restate: that bd accepts a metadata value set to nothing, and gives the
// key back as empty rather than as the value it held before.
//
// The release rests entirely on it. Parking and releasing are one write with one
// shape — the same key set to the reason or to nothing — and if bd refused an
// empty value or kept the old one, releasing parked work would fail loudly at
// the read-back check and there would be no way to put a parked item back into
// the queue from the conversation at all.
func TestParkingMetadataConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	const reason = "off the critical path by the scope decision"
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "The thin Codex backend",
		Description: "A second provider, deferred.",
		Type:        "task",
		Parking:     reason,
	})
	if err != nil {
		t.Fatalf("Create() with a parking error = %v", err)
	}
	if created.Parking.Reason() != reason {
		t.Fatalf("Create() parking = %q, want bd to echo the reason it stored", created.Parking)
	}
	// It survives a separate read, which is what selection does.
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Parking.Reason() != reason {
		t.Fatalf("Show() parking = %q, want the stored reason", shown.Parking)
	}

	// The other spelling, and the way the queue that provoked this gets parked:
	// one key set on an item that already exists.
	ordinary, err := client.Create(ctx, NewWorkItem{Title: "Ordinary work", Description: "A run carries it.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if ordinary.Parking.Parked() {
		t.Fatalf("Create() parking = %q, want ordinary work to carry none", ordinary.Parking)
	}
	parking := domain.WorkItemParking(reason)
	parked, err := client.Update(ctx, ordinary.ID, WorkItemChange{Parking: &parking})
	if err != nil {
		t.Fatalf("Update() with a parking error = %v", err)
	}
	if parked.Parking.Reason() != reason {
		t.Fatalf("Update() parking = %q, want bd to echo the reason it set", parked.Parking)
	}

	// The assertion this whole test exists for: the same key set to nothing puts
	// the work back, and reads back as unparked rather than as what it was.
	released := domain.WorkItemParking("")
	back, err := client.Update(ctx, ordinary.ID, WorkItemChange{Parking: &released})
	if err != nil {
		t.Fatalf("Update() releasing a parking error = %v", err)
	}
	if back.Parking.Parked() {
		t.Fatalf("Update() parking = %q after a release, want bd to have cleared it", back.Parking)
	}
	reread, err := client.Show(ctx, ordinary.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if reread.Parking.Parked() {
		t.Fatalf("Show() parking = %q after a release, want released work to read as unparked", reread.Parking)
	}
}

// TestGoalWitnessConformance pins the bd behaviours the goal-attribution
// hardening rests on, and which every other check covering it can only restate:
// the client's tests drive a scripted runner, so all of them pass identically
// against a bd that refuses these flags or drops what they store.
//
// Three assumptions are checked, in the order one item meets them.
//
// A creation passes --notes and --metadata together, because bd takes a
// creation's whole metadata as one JSON object: the witness rides alongside the
// notes it witnesses rather than in a second call, and a bd that refused the
// pair would fail every goal-carrying admission at runtime on both admission
// paths.
//
// An update passes --append-notes and --set-metadata together, and
// --set-metadata splits its argument at the first = and stores the rest
// verbatim. A goal statement can contain an =, and one stored cut short at it is
// a statement that would be put back wrong.
//
// Metadata survives a replace-style `bd update --notes`. That is the assumption
// the whole attribution-destruction detector rests on: the witness is kept in
// the tracker's metadata precisely because the write that destroys an
// attribution cannot reach it, and a bd version that cleared metadata alongside
// the notes would turn every destroyed attribution back into work nobody ever
// attributed -- the one state the audit deliberately does not fail on. That
// failure is silent, which is why it is checked here rather than trusted.
func TestGoalWitnessConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	const admission = "Admitted to the backlog by the product manager in conversation chat-2f0."
	const admitted = "Run development nearly autonomously, with the product manager as the human's routine interface."
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Admit the queue",
		Description: "The queue the scheduler pulls from.",
		Type:        "task",
		Notes:       admission + "\n" + goal.Note(admitted),
	})
	if err != nil {
		t.Fatalf("Create() with attributed notes error = %v; bd must accept --notes and --metadata on one creation", err)
	}
	if named, records := goal.NamedIn(created.Notes); !records || named != admitted {
		t.Fatalf("Create() notes name %q (recorded = %v), want %q; bd must store --notes given alongside --metadata",
			named, records, admitted)
	}
	if created.GoalWitness.Statement != admitted {
		t.Fatalf("Create() witness = %#v, want the statement %q; bd must store --metadata given alongside --notes",
			created.GoalWitness, admitted)
	}

	// The other spelling, and the one a re-attribution takes. The statement
	// carries an = so what is checked is the split rather than only the write:
	// --set-metadata=key=value has to divide at the first one.
	const reattributed = "Keep what a run costs visible, so spend = what the operator chose rather than what they discovered."
	updated, err := client.Update(ctx, created.ID, WorkItemChange{AppendNotes: goal.Note(reattributed)})
	if err != nil {
		t.Fatalf("Update() re-attributing error = %v; bd must accept --append-notes and --set-metadata on one update", err)
	}
	if !strings.Contains(updated.Notes, admission) {
		t.Fatalf("Update() notes = %q, want what the item already recorded still in them; --append-notes must add rather than replace",
			updated.Notes)
	}
	if named, _ := goal.NamedIn(updated.Notes); named != reattributed {
		t.Fatalf("Update() notes name %q, want the appended attribution %q", named, reattributed)
	}
	if updated.GoalWitness.Statement != reattributed {
		t.Fatalf("Update() witness = %#v, want %q stored verbatim; --set-metadata must split at the first = and keep the rest",
			updated.GoalWitness, reattributed)
	}

	// The write nothing in this package makes, and the one the witness exists to
	// outlive: an item's notes replaced wholesale from outside the harness. It is
	// spelled as bd itself because the client has no way to spell it.
	runCommand(t, project, "bd", "update", created.ID, "--notes=Rewritten wholesale by a careless writer.")
	destroyed, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	// The destruction has to have actually happened, or what follows proves
	// nothing about what survives it.
	if named, records := goal.NamedIn(destroyed.Notes); records {
		t.Fatalf("Show() notes still name %q after a wholesale replacement, so this check says nothing about the witness", named)
	}
	if destroyed.GoalWitness.Statement != reattributed {
		t.Fatalf("Show() witness = %#v after the notes were replaced, want %q still recorded; a bd that clears an item's "+
			"metadata when its notes are replaced makes every destroyed attribution read as work nobody ever attributed",
			destroyed.GoalWitness, reattributed)
	}
}

// TestDecompositionEdgeConformance pins the direction bd states decomposition
// in, which until now was pinned only by a payload captured by hand: a scripted
// runner replays the direction it was written with, so it agrees with itself
// whichever way bd actually answers.
//
// The direction is what a listing turns on. bd states parentage as an edge
// attributed to the child and naming the parent, and the epic beside it carries
// no such edge of its own; a reading with those the other way round would see an
// epic as work broken out of its own child, defer the child, and run the epic.
// That is a failure the guard could have rather than one it has had:
// yoyodyne-ifd.121 and the child carrying its execution were started as two
// developer runs of one scope thirteen hours before any parentage-keyed guard
// existed, so the double-run says nothing about the direction either way. See
// docs/diagnoses/yoyodyne-ifd-273-121-double-run-mechanism.md.
//
// The edge is asserted directly rather than only through DecomposedFrom, because
// bd answers the parent as a field as well, and a reading that found the field
// would report the right answer whatever the edge said.
func TestDecompositionEdgeConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	epic, err := client.Create(ctx, NewWorkItem{
		Title:       "Split the README",
		Description: "The work it was broken into is below it.",
		Type:        "epic",
	})
	if err != nil {
		t.Fatalf("Create() an epic error = %v", err)
	}
	child, err := client.Create(ctx, NewWorkItem{
		Title:       "Execute the README split",
		Description: "One piece of it.",
		Type:        "task",
		Parent:      epic.ID,
	})
	if err != nil {
		t.Fatalf("Create() a child error = %v; bd must accept --parent", err)
	}

	// The listing shape rather than a single item's, because that is what
	// selection reads and where the direction was got wrong.
	listed, err := client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	byID := make(map[string]WorkItem, len(listed))
	for _, item := range listed {
		byID[item.ID] = item
	}
	decomposed, hasChild := byID[child.ID]
	whole, hasEpic := byID[epic.ID]
	if !hasChild || !hasEpic {
		t.Fatalf("Ready() = %#v, want both %s and %s", listed, epic.ID, child.ID)
	}

	if named := parentEdgesOf(decomposed); len(named) != 1 || named[0] != epic.ID {
		t.Fatalf("the child's own parent-child edges name %v, want just the epic %s; bd must attribute the edge to the "+
			"child and point it at the parent", named, epic.ID)
	}
	if named := parentEdgesOf(whole); len(named) != 0 {
		t.Fatalf("the epic's own parent-child edges name %v, want none; an edge stated in the other direction reads as an "+
			"epic having been broken out of its own child", named)
	}
	if got := decomposed.DecomposedFrom(); got != epic.ID {
		t.Fatalf("the child's DecomposedFrom() = %q, want the epic %s", got, epic.ID)
	}
	if got := whole.DecomposedFrom(); got != "" {
		t.Fatalf("the epic's DecomposedFrom() = %q, want nothing: it was broken out of nothing", got)
	}
}

// TestParentFieldConformance pins the other half of how bd states parentage: the
// field beside the item, on every read path selection consumes.
//
// It is here because a reading in the harness rests on the field alone.
// orchestrator's in-flight sequencing keys on beads.WorkItem.Parent and
// deliberately not on the wider DecomposedFrom, so a bd that stopped populating
// the field would leave that half of the guard reading an empty map — inert, and
// silently, because every other check of it drives a scripted runner that
// replays whatever it was handed.
//
// The field's history is settled rather than assumed: bd 1.1.2 is the version
// this project has had installed since 2026-07-26, twenty-five days before the
// yoyodyne-ifd.121 double-run, and it populates the field. So the field was not
// gained after that incident and no version floor is owed to it. What this check
// is for is the other direction — a bd that drops the field later — which is why
// it asserts rather than trusts. The floor it pins the behaviour at is the bd
// this ran against, and `bd version` names it.
//
// The export is the one shape that states only the edge, carrying no parent
// field on any item; nothing here reads the export, and beads.WorkItem's own
// DecomposedFrom is what covers it.
func TestParentFieldConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	epic, err := client.Create(ctx, NewWorkItem{
		Title:       "Split the README",
		Description: "The work it was broken into is below it.",
		Type:        "epic",
	})
	if err != nil {
		t.Fatalf("Create() an epic error = %v", err)
	}
	child, err := client.Create(ctx, NewWorkItem{
		Title:       "Execute the README split",
		Description: "One piece of it.",
		Type:        "task",
		Parent:      epic.ID,
	})
	if err != nil {
		t.Fatalf("Create() a child error = %v; bd must accept --parent", err)
	}

	// Every read path a pull makes, because the guard that rests on the field
	// reads the queue and the claimed slice alike, and a field carried on one
	// listing and not another is the same inertness in a narrower place.
	listed, err := client.List(ctx, "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	ready, err := client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	shown, err := client.Show(ctx, child.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	shownEpic, err := client.Show(ctx, epic.ID)
	if err != nil {
		t.Fatalf("Show() the epic error = %v", err)
	}

	for _, path := range []struct {
		name  string
		items []WorkItem
	}{
		{name: "List(open)", items: listed},
		{name: "Ready()", items: ready},
		{name: "Show()", items: []WorkItem{shown, shownEpic}},
	} {
		byID := make(map[string]WorkItem, len(path.items))
		for _, item := range path.items {
			byID[item.ID] = item
		}
		decomposed, hasChild := byID[child.ID]
		whole, hasEpic := byID[epic.ID]
		if !hasChild || !hasEpic {
			t.Fatalf("%s did not answer both %s and %s", path.name, epic.ID, child.ID)
		}
		if decomposed.Parent != epic.ID {
			t.Fatalf("%s gave the child's parent field as %q, want the epic %s; a bd that stops populating it leaves the "+
				"scheduler's in-flight sequencing reading an empty map and holding nothing back",
				path.name, decomposed.Parent, epic.ID)
		}
		if whole.Parent != "" {
			t.Fatalf("%s gave the epic's parent field as %q, want nothing: it was broken out of nothing", path.name, whole.Parent)
		}
	}
}

// trackerExportPath is where the harness looks for the tracker's passive JSONL
// dump, spelled here as the harness spells it — internal/cli/run.go names the
// same path as the export a worktree is given. Nothing asks bd where it writes,
// so the path being bd's own default is the half of this the check proves.
const trackerExportPath = ".beads/issues.jsonl"

// TestTrackerExportConformance pins the bd behaviour every in-run traceability
// sweep rests on and which nothing here has ever executed: that a project asking
// bd for the JSONL dump gets a real file, at the path the harness looks for it,
// carrying the item a run has just claimed.
//
// The harness never writes that file. It copies the primary checkout's copy into
// each new worktree and holds it out of the change — internal/gitworktree/exports.go
// — so what makes the copy current is bd's own auto-export and nothing else.
// Every check of the copying drives a file the test wrote itself and is
// satisfied whatever bd does, and the path is a constant the harness spells
// rather than one it asks bd for. A bd that stopped writing the dump, or wrote
// it somewhere else, would leave every run reading whatever the last release cut
// committed and reporting work admitted since as simply absent — the
// confidently wrong answer rather than the visibly stale one.
//
// The dump is off in a project bd has just initialized, so a check that only
// created a tracker would find no file and could say nothing either way. It is
// enabled here the way the project this harness runs on enables it — one key bd
// itself writes into .beads/config.yaml — and only that key, so the path the
// file lands at is bd's default rather than one this check chose.
//
// What is asserted is that the item is in the dump, not what the dump says about
// it. bd flushes on an interval of its own, so a claim reaches the store before
// it reaches the file, and asserting the status there would be asserting bd's
// schedule rather than the behaviour a run depends on.
func TestTrackerExportConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	// bd's own spelling of the knob, and the one the project this harness runs on
	// carries in its .beads/config.yaml.
	runCommand(t, project, "bd", "config", "set", "export.auto", "true")

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Read the work around this run's own",
		Description: "A run sweeps the export for what cites what.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Claimed rather than only created, because the item a run most needs to find
	// in the dump is its own, and the claim is the last write before it looks.
	claimed, err := client.Claim(ctx, created.ID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress; the item this check looks for was never claimed", claimed.Status)
	}

	content, err := os.ReadFile(filepath.Join(project, trackerExportPath))
	if err != nil {
		t.Fatalf("read the tracker's dump at %s error = %v; a project that asked bd for the JSONL export must get "+
			"one written there, or every run reads the copy the last release cut committed", trackerExportPath, err)
	}
	named, err := exportedIDs(content)
	if err != nil {
		t.Fatalf("the dump at %s is not the JSONL of work items a run reads it as: %v", trackerExportPath, err)
	}
	if !slices.Contains(named, claimed.ID) {
		t.Fatalf("the dump at %s names %v, want the just-claimed item %s among them; a dump that lags the store is a "+
			"run reading its own work item as absent", trackerExportPath, named, claimed.ID)
	}
}

// exportedIDs is what one JSONL dump names, and refuses a dump whose lines are
// not work items at all. A file that is there but says nothing a run can read is
// the same failure as a file that is not there.
func exportedIDs(content []byte) ([]string, error) {
	var named []string
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var exported struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &exported); err != nil {
			return nil, fmt.Errorf("decode %q: %w", line, err)
		}
		if exported.ID == "" {
			return nil, fmt.Errorf("line %q names no work item", line)
		}
		named = append(named, exported.ID)
	}
	if len(named) == 0 {
		return nil, errors.New("the dump names nothing")
	}
	return named, nil
}

// parentEdgesOf reports what an item's own parent-child edges name. An edge the
// tracker attributes to some other item is not this item's, which is the whole
// of the distinction the direction check turns on.
func parentEdgesOf(item WorkItem) []string {
	var named []string
	for _, dependency := range item.Dependencies {
		if !strings.EqualFold(dependency.Type, parentChildDependency) {
			continue
		}
		if dependency.IssueID != "" && dependency.IssueID != item.ID {
			continue
		}
		named = append(named, dependency.ID)
	}
	return named
}

// newTracker cuts one conformance check a scratch Beads store of its own and
// answers the directory bd runs in. Nothing here reaches the project's own
// tracker or a network: the store is thrown away with the test's temporary
// directory.
//
// It skips where bd is not installed. bd is a required dependency of the harness
// rather than an optional integration, so that is a statement about the machine
// running the tests and not about these checks being optional.
func newTracker(t *testing.T) string {
	t.Helper()

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
	return project
}

func runCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v error = %v: %s", name, args, err, output)
	}
}
