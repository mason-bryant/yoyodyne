package beads

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestClientWorkItemLifecycle(t *testing.T) {
	t.Parallel()

	responses := []string{
		workItemJSON("open", ""),
		workItemJSON("in_progress", ""),
		workItemJSON("in_progress", "checks passed"),
		`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"added"}`,
		workItemJSON("closed", "checks passed"),
	}
	runner := &fakeRunner{responses: responses}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	item, err := client.Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.ID != "yoyodyne-1" || len(item.Dependencies) != 1 || item.Dependencies[0].ID != "yoyodyne-parent" || item.Dependencies[0].Status != "closed" {
		t.Fatalf("Show() = %#v", item)
	}
	if _, err := client.Claim(context.Background(), item.ID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := client.RecordOutcome(context.Background(), item.ID, "checks passed"); err != nil {
		t.Fatalf("RecordOutcome() error = %v", err)
	}
	if err := client.AddBlocker(context.Background(), item.ID, "yoyodyne-blocker"); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), item.ID, "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	wantArgs := [][]string{
		{"show", "yoyodyne-1", "--json"},
		{"update", "yoyodyne-1", "--claim", "--json"},
		{"update", "yoyodyne-1", "--append-notes=checks passed", "--json"},
		{"dep", "add", "yoyodyne-1", "yoyodyne-blocker", "--json"},
		{"close", "yoyodyne-1", "--reason=done", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestClientBlocksAnItemAndVerifiesTheStatusItApplied(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{workItemJSON("blocked", "unresolved review findings")}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Block(context.Background(), "yoyodyne-1", "unresolved review findings")
	if err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	if item.Status != "blocked" || item.Notes != "unresolved review findings" {
		t.Fatalf("Block() = %#v", item)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=blocked", "--append-notes=unresolved review findings", "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A blocker that was not actually applied must not read as recorded.
	unapplied := &fakeRunner{responses: []string{workItemJSON("in_progress", "unresolved review findings")}}
	if _, err := (Client{Runner: unapplied}).Block(context.Background(), "yoyodyne-1", "findings"); err == nil || !strings.Contains(err.Error(), "want blocked") {
		t.Fatalf("Block() unapplied error = %v", err)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).Block(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("Block() empty reason error = nil")
	}
}

// An item a run integrated a change for and did not discharge goes back to the
// backlog carrying why. The status is read back for the reason a blocker's is:
// an item left claimed by a run that has ended is work nothing can start and
// nothing is watching.
func TestClientReopensAnItemAndVerifiesTheStatusItApplied(t *testing.T) {
	t.Parallel()

	reason := "run-1 landed evidence and did not discharge this item"
	runner := &fakeRunner{responses: []string{workItemJSON("open", reason)}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Reopen(context.Background(), "yoyodyne-1", reason)
	if err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	if item.Status != "open" || item.Notes != reason {
		t.Fatalf("Reopen() = %#v", item)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=open", "--append-notes=" + reason, "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	unapplied := &fakeRunner{responses: []string{workItemJSON("in_progress", reason)}}
	if _, err := (Client{Runner: unapplied}).Reopen(context.Background(), "yoyodyne-1", reason); err == nil ||
		!strings.Contains(err.Error(), "want open") {
		t.Fatalf("Reopen() unapplied error = %v", err)
	}
	// An item put back with no reason reads afterwards as work somebody walked
	// away from, which is the state this call exists to avoid.
	if _, err := (Client{Runner: &fakeRunner{}}).Reopen(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("Reopen() empty reason error = nil")
	}
}

func TestClientAppliesOnlyTheEditItWasGiven(t *testing.T) {
	t.Parallel()

	priority := 0
	parent := "yoyodyne-ifd.12"
	detached := ""
	runner := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-1","title":"Readable conversations","status":"open","priority":2,"issue_type":"task"}]`,
		`[{"id":"yoyodyne-1","title":"Implement feature","status":"open","priority":0,"issue_type":"task"}]`,
		workItemJSON("open", ""),
		`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"removed"}`,
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{
		Title:       "Readable conversations",
		Description: "Say who is speaking.",
		AppendNotes: "Renamed by the product manager.",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Priority: &priority, Parent: &parent}); err != nil {
		t.Fatalf("Update() priority error = %v", err)
	}
	// An empty parent detaches the item, which is a different request from
	// saying nothing about the parent at all.
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Parent: &detached}); err != nil {
		t.Fatalf("Update() detach error = %v", err)
	}
	if err := client.RemoveBlocker(context.Background(), "yoyodyne-1", "yoyodyne-blocker"); err != nil {
		t.Fatalf("RemoveBlocker() error = %v", err)
	}

	wantArgs := [][]string{
		{"update", "yoyodyne-1", "--title=Readable conversations", "--description=Say who is speaking.", "--append-notes=Renamed by the product manager.", "--json"},
		{"update", "yoyodyne-1", "--priority=0", "--parent=yoyodyne-ifd.12", "--json"},
		{"update", "yoyodyne-1", "--parent=", "--json"},
		{"dep", "remove", "yoyodyne-1", "yoyodyne-blocker", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// An edit bd did not actually apply must not read as applied.
	unapplied := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"Something else","status":"open","priority":2,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unapplied}).Update(context.Background(), "yoyodyne-1", WorkItemChange{Title: "Readable conversations"}); err == nil ||
		!strings.Contains(err.Error(), "want \"Readable conversations\"") {
		t.Fatalf("Update() unapplied title error = %v", err)
	}
	unmoved := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"t","status":"open","priority":3,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unmoved}).Update(context.Background(), "yoyodyne-1", WorkItemChange{Priority: &priority}); err == nil ||
		!strings.Contains(err.Error(), "want 0") {
		t.Fatalf("Update() unapplied priority error = %v", err)
	}
	// A dependency the tracker did not report removing is still a dependency.
	unremoved := &fakeRunner{responses: []string{`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"added"}`}}
	if err := (Client{Runner: unremoved}).RemoveBlocker(context.Background(), "yoyodyne-1", "yoyodyne-blocker"); err == nil ||
		!strings.Contains(err.Error(), "unexpected bd dependency response") {
		t.Fatalf("RemoveBlocker() unapplied error = %v", err)
	}
}

