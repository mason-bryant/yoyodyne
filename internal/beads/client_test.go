package beads

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
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

	listed := `[{"id":"yoyodyne-1","title":"First","status":"open","priority":1,"issue_type":"task"},
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
