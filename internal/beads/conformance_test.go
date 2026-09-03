package beads

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
// no such edge of its own; a reading with those the other way round sees an epic
// as work broken out of its own child, which is how yoyodyne-ifd.121 and the
// child carrying its execution came to be started as two developer runs of one
// scope.
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

// concurrentRuns is the developer capacity this exercise clears. It is two
// because that is what raising execution.max_concurrent_developers off its
// default asks for, and two runs beside the scheduler is already three processes
// holding one store open — one more than the case that has never been exercised.
const concurrentRuns = 2

// TestConcurrentRunConformance is the live exercise nothing in this package
// could stand in for: every other concurrency check here drives an in-process
// fake, so all of them pass identically against a store that refuses a second
// opener. The tracker is an embedded Dolt database behind a file lock, and this
// adapter neither serializes its invocations nor retries a contended one, so
// whether capacity above one is safe was a question no check asked.
//
// What runs here is the invocation pattern capacity two actually produces: two
// developer runs each making the whole sequence one run makes against the
// tracker — Show, Claim, RecordOutcome, RecordCost, Complete — while the
// scheduler reads List and Ready beside them for as long as they work. Every
// invocation is a separate bd process against one store, which is the shape the
// fakes cannot have.
//
// It asserts no invocation failed rather than that contention was handled,
// because the two are the same assertion from here: a contended invocation this
// adapter meets has nowhere to go but back to its caller as a failed run. A bd
// that stops serializing concurrent openers fails this loudly, at the boundary
// that would otherwise record completed work as failed.
func TestConcurrentRunConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	carried := make([]string, 0, concurrentRuns)
	for run := 0; run < concurrentRuns; run++ {
		created, err := client.Create(ctx, NewWorkItem{
			Title:       fmt.Sprintf("Work for run %d", run),
			Description: "One of the concurrent developer runs carries it.",
			Type:        "task",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		carried = append(carried, created.ID)
	}

	var contended problems
	var running sync.WaitGroup
	for run, id := range carried {
		running.Add(1)
		go func() {
			defer running.Done()

			if _, err := client.Show(ctx, id); err != nil {
				contended.record("run %d: Show(%s): %v", run, id, err)
				return
			}
			if _, err := client.Claim(ctx, id); err != nil {
				contended.record("run %d: Claim(%s): %v", run, id, err)
				return
			}
			if _, err := client.RecordOutcome(ctx, id, outcomeOf(run)); err != nil {
				contended.record("run %d: RecordOutcome(%s): %v", run, id, err)
				return
			}
			if _, err := client.RecordCost(ctx, id, Cost{TotalUSD: float64(run) + 0.5, Runs: 1}); err != nil {
				contended.record("run %d: RecordCost(%s): %v", run, id, err)
				return
			}
			if _, err := client.Complete(ctx, id, "the run finished"); err != nil {
				contended.record("run %d: Complete(%s): %v", run, id, err)
			}
		}()
	}

	// The scheduler's own reads, made against the same store for as long as the
	// runs are writing to it. It is bounded by the runs rather than by a count so
	// the reads cover the whole of the window they contend with.
	finished := make(chan struct{})
	var reading sync.WaitGroup
	reading.Add(1)
	go func() {
		defer reading.Done()

		for {
			select {
			case <-finished:
				return
			default:
			}
			if _, err := client.Ready(ctx); err != nil {
				contended.record("scheduler: Ready(): %v", err)
				return
			}
			if _, err := client.List(ctx, "in_progress"); err != nil {
				contended.record("scheduler: List(in_progress): %v", err)
				return
			}
		}
	}()

	running.Wait()
	close(finished)
	reading.Wait()

	if met := contended.recorded(); len(met) > 0 {
		t.Fatalf("bd invocations failed with %d concurrent developer runs beside the scheduler's reads:\n%s\n"+
			"the adapter neither serializes nor retries a contended invocation, so each of these is a run recorded as "+
			"failed; capacity above one needs the adapter given a lock or retry-with-backoff before it is raised",
			concurrentRuns, strings.Join(met, "\n"))
	}

	// A failure that is reported is the loud half. The quiet half is a write bd
	// accepted and did not keep, which is what would corrupt exactly the records
	// a run's outcome is reconstructed from, so each item is read back afterwards.
	for run, id := range carried {
		item, err := client.Show(ctx, id)
		if err != nil {
			t.Fatalf("Show(%s) after the runs finished error = %v", id, err)
		}
		if item.Status != "closed" {
			t.Errorf("work item %s status = %q after run %d completed it, want closed", id, item.Status, run)
		}
		if !strings.Contains(item.Notes, outcomeOf(run)) {
			t.Errorf("work item %s notes = %q, want run %d's outcome in them", id, item.Notes, run)
		}
		if item.Cost == nil || item.Cost.Runs != 1 {
			t.Errorf("work item %s cost = %#v, want the price run %d recorded", id, item.Cost, run)
		}
	}
}

// TestConcurrentWriteConformance pins the other half of the same question, and
// the half a failure count would never show: whether two writes to one item that
// overlap both survive.
//
// It matters because every write this adapter makes is read-modify-write inside
// bd — a note is appended to the notes already there, a metadata key is set
// beside the keys already there — so two overlapping writes to one item are a
// lost update unless bd serializes them. That is not the pattern capacity two
// produces on its own, where each run holds its own item; it is the pattern a
// run's own write meets a reconcile or a conversation write on, and it is the
// one whose failure is silent. A lost --append-notes takes a goal attribution
// with it, and this package already carries a witness against exactly that loss.
func TestConcurrentWriteConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	const writers = 4
	contested, err := client.Create(ctx, NewWorkItem{
		Title:       "The item everything writes to",
		Description: "A run, a reconcile, and a conversation all reach it.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var contended problems
	var writing sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		writing.Add(1)
		go func() {
			defer writing.Done()

			if _, err := client.RecordOutcome(ctx, contested.ID, outcomeOf(writer)); err != nil {
				contended.record("writer %d: RecordOutcome(): %v", writer, err)
			}
		}()
	}
	writing.Wait()

	if met := contended.recorded(); len(met) > 0 {
		t.Fatalf("%d overlapping writes to one work item failed:\n%s", writers, strings.Join(met, "\n"))
	}

	item, err := client.Show(ctx, contested.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	for writer := 0; writer < writers; writer++ {
		if !strings.Contains(item.Notes, outcomeOf(writer)) {
			t.Errorf("work item %s notes = %q after %d overlapping appends, want writer %d's in them; a bd that reads an "+
				"item's notes and writes them back without serializing loses whichever write finished first, and an "+
				"attribution lost that way is lost silently",
				contested.ID, item.Notes, writers, writer)
		}
	}
}

// outcomeOf is what one concurrent writer records, distinct per writer so a
// write that was accepted and dropped is told from one that never happened.
func outcomeOf(writer int) string {
	return fmt.Sprintf("outcome recorded by writer %d", writer)
}

// problems collects what failed across the goroutines making concurrent
// invocations. The failures are gathered rather than reported where they happen
// because a t.Fatalf off the test's own goroutine stops nothing, and because a
// contended store fails several invocations at once: which of them failed is the
// evidence, and reporting only the first would hide the shape.
type problems struct {
	mu  sync.Mutex
	met []string
}

func (p *problems) record(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.met = append(p.met, fmt.Sprintf(format, args...))
}

func (p *problems) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.met
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