func TestClientRefusesAnEditItCannotApply(t *testing.T) {
	t.Parallel()

	tooLow, tooHigh := -1, MaxPriority+1
	badParent := "../etc"
	for _, test := range []struct {
		name   string
		id     string
		change WorkItemChange
		want   string
	}{
		{name: "invented id", id: "../etc", change: WorkItemChange{Title: "t"}, want: "invalid Beads issue id"},
		{name: "nothing to change", id: "yoyodyne-1", want: "must change something"},
		{name: "priority below the scale", id: "yoyodyne-1", change: WorkItemChange{Priority: &tooLow}, want: "outside 0.."},
		{name: "priority above the scale", id: "yoyodyne-1", change: WorkItemChange{Priority: &tooHigh}, want: "outside 0.."},
		{name: "invented parent", id: "yoyodyne-1", change: WorkItemChange{Parent: &badParent}, want: "invalid parent"},
		{name: "title spanning lines", id: "yoyodyne-1", change: WorkItemChange{Title: "t\n--force"}, want: "cannot span lines"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).Update(context.Background(), test.id, test.change); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Update() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused edit still ran bd %#v", runner.args)
			}
		})
	}
}

func TestClientListsWorkItemsWithoutChangingAnything(t *testing.T) {
	t.Parallel()

	// Notes ride along on a listing: every attribution-reporting path reads
	// them off List results, so a List that dropped them would report the
	// whole queue as naming no goal. The real bd emits notes in list --json;
	// this pins that the client keeps them.
	listed := `[{"id":"yoyodyne-1","title":"First","status":"open","priority":1,"issue_type":"task","notes":"Goal: maintain the traceable chain."},
	            {"id":"yoyodyne-2","title":"Second","status":"open","priority":2,"issue_type":"feature"}]`
	runner := &fakeRunner{responses: []string{listed, `[]`}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != "yoyodyne-1" || items[1].IssueType != "feature" {
		t.Fatalf("List() = %#v", items)
	}
	if items[0].Notes != "Goal: maintain the traceable chain." {
		t.Fatalf("List() dropped notes: %#v", items[0])
	}
	empty, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() unfiltered error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List() unfiltered = %#v", empty)
	}
	wantArgs := [][]string{
		{"list", "--json", "--status=open"},
		{"list", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A status is a filter, never an argument smuggled onto the command line.
	if _, err := (Client{Runner: &fakeRunner{}}).List(context.Background(), "--dangerous"); err == nil {
		t.Fatal("List() invalid status error = nil")
	}
}

func TestClientReadsWhenAnItemWasAdmittedAndSaysNothingWhenItCannot(t *testing.T) {
	t.Parallel()

	// When an item was admitted is what says which wording the work was pulled
	// under, so a listing that dropped it would report every item as one nothing
	// upstream can be compared against. The real bd emits it as RFC 3339.
	listed := `[{"id":"yoyodyne-1","title":"First","status":"open","priority":1,"issue_type":"task","created_at":"2026-08-17T03:55:26Z"},
	            {"id":"yoyodyne-2","title":"Second","status":"open","priority":2,"issue_type":"task"},
	            {"id":"yoyodyne-3","title":"Third","status":"open","priority":2,"issue_type":"task","created_at":"last Tuesday"}]`
	client := Client{Runner: &fakeRunner{responses: []string{listed}}, Binary: "bd-test", Dir: "/repo"}

	items, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("List() = %#v", items)
	}
	if want := time.Date(2026, 8, 17, 3, 55, 26, 0, time.UTC); !items[0].CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", items[0].CreatedAt, want)
	}
	// A tracker that says nothing, and one that says something this cannot read,
	// both leave the admission time unknown. Neither costs the read: the item is
	// still the item, and whatever needs the time reports that it does not have it.
	if !items[1].CreatedAt.IsZero() || !items[2].CreatedAt.IsZero() {
		t.Fatalf("List() = %#v, want an unknown admission time rather than a guessed one", items[1:])
	}
}

