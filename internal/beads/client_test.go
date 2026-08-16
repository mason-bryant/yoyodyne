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
