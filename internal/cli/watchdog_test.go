package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A product reporting nowhere still notices that it has stopped.
//
// This is the whole of yoyodyne-ifd.295: the stall check used to be produced by
// the Slack sink's poll loop, and Slack reporting is opt-in throughout — an
// observation and never a gate — so a product that never started a sink recorded
// no stalls and its `yoyo status` history was permanently empty. The sweep an
// unattended pass already runs is where it lives now, and this walks the whole
// path over a project with no Slack configuration at all: the records say nothing
// has started while work is ready, `yoyo reconcile` records that, and
// `yoyo status` reads it back.
func TestASweepOnAProductWithoutSlackRecordsAStallAndStatusReadsItBack(t *testing.T) {
	// t.Setenv rules out t.Parallel, and the state root has to be this test's own:
	// the sweep reads and writes the product's durable records.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := stalledProject(t, 4)

	stdout, stderr, code := runCLI(t, "reconcile", "--config", configPath)
	if code != 0 {
		t.Fatalf("reconcile code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "nothing has started on this product") {
		t.Fatalf("the sweep said nothing about a product that has stopped:\n%s\n%s", stdout, stderr)
	}
	// The second line is the one to act on: a session still claiming to be
	// watching wants killing first, and a dead one wants starting.
	if !strings.Contains(stdout, "said nothing since") {
		t.Fatalf("the sweep did not say what the thing that chooses work last said:\n%s", stdout)
	}

	// And the record is the product's rather than the sweep's, so the surface an
	// operator asks reads the same stall back.
	status, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, fact := range []string{"nothing has started on this product since", "4 items ready", "said nothing since"} {
		if !strings.Contains(status, fact) {
			t.Fatalf("status does not carry %q:\n%s", fact, status)
		}
	}

	// A second sweep is the ordinary case — an unattended pass runs this every few
	// minutes — and one stall stays one stall.
	if _, stderr, code = runCLI(t, "reconcile", "--config", configPath); code != 0 {
		t.Fatalf("reconcile code = %d, stderr = %q", code, stderr)
	}
	stalls, err := runstate.NewStallStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || !events[0].Open() {
		t.Fatalf("List() = %+v, want the one stall, still standing", events)
	}
}

// A drained queue is a machine with nothing to do rather than one that has
// stopped, and the sweep says nothing at all about it: a line asserting that
// nothing is wrong, on every sweep, is a line nobody reads.
func TestASweepOverADrainedQueueRecordsNoStallAndSaysNothing(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := stalledProject(t, 0)

	stdout, stderr, code := runCLI(t, "reconcile", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reconcile code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	var result reconcileOutput
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout)
	}
	if result.StallProblem != "" {
		t.Fatalf("StallProblem = %q, want the reading made", result.StallProblem)
	}
	if result.Stall == nil || result.Stall.Standing != nil {
		t.Fatalf("Stall = %+v, want a drained queue read as nothing to do", result.Stall)
	}
	if !strings.Contains(result.Stall.Silence.Explains, "nothing ready") {
		t.Fatalf("Explains = %q, want the drained queue named", result.Stall.Silence.Explains)
	}
}

// The threshold is the operator's number, and it is on the sweep that always
// runs rather than on the optional reporting process. There is no way to ask for
// no watchdog: zero would take the default rather than turn anything off, so
// reading it as a switch would silently do the opposite of what somebody meant.
func TestTheStallThresholdIsTheSweepsAndCannotBeUsedToSwitchItOff(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := stalledProject(t, 2)

	// Wider than the quiet stretch these records hold, so there is nothing to
	// record yet.
	stdout, stderr, code := runCLI(t, "reconcile", "--config", configPath, "--stall-after", "6h")
	if code != 0 {
		t.Fatalf("reconcile code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "nothing has started on this product") {
		t.Fatalf("a gap inside the operator's threshold was reported as a stall:\n%s", stdout)
	}

	var out, errs bytes.Buffer
	if code := Run([]string{"reconcile", "--config", configPath, "--stall-after", "0"}, &out, &errs, "test"); code != 2 {
		t.Fatalf("reconcile code = %d, want a refusal; stderr = %q", code, errs.String())
	}
	if !strings.Contains(errs.String(), "rather than a switch") {
		t.Fatalf("stderr = %q, want it to say the threshold is not a way to turn this off", errs.String())
	}
}

// stalledProject writes a project whose durable records say nothing has started
// for two hours while the tracker calls this much work ready — the shape of a
// harness that has stopped — and returns its configuration path.
//
// It configures no Slack at all, which is the point: every product that never
// opted into reporting is this one.
func stalledProject(t *testing.T, ready int) string {
	t.Helper()
	project := t.TempDir()
	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")

	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// A session that said it was watching two hours ago and has said nothing
	// since, which is what a scheduler that died looks like from every record.
	watch, err := runstate.NewWatchStore(os.Getenv("YOYODYNE_STATE_HOME"), "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchWatching,
		At:            time.Now().UTC().Add(-2 * time.Hour),
		Reason:        "watching the backlog until stopped",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	stubTracker(t, ready)
	return configPath
}

// stubTracker puts a `bd` on PATH that reports this many ready items, so a test
// exercises the sweep's own reading of the queue without a tracker store.
func stubTracker(t *testing.T, ready int) {
	t.Helper()
	items := make([]string, 0, ready)
	for index := 0; index < ready; index++ {
		items = append(items, `{"id": "yoyodyne-ifd.`+string(rune('1'+index))+`", "title": "waiting work", "status": "open"}`)
	}
	script := "#!/bin/sh\ncase \"$1\" in\n  ready) echo '[" + strings.Join(items, ",") + "]' ;;\n  *) echo '[]' ;;\nesac\n"
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "bd"), []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}