// What can be pulled is a question the tracker answers from its own dependency
// graph. The payload below is what bd actually returns, dependency shape and
// all: the relation is recorded with no completion state on it, which is exactly
// why readiness is asked for rather than worked out from a listing.
func TestClientAsksTheTrackerWhatIsReadyRatherThanWorkingItOut(t *testing.T) {
	t.Parallel()

	ready := `[{"id":"bdprobe-uxm","title":"Waiting item","description":"d","status":"open","priority":0,
	            "issue_type":"task","owner":"someone@example.com",
	            "dependencies":[{"issue_id":"bdprobe-uxm","depends_on_id":"bdprobe-3kw","type":"blocks",
	                             "created_by":"Someone","metadata":"{}"}],
	            "dependency_count":1,"dependent_count":0,"comment_count":0}]`
	runner := &fakeRunner{responses: []string{ready}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "bdprobe-uxm" {
		t.Fatalf("Ready() = %#v", items)
	}
	// The dependency decodes from the spelling bd uses, and carries no status,
	// which is the fact the backlog is built around.
	if len(items[0].Dependencies) != 1 {
		t.Fatalf("dependencies = %#v", items[0].Dependencies)
	}
	dependency := items[0].Dependencies[0]
	if dependency.ID != "bdprobe-3kw" || dependency.Type != "blocks" || dependency.Status != "" {
		t.Fatalf("dependency = %#v", dependency)
	}
	if !reflect.DeepEqual(runner.args, [][]string{{"ready", "--json"}}) {
		t.Fatalf("bd args = %#v", runner.args)
	}
}

// Decomposition can reach this reading as an edge and nothing else, and the
// reading that matters is the one that finds it there. The payload is that case
// rather than the whole of what bd answers — no parent field anywhere in it, and
// the parent named by a parent-child edge attributed to the child itself — which
// is how the tracker's own export states parentage. What a real listing states
// is pinned by the capture in TestACapturedListingStatesParentageAsAnEdgeAttributedToTheChild.
func TestClientReadsDecompositionStatedAsAnEdgeRatherThanAField(t *testing.T) {
	t.Parallel()

	child := `[{"id":"yoyodyne-ifd.121.2","title":"Execute the README split","description":"d","status":"open",
	            "priority":1,"issue_type":"task",
	            "dependencies":[{"issue_id":"yoyodyne-ifd.121.2","depends_on_id":"yoyodyne-ifd.121",
	                             "type":"parent-child","metadata":"{}"},
	                            {"issue_id":"yoyodyne-ifd.121.2","depends_on_id":"yoyodyne-ifd.121.1",
	                             "type":"blocks","metadata":"{}"}]}]`
	runner := &fakeRunner{responses: []string{child}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Ready() = %#v", items)
	}
	item := items[0]
	// The field really is absent: this is the case a reader of it alone gets
	// wrong, rather than a store that states parentage both ways.
	if item.Parent != "" {
		t.Fatalf("parent field = %q, want the payload's own answer of none", item.Parent)
	}
	if got := item.DecomposedFrom(); got != "yoyodyne-ifd.121" {
		t.Fatalf("DecomposedFrom() = %q, want the parent the edge names", got)
	}
	// Which item an edge belongs to is what tells a parent from a child, so it
	// has to survive decoding to be checkable at all.
	if item.Dependencies[0].IssueID != "yoyodyne-ifd.121.2" {
		t.Fatalf("dependency = %#v, want the item the tracker attributes it to", item.Dependencies[0])
	}
}

func TestWorkItemDecomposedFrom(t *testing.T) {
	t.Parallel()

	edge := func(issue, parent, kind string) Dependency {
		return Dependency{IssueID: issue, ID: parent, Type: kind}
	}
	for _, test := range []struct {
		name string
		item WorkItem
		want string
	}{
		{
			name: "stated as a field",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Parent: "yoyodyne-ifd.121"},
			want: "yoyodyne-ifd.121",
		},
		{
			name: "stated as an edge",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			// The tracker answering directly beats the edge, so a store that
			// restates one relationship both ways cannot report two parents.
			name: "stated both ways",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Parent: "yoyodyne-ifd.121", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			name: "an edge the tracker did not attribute",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			// A listing that carried the epic's children beside it must not read
			// as the epic having been broken out of one of them.
			name: "an edge belonging to another item",
			item: WorkItem{ID: "yoyodyne-ifd.121", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "",
		},
		{
			name: "a blocker is not a parent",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121.1", "blocks")}},
			want: "",
		},
		{
			name: "work nothing was broken out of",
			item: WorkItem{ID: "yoyodyne-ifd.256"},
			want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.item.DecomposedFrom(); got != test.want {
				t.Fatalf("DecomposedFrom() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientCreatesAWorkItemAndReportsTheIdentifierItGot(t *testing.T) {
	t.Parallel()

	// Creation answers with the one item it made rather than with a list.
	created := `{"id":"yoyodyne-9","title":"Pause on a usage limit","description":"Wait and resume.",
	             "notes":"Proposed in conversation chat-1","status":"open","priority":2,"issue_type":"task"}`
	runner := &fakeRunner{responses: []string{created}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Create(context.Background(), NewWorkItem{
		Title:       "Pause on a usage limit",
		Description: "Wait and resume.",
		Type:        "task",
		Notes:       "Proposed in conversation chat-1",
		Parent:      "yoyodyne-ifd.12",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if item.ID != "yoyodyne-9" || item.Title != "Pause on a usage limit" {
		t.Fatalf("Create() = %#v", item)
	}
	wantArgs := [][]string{{
		"create",
		"--title=Pause on a usage limit",
		"--description=Wait and resume.",
		"--type=task",
		"--notes=Proposed in conversation chat-1",
		"--parent=yoyodyne-ifd.12",
		"--json",
	}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// Work is admitted at a place in the backlog's order, and the highest place is
	// zero, so an unstated priority and the top of the queue cannot be the same
	// request. A creation that says nothing about it asks bd for its default.
	top := 0
	placed := &fakeRunner{responses: []string{created}}
	if _, err := (Client{Runner: placed}).Create(context.Background(), NewWorkItem{
		Title: "Pause on a usage limit", Description: "Wait and resume.", Type: "task", Priority: &top,
	}); err != nil {
		t.Fatalf("Create() with a priority error = %v", err)
	}
	wantPlaced := [][]string{{
		"create",
		"--title=Pause on a usage limit",
		"--description=Wait and resume.",
		"--type=task",
		"--priority=0",
		"--json",
	}}
	if !reflect.DeepEqual(placed.args, wantPlaced) {
		t.Fatalf("bd args = %#v, want %#v", placed.args, wantPlaced)
	}

	// An item created as something other than what was asked for is a failure:
	// the caller approved the item it described, not whatever bd produced.
	mismatched := &fakeRunner{responses: []string{`{"id":"yoyodyne-9","title":"Something else","issue_type":"task"}`}}
	if _, err := (Client{Runner: mismatched}).Create(context.Background(), NewWorkItem{Title: "Pause", Description: "d", Type: "task"}); err == nil ||
		!strings.Contains(err.Error(), "want \"Pause\"") {
		t.Fatalf("Create() mismatched title error = %v", err)
	}
	// A creation without an identifier is not a creation anyone can refer to.
	anonymous := &fakeRunner{responses: []string{`{"title":"Pause"}`}}
	if _, err := (Client{Runner: anonymous}).Create(context.Background(), NewWorkItem{Title: "Pause", Description: "d", Type: "task"}); err == nil ||
		!strings.Contains(err.Error(), "invalid Beads issue id") {
		t.Fatalf("Create() anonymous error = %v", err)
	}
}

func TestClientRefusesToCreateAnUnusableWorkItem(t *testing.T) {
	t.Parallel()

	outsideScale := MaxPriority + 1
	for _, test := range []struct {
		name string
		item NewWorkItem
		want string
	}{
		{name: "no title", item: NewWorkItem{Description: "d", Type: "task"}, want: "title is required"},
		{name: "no description", item: NewWorkItem{Title: "t", Type: "task"}, want: "description is required"},
		{name: "no type", item: NewWorkItem{Title: "t", Description: "d"}, want: "invalid Beads issue type"},
		{name: "smuggled type", item: NewWorkItem{Title: "t", Description: "d", Type: "task --force"}, want: "invalid Beads issue type"},
		{name: "invented parent", item: NewWorkItem{Title: "t", Description: "d", Type: "task", Parent: "../etc"}, want: "invalid parent"},
		{name: "priority outside the scale", item: NewWorkItem{Title: "t", Description: "d", Type: "task", Priority: &outsideScale}, want: "outside 0..4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).Create(context.Background(), test.item); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Create() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused item still ran bd %#v", runner.args)
			}
		})
	}
}

func TestClientRejectsInvalidIDsAndEmptyUpdates(t *testing.T) {
	t.Parallel()

	client := Client{Runner: &fakeRunner{}}
	if _, err := client.Show(context.Background(), "../escape"); err == nil {
		t.Fatal("Show() error = nil")
	}
	if _, err := client.RecordOutcome(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("RecordOutcome() error = nil")
	}
	if err := client.AddBlocker(context.Background(), "yoyodyne-1", "bad/id"); err == nil {
		t.Fatal("AddBlocker() error = nil")
	}
	if _, err := client.Complete(context.Background(), "yoyodyne-1", ""); err == nil {
		t.Fatal("Complete() error = nil")
	}
}

func TestClientReportsProcessAndMalformedJSONErrors(t *testing.T) {
	t.Parallel()

	failed := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "blocked\n"}}}
	if _, err := (Client{Runner: failed}).Show(context.Background(), "yoyodyne-1"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("Show() failure error = %v", err)
	}

	malformed := &fakeRunner{responses: []string{"{"}}
	if _, err := (Client{Runner: malformed}).Show(context.Background(), "yoyodyne-1"); err == nil || !strings.Contains(err.Error(), "decode bd work item") {
		t.Fatalf("Show() malformed error = %v", err)
	}
}

// The tracker is where a work item's price lives, so what is written has to be
// exactly what was priced and has to be verified against what bd echoes back.
func TestClientRecordsAndReadsBackAWorkItemPrice(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{costJSON(27.93, 2, 1)}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 27.93, Runs: 2, UnknownRuns: 1})
	if err != nil {
		t.Fatalf("RecordCost() error = %v", err)
	}
	if item.Cost == nil || item.Cost.TotalUSD != 27.93 || item.Cost.Runs != 2 || item.Cost.UnknownRuns != 1 {
		t.Fatalf("RecordCost() = %#v", item.Cost)
	}
	if item.Cost.Complete() {
		t.Fatal("a price with an unpriced run behind it must not read as complete")
	}
	wantArgs := [][]string{{
		"update", "yoyodyne-1",
		"--set-metadata=yoyodyne_cost_usd=27.930000",
		"--set-metadata=yoyodyne_cost_runs=2",
		"--set-metadata=yoyodyne_cost_unknown_runs=1",
		"--json",
	}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A price bd did not actually store must not read as recorded: an item that
	// looks priced and is not is how a ledger silently goes wrong.
	unstored := &fakeRunner{responses: []string{costJSON(3.5, 2, 1)}}
	if _, err := (Client{Runner: unstored}).RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 27.93, Runs: 2, UnknownRuns: 1}); err == nil ||
		!strings.Contains(err.Error(), "after being priced") {
		t.Fatalf("RecordCost() unstored error = %v", err)
	}
	unpriced := &fakeRunner{responses: []string{workItemJSON("closed", "")}}
	if _, err := (Client{Runner: unpriced}).RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 1, Runs: 1}); err == nil ||
		!strings.Contains(err.Error(), "carries no cost") {
		t.Fatalf("RecordCost() unpriced error = %v", err)
	}
}

