package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/watchdog"
)

// A product reporting nowhere still notices that it has stopped.
//
// This is the whole of yoyodyne-ifd.295: the stall check used to be produced by
// the Slack sink's poll loop, and Slack reporting is opt-in throughout — an
// observation and never a gate — so a product that never started a sink recorded
// no stalls and its `yoyo status` history was permanently empty. It is taken by
// the harness's own loop and by this sweep now, and this walks the whole path
// over a project with no Slack configuration at all: the records say nothing has
// started while work is ready, `yoyo reconcile` records that, and `yoyo status`
// reads it back. The watch loop's half of it is below.
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

// The harness's own loop takes the same reading, gated so a loop that polls in
// seconds does not spawn a tracker process every poll.
//
// This is the case no sweep catches without an operator's cron: a session that
// is alive and has stopped starting anything writes nothing about that, and every
// other surface reads it as a machine with nothing to do.
func TestTheWatchLoopsStallReadingIsGatedAndRecordsTheStallOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	stalls, err := runstate.NewStallStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	// The session this loop belongs to: up for two hours, still writing a
	// transition on every poll, and starting nothing over a queue with work in it.
	// That is the case this invoker exists for — a dead session writes no polls at
	// all, and `yoyo reconcile` is what reads that one.
	began := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchWatching,
		At:            began,
		Reason:        "watching the backlog until stopped",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	for poll := 1; poll <= 8; poll++ {
		if err := watch.Record(runstate.WatchTransition{
			SchemaVersion: runstate.WatchSchemaVersion,
			ProductID:     "yoyodyne",
			SessionID:     "watch-0123456789abcdef0123456789abcdef",
			State:         runstate.WatchIdle,
			At:            began.Add(time.Duration(poll) * 15 * time.Minute),
			Reason:        "nothing pullable this poll",
		}); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	backlog := &countingBacklog{count: 3}
	said := &bytes.Buffer{}
	now := began.Add(2 * time.Hour)
	loop := &stallWatch{
		checker: watchdog.Checker{
			Runs: runs, Sessions: watch, Holds: holds, Intake: intake,
			Backlog: backlog, Stalls: stalls,
			Threshold: readmodel.DefaultStallThreshold,
			Now:       func() time.Time { return now },
		},
		threshold: readmodel.DefaultStallThreshold,
		stderr:    said,
		now:       func() time.Time { return now },
	}

	// An hour of a session polling every fifteen seconds, which is 240 polls.
	for poll := 0; poll < 240; poll++ {
		loop.check(context.Background())
		now = now.Add(15 * time.Second)
	}

	// One reading a threshold rather than one a poll. The figure is reported
	// either way, because what this guards is a regression back towards the
	// unbounded case.
	perHour := int(time.Hour / readmodel.DefaultStallThreshold)
	t.Logf("the tracker was read %d time(s) over an hour of polling, against 240 polls", backlog.asked)
	if backlog.asked > perHour+1 {
		t.Fatalf("the tracker was read %d time(s) over one hour, want about %d", backlog.asked, perHour)
	}
	if backlog.asked == 0 {
		t.Fatal("the tracker was never read, so a stall could not have been noticed at all")
	}

	// And the stall is recorded once, and said once, however many readings agreed
	// with it.
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || !events[0].Open() {
		t.Fatalf("List() = %+v, want the one standing stall", events)
	}
	if got := strings.Count(said.String(), "nothing has started on this product"); got != 1 {
		t.Fatalf("the session said it %d time(s):\n%s", got, said.String())
	}
	if !strings.Contains(said.String(), "said nothing since") {
		t.Fatalf("the session did not say what the thing that chooses work last said:\n%s", said.String())
	}
}

// A reading that cannot be made records nothing and says so, rather than
// guessing in either direction: inventing ready work wakes somebody for nothing,
// and assuming none is the silence the watchdog exists to end.
func TestTheWatchLoopSaysAReadingItCouldNotMakeAndRecordsNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	stalls, err := runstate.NewStallStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	began := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchWatching,
		At:            began,
		Reason:        "watching the backlog until stopped",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	said := &bytes.Buffer{}
	now := began.Add(2 * time.Hour)
	loop := &stallWatch{
		checker: watchdog.Checker{
			Runs: runs, Sessions: watch, Holds: holds, Intake: intake,
			Backlog: refusingBacklog{}, Stalls: stalls,
			Threshold: readmodel.DefaultStallThreshold,
			Now:       func() time.Time { return now },
		},
		threshold: readmodel.DefaultStallThreshold,
		stderr:    said,
		now:       func() time.Time { return now },
	}
	loop.check(context.Background())

	if !strings.Contains(said.String(), "was not decided") {
		t.Fatalf("the session said %q, want it to say the reading was not made", said.String())
	}
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded over a tracker nobody could read", events)
	}
}

// countingBacklog answers what is ready and counts how often it was asked, which
// is the whole of what the gate is about: `bd` is a process the session spawns,
// and how often it spawns one is the thing under test.
type countingBacklog struct {
	count int
	asked int
}

func (b *countingBacklog) Ready(context.Context) (int, error) {
	b.asked++
	return b.count, nil
}

// refusingBacklog is the tracker that will not answer at all.
type refusingBacklog struct{}

func (refusingBacklog) Ready(context.Context) (int, error) {
	return 0, errors.New("bd: executable file not found in $PATH")
}

// The sink says where stalls are noticed on every start, whatever the flags
// were.
//
// It is unconditional because the failure it guards against is silent by
// construction: an installation that had a sink running had a watchdog by having
// this process, and after yoyodyne-ifd.295 it has one only if something takes the
// reading. A product with no stalls and a product nobody is checking look
// identical from a channel, so this is the one moment it can be said to the
// person who started the process.
func TestTheSinkSaysWhereStallsAreNoticedWhenItStarts(t *testing.T) {
	t.Parallel()

	said := &bytes.Buffer{}
	sayWhereStallsAreNoticed(said)
	for _, fact := range []string{"yoyo work --watch", "yoyo reconcile", "records no stalls at all"} {
		if !strings.Contains(said.String(), fact) {
			t.Fatalf("the sink's notice does not carry %q:\n%s", fact, said.String())
		}
	}
}

// A usage string's first back-quoted run is what Go's flag package renders as
// the flag's value placeholder, so a command named in backquotes inside one
// comes out as `-stall-after yoyo reconcile --stall-after`. The retired flag says
// where the threshold went, so it is exactly the usage string most likely to name
// a command, and this pins that it names one without spending the placeholder.
func TestTheRetiredThresholdFlagKeepsItsValuePlaceholder(t *testing.T) {
	t.Parallel()

	var out, errs bytes.Buffer
	if code := Run([]string{"slack", "-h"}, &out, &errs, "test"); code != 2 {
		t.Fatalf("slack -h code = %d, want the usage exit", code)
	}
	if !strings.Contains(errs.String(), "-stall-after duration") {
		t.Fatalf("the flag's placeholder is not its value type:\n%s", errs.String())
	}
}