func TestClientRefusesPricesItCannotMean(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		cost Cost
		want string
	}{
		{name: "no runs", cost: Cost{TotalUSD: 1}, want: "at least one run"},
		{name: "negative", cost: Cost{TotalUSD: -1, Runs: 1}, want: "cannot be negative"},
		{name: "not a number", cost: Cost{TotalUSD: math.NaN(), Runs: 1}, want: "not a number"},
		{name: "more unknown than run", cost: Cost{TotalUSD: 1, Runs: 1, UnknownRuns: 2}, want: "cannot be unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).RecordCost(context.Background(), "yoyodyne-1", test.cost); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("RecordCost() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused price still ran bd %#v", runner.args)
			}
		})
	}
	if _, err := (Client{Runner: &fakeRunner{}}).RecordCost(context.Background(), "../escape", Cost{TotalUSD: 1, Runs: 1}); err == nil {
		t.Fatal("RecordCost() invalid id error = nil")
	}
}

// Metadata the harness did not write, or wrote only half of, is not a price. An
// item with a total and nothing saying what it covers is reported as unpriced
// rather than as cheap.
func TestClientReadsOnlyACompleteRecordedPrice(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		metadata string
	}{
		{name: "no metadata", metadata: ""},
		{name: "somebody else's metadata", metadata: `,"metadata":{"team":"platform"}`},
		{name: "total without the runs it covers", metadata: `,"metadata":{"yoyodyne_cost_usd":12.5}`},
		{name: "a total that is not a number", metadata: `,"metadata":{"yoyodyne_cost_usd":"free","yoyodyne_cost_runs":1}`},
		{name: "a price no run could have produced", metadata: `,"metadata":{"yoyodyne_cost_usd":12.5,"yoyodyne_cost_runs":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{responses: []string{
				fmt.Sprintf(`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task"%s}]`, test.metadata),
			}}
			item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
			if err != nil {
				t.Fatalf("Show() error = %v", err)
			}
			if item.Cost != nil {
				t.Fatalf("Show() cost = %#v, want none", item.Cost)
			}
		})
	}

	// The unknown count is absent from an item all of whose runs were priced,
	// because bd stores no key it was never given.
	runner := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task","metadata":{"yoyodyne_cost_usd":12.5,"yoyodyne_cost_runs":2}}]`}}
	item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.Cost == nil || !item.Cost.Complete() || item.Cost.TotalUSD != 12.5 || item.Cost.Runs != 2 {
		t.Fatalf("Show() cost = %#v", item.Cost)
	}
}

// The attribution lives in an item's notes, and notes are what a careless
// writer replaces wholesale. So a write that puts a goal into them also tells
// the tracker, in metadata the same write cannot reach, which goal was written
// here — and a read gives that back. Without it an item whose goal was destroyed
// is indistinguishable from one that never had a goal, which is the one state
// the audit deliberately does not fail; without the words, it is distinguishable
// and still unrecoverable.
//
// The two spellings are bd's, not a choice: it takes an item's whole metadata as
// JSON when the item is created and one key at a time when it is updated. Both
// were run against a real bd, which stores a `--set-metadata=key=value` split at
// the first `=` and returns the rest verbatim; a replace-style
// `bd update --notes=` was confirmed to leave the metadata standing.
func TestAWrittenGoalIsWitnessedWhereReplacingTheNotesCannotReachIt(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	created := `{"id":"yoyodyne-9","title":"Triage docket","description":"Stopped work reaches the development manager.",
	             "status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_goal_recorded":"` + autonomy + `"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title:       "Triage docket",
		Description: "Stopped work reaches the development manager.",
		Type:        "task",
		Notes:       "Created under yoyodyne-ifd.102, decomposing it.\n\n" + goal.Note(autonomy),
		Parent:      "yoyodyne-ifd.102",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_goal_recorded":"`+autonomy+`"}`) {
		t.Fatalf("the creation carried no witness: %#v", runner.args[0])
	}
	// The words, not a flag: what a destroyed attribution is put back from.
	if item.GoalWitness != (goal.Witness{Recorded: true, Statement: autonomy}) {
		t.Fatalf("Create() = %#v, want the goal witnessed", item.GoalWitness)
	}

	// An item that acquires its goal later is witnessed by the same write that
	// appends it, so an attribution made after the fact is no less protected than
	// one made at creation.
	attributed := &fakeRunner{responses: []string{`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_goal_recorded":"` + autonomy + `"}}]`}}
	if _, err := (Client{Runner: attributed}).Update(context.Background(), "yoyodyne-4", WorkItemChange{
		AppendNotes: "Attributed to a goal.\n\n" + goal.Note(autonomy),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(attributed.args[0], "--set-metadata=yoyodyne_goal_recorded="+autonomy) {
		t.Fatalf("the attribution carried no witness: %#v", attributed.args[0])
	}

	// A statement longer than a goals document may state is witnessed without its
	// words rather than stored cut in half: half a goal is not the goal, and it
	// would be put back as though it were.
	long := strings.Repeat("a", goal.MaxStatementBytes+1)
	oversized := &fakeRunner{responses: []string{`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_goal_recorded":1}}]`}}
	witnessed, err := (Client{Runner: oversized}).Update(context.Background(), "yoyodyne-4", WorkItemChange{AppendNotes: goal.Note(long)})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(oversized.args[0], "--set-metadata=yoyodyne_goal_recorded=1") {
		t.Fatalf("an oversized goal was stored rather than witnessed bare: %#v", oversized.args[0])
	}
	if witnessed.GoalWitness != (goal.Witness{Recorded: true}) {
		t.Fatalf("Update() = %#v, want a witness carrying no words", witnessed.GoalWitness)
	}

	// A write that records no goal witnesses none. The witness says a goal was
	// written, and an item that got a note about anything else must not read
	// afterwards as one whose attribution was destroyed.
	plain := &fakeRunner{responses: []string{`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task"}]`}}
	updated, err := (Client{Runner: plain}).Update(context.Background(), "yoyodyne-4", WorkItemChange{AppendNotes: "Noted: the reviewer asked for evidence."})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	for _, argument := range plain.args[0] {
		if strings.HasPrefix(argument, "--set-metadata=yoyodyne_goal_recorded=") {
			t.Fatalf("a note carrying no goal witnessed one: %#v", plain.args[0])
		}
	}
	if updated.GoalWitness.Recorded {
		t.Fatalf("Update() = %#v, want no witness", updated.GoalWitness)
	}
}

// A goal already recorded in an item's own notes can be witnessed after the
// fact. It is what covers work attributed before the witness existed, which is
// otherwise protected by nothing at all, and it decides nothing: the statement
// is the item's own, read off it and copied where replacing the notes cannot
// reach it.
func TestAGoalAlreadyRecordedOnAnItemCanBeWitnessedAfterTheFact(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	runner := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.102.2","title":"Triage docket","status":"open","priority":1,"issue_type":"task",` +
			`"notes":"Goal served: ` + autonomy + `","metadata":{"yoyodyne_goal_recorded":"` + autonomy + `"}}]`,
	}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).RecordGoalWitness(context.Background(), "yoyodyne-ifd.102.2", autonomy)
	if err != nil {
		t.Fatalf("RecordGoalWitness() error = %v", err)
	}
	want := []string{"update", "yoyodyne-ifd.102.2", "--set-metadata=yoyodyne_goal_recorded=" + autonomy, "--json"}
	if !reflect.DeepEqual(runner.args[0], want) {
		t.Fatalf("bd args = %#v, want %#v", runner.args[0], want)
	}
	if item.GoalWitness.Statement != autonomy {
		t.Fatalf("RecordGoalWitness() = %#v", item.GoalWitness)
	}

	// A witness bd did not actually store is a failure rather than a reported
	// success: an item believed covered and not covered is worse than one known
	// to be uncovered.
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).RecordGoalWitness(context.Background(), "yoyodyne-4", autonomy); err == nil {
		t.Fatal("RecordGoalWitness() accepted a witness the tracker did not store")
	}
	// And there is no goal to witness on an item recording none, so asking is a
	// mistake rather than a bare marker written over work nobody has attributed.
	if _, err := (Client{Runner: &fakeRunner{}}).RecordGoalWitness(context.Background(), "yoyodyne-4", "  "); err == nil {
		t.Fatal("RecordGoalWitness() accepted an empty goal")
	}
}

// The witness is read as "the tracker holds this key", because the harness
// writes it two ways and bd stores what it is given. A stricter reading would
// turn the tracker's own coercion into a destroyed attribution reported as an
// item nobody has attributed yet, which is the failure being guarded against.
func TestTheGoalWitnessIsReadHoweverTheTrackerStoredIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		stored string
		want   goal.Witness
	}{
		{`{"yoyodyne_goal_recorded":"Run development nearly autonomously."}`, goal.Witness{Recorded: true, Statement: "Run development nearly autonomously."}},
		{`{"yoyodyne_goal_recorded":1}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":"1"}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":true}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":0}`, goal.Witness{}},
		{`{"yoyodyne_goal_recorded":""}`, goal.Witness{}},
		{`{"yoyodyne_goal_recorded":null}`, goal.Witness{}},
		{`{"team":"platform"}`, goal.Witness{}},
		{`{}`, goal.Witness{}},
	} {
		t.Run(test.stored, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{responses: []string{
				`[{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task","metadata":` + test.stored + `}]`,
			}}
			item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
			if err != nil {
				t.Fatalf("Show() error = %v", err)
			}
			if item.GoalWitness != test.want {
				t.Fatalf("Show() witness = %#v, want %#v", item.GoalWitness, test.want)
			}
		})
	}
}

// What carries a work item is written where replacing its notes cannot reach
// it, and for a sharper reason than the goal witness beside it: this one is read
// by selection, so a marker the next recorded outcome could overwrite would stop
// working exactly when a run wrote on the item. The two spellings are the same
// two the witness uses, which were run against a real bd.
func TestTheExecutorIsWrittenWhereSelectionCanReadItAndTheNotesCannot(t *testing.T) {
	t.Parallel()

	created := `{"id":"yoyodyne-ifd.138","title":"Promote the brief","description":"The architect promotes it.",
	             "status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"conversation:architect"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title:       "Promote the brief",
		Description: "The architect promotes it.",
		Type:        "task",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_executor":"conversation:architect"}`) {
		t.Fatalf("the creation carried no executor: %#v", runner.args[0])
	}
	if item.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Create() executor = %q, want the marker read back", item.Executor)
	}

	// An item admitted before the marker existed acquires one by an update, which
	// is how the queue that provoked this gets marked at all.
	marked := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.138","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"conversation:architect"}}]`,
	}}
	if _, err := (Client{Runner: marked}).Update(context.Background(), "yoyodyne-ifd.138", WorkItemChange{
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(marked.args[0], "--set-metadata=yoyodyne_executor=conversation:architect") {
		t.Fatalf("the update carried no executor: %#v", marked.args[0])
	}

	// A marker bd did not actually store is a failure rather than a reported
	// success, on both doors: what rests on it is that nothing selects the item
	// afterwards, and a caller told it was marked would believe the item covered
	// by exactly the guard it is not covered by. Admission is the sharper of the
	// two, because the item is in the queue and pullable the moment the call
	// returns.
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.138","title":"t","status":"open","priority":1,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-ifd.138", WorkItemChange{
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err == nil {
		t.Fatal("Update() with an executor bd did not store = nil error, want a failure")
	}
	unmarked := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.138","title":"Promote the brief","status":"open","priority":1,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unmarked}).Create(context.Background(), NewWorkItem{
		Title: "Promote the brief", Description: "d", Type: "task", Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err == nil {
		t.Fatal("Create() with an executor bd did not store = nil error, want a failure")
	}

	// A creation that says nothing about an executor writes no metadata at all,
	// so ordinary work is unaffected by any of this.
	ordinary := &fakeRunner{responses: []string{`{"id":"yoyodyne-1","title":"Implement feature","status":"open","priority":1,"issue_type":"task"}`}}
	plain, err := (Client{Runner: ordinary}).Create(context.Background(), NewWorkItem{
		Title: "Implement feature", Description: "d", Type: "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, argument := range ordinary.args[0] {
		if strings.HasPrefix(argument, "--metadata=") {
			t.Fatalf("an ordinary creation carried %q", argument)
		}
	}
	if !plain.Executor.DeveloperRun() {
		t.Fatalf("Create() executor = %q, want ordinary work to be a developer run", plain.Executor)
	}
}

// An executor the harness does not recognize is refused where it is written and
// carried where it is read, and the asymmetry is deliberate. Refusing the write
// is what keeps the case rare; reading it as work no run may take is what makes
// a typo cost nothing worse than an item nobody pulls, rather than the run this
// whole marker exists to save.
func TestAnUnrecognizedExecutorIsRefusedOnAWriteAndSurvivesARead(t *testing.T) {
	t.Parallel()

	client := Client{Runner: &fakeRunner{}}
	if _, err := client.Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Executor: "architect",
	}); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("Create() with an unknown executor error = %v, want it refused by name", err)
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Executor: "architect"}); err == nil ||
		!strings.Contains(err.Error(), "executor") {
		t.Fatalf("Update() with an unknown executor error = %v, want it refused by name", err)
	}

	stored := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"architect"}}]`,
	}}
	item, err := (Client{Runner: stored}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.Executor.DeveloperRun() {
		t.Fatalf("Show() executor = %q, want a marker nobody recognizes still to mean not-a-developer-run", item.Executor)
	}
}

// A parking is written where selection can read it, for the reason the executor
// beside it is: the convention it replaces lived in a priority and in whoever
// remembered setting it, and a queue that drains reads neither. Releasing it
// writes the same key with nothing in it, so both directions are one shape.
func TestAParkingIsWrittenWhereSelectionCanReadItAndReleasedTheSameWay(t *testing.T) {
	t.Parallel()

	const reason = "off the critical path by the scope decision"
	created := `{"id":"yoyodyne-ifd.6","title":"The thin Codex backend","description":"d",
	             "status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"` + reason + `"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title: "The thin Codex backend", Description: "d", Type: "task", Parking: reason,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_parked":"`+reason+`"}`) {
		t.Fatalf("the creation carried no parking: %#v", runner.args[0])
	}
	if !item.Parking.Parked() || item.Parking.Reason() != reason {
		t.Fatalf("Create() parking = %q, want the reason read back", item.Parking)
	}

	// The queue is older than the marker, so work already admitted acquires one by
	// an update. That is how the set parked by convention gets parked in fact.
	parked := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"` + reason + `"}}]`,
	}}
	parking := domain.WorkItemParking(reason)
	if _, err := (Client{Runner: parked}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &parking}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(parked.args[0], "--set-metadata=yoyodyne_parked="+reason) {
		t.Fatalf("the update carried no parking: %#v", parked.args[0])
	}

	// Releasing sets the same key to nothing, and an item whose key is empty reads
	// exactly like one nobody ever parked.
	released := domain.WorkItemParking("")
	freeing := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":""}}]`,
	}}
	back, err := (Client{Runner: freeing}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &released})
	if err != nil {
		t.Fatalf("Update() releasing error = %v", err)
	}
	if !slices.Contains(freeing.args[0], "--set-metadata=yoyodyne_parked=") {
		t.Fatalf("the release carried no parking: %#v", freeing.args[0])
	}
	if back.Parking.Parked() {
		t.Fatalf("Update() parking = %q after a release, want it unparked", back.Parking)
	}

	// A creation that says nothing about parking writes no metadata at all, so
	// ordinary work is unaffected.
	ordinary := &fakeRunner{responses: []string{`{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task"}`}}
	plain, err := (Client{Runner: ordinary}).Create(context.Background(), NewWorkItem{Title: "t", Description: "d", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if plain.Parking.Parked() {
		t.Fatalf("Create() parking = %q, want ordinary work unparked", plain.Parking)
	}
}

// Both directions are verified against what bd echoed back, because both have
// something resting on them. A parking that did not take leaves work the
// operator was told is parked sitting pullable in a queue that drains; a release
// that did not take leaves work nobody can start and nothing saying why.
func TestAParkingBdDidNotStoreIsAFailureInBothDirections(t *testing.T) {
	t.Parallel()

	parking := domain.WorkItemParking("deferred by the scope decision")
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &parking}); err == nil {
		t.Fatal("Update() with a parking bd did not store = nil error, want a failure")
	}
	unmarked := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unmarked}).Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Parking: parking,
	}); err == nil {
		t.Fatal("Create() with a parking bd did not store = nil error, want a failure")
	}

	released := domain.WorkItemParking("")
	stuck := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"deferred by the scope decision"}}]`,
	}}
	_, err := (Client{Runner: stuck}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &released})
	if err == nil || !strings.Contains(err.Error(), "still parked") {
		t.Fatalf("Update() releasing work bd left parked = %v, want it reported as still parked", err)
	}

	// A reason the tracker could not hold as one value on one line is refused
	// before anything is written, rather than stored as half a decision.
	client := Client{Runner: &fakeRunner{}}
	wrapped := domain.WorkItemParking("deferred\nuntil team mode is scoped")
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Parking: &wrapped}); err == nil ||
		!strings.Contains(err.Error(), "cannot span lines") {
		t.Fatalf("Update() with a multi-line parking = %v, want it refused", err)
	}
	long := domain.WorkItemParking(strings.Repeat("a", domain.MaxWorkItemParkingBytes+1))
	if _, err := client.Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Parking: long,
	}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Create() with an oversized parking = %v, want it refused", err)
	}
}

// An update that only releases a parking is an update: it changes something, and
// refusing it as empty would leave parked work with no way back into the queue.
func TestReleasingAParkingIsAChangeAnUpdateAccepts(t *testing.T) {
	t.Parallel()

	released := domain.WorkItemParking("")
	if err := (WorkItemChange{Parking: &released}).validate(); err != nil {
		t.Fatalf("a release was refused as an empty update: %v", err)
	}
	if err := (WorkItemChange{}).validate(); err == nil {
		t.Fatal("an update changing nothing was accepted")
	}
}

type fakeRunner struct {
	responses []string
	results   []execution.ProcessResult
	args      [][]string
}

func (f *fakeRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	f.args = append(f.args, append([]string(nil), command.Args...))
	index := len(f.args) - 1
	if index < len(f.results) {
		return f.results[index], nil
	}
	if index >= len(f.responses) {
		return execution.ProcessResult{}, fmt.Errorf("unexpected command %v", command.Args)
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: f.responses[index]}, nil
}

// costJSON is what bd echoes back after a price is stored: the item, with the
// price among whatever else the project keeps in its metadata.
func costJSON(total float64, runs, unknown int) string {
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "status":"closed",
  "priority":1,
  "issue_type":"task",
  "metadata":{"team":"platform","yoyodyne_cost_usd":%v,"yoyodyne_cost_runs":%d,"yoyodyne_cost_unknown_runs":%d}
}]`, total, runs, unknown)
}

func workItemJSON(status, notes string) string {
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "description":"Use docs/v1-harness-design.md",
  "design":"Bounded change",
  "acceptance_criteria":"Tests pass",
  "notes":%q,
  "status":%q,
  "priority":1,
  "issue_type":"task",
  "dependencies":[{"id":"yoyodyne-parent","dependency_type":"parent-child","status":"closed"}]
}]`, notes, status)
}
