package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The hold the scheduler is actually subject to. `yoyo work` is the harness
// choosing, so a held intake stops it before it reads the tracker at all --
// nothing is claimed, nothing is developed, and the operator is told the two
// ways out.
func TestWorkStartsNothingWhileIntakeIsHeld(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// hold it reads is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	intake, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold("the queue needs reordering first", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "work", "--config", configPath)
	if code != 0 {
		t.Fatalf("work code = %d, stderr = %q", code, stderr)
	}
	// The remedy is named as something runnable from this terminal: `/release`
	// is said beside it rather than instead of it, because whoever is reading
	// this may have no conversation open.
	for _, want := range []string{"nothing was started", "holding intake", "the queue needs reordering first", "yoyo release", "/release", "yoyo run"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("work stdout = %q, want it to mention %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "work", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("work --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Schedule orchestrator.Schedule `json:"schedule"`
		Error    string                `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if output.Error != "" {
		t.Fatalf("error = %q, want a held intake reported as a schedule rather than a failure", output.Error)
	}
	if len(output.Schedule.Started) != 0 || output.Schedule.IntakeHeld == nil {
		t.Fatalf("schedule = %#v, want nothing started and the hold named", output.Schedule)
	}
	if output.Schedule.Stopped != orchestrator.ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be why the choosing stopped", output.Schedule.Stopped)
	}
}

// The verb takes no item: naming one is `yoyo run`, and the difference between
// the two is exactly the hold above.
func TestWorkRefusesArgumentsItCannotActOn(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "yoyodyne-1"); code != 2 {
		t.Fatalf("work with an item code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "--limit", "-1"); code != 2 {
		t.Fatalf("work with a negative limit code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "--budget", "-1"); code != 2 {
		t.Fatalf("work with a negative budget code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	// Staying open and returning when the queue empties are opposite requests,
	// and guessing which was meant is how a session runs all night that was
	// meant to return.
	_, stderr, code := runCLI(t, "work", "--config", configPath, "--watch", "--until-drained")
	if code != 2 {
		t.Fatalf("work with both --watch and --until-drained code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if !strings.Contains(stderr, "opposite things") {
		t.Fatalf("stderr = %q, want the refusal to say why the two cannot both be asked for", stderr)
	}
}

// Watching is what the loop is for, and the default is deliberately still the
// drain: the flip is a decision somebody makes rather than a side effect of this
// landing. Both are stated in the usage so neither is inferred from behaviour.
func TestWorkUsageSaysWhatWatchingIsAndThatDrainingIsTheDefault(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printWorkUsage(&usage)
	for _, want := range []string{
		"--watch",
		"--until-drained",
		"(the default)",
		"execution.work_poll",
		"execution.blocked_runs_before_intake_hold",
		"Holding intake brakes a watching session in place",
		"--budget",
		// A bound that can stop the session has to say so where the flag is
		// documented: an operator who reads "caps what one session spends" and
		// meets a stop instead is reading the wrong promise.
		"--budget fails closed",
		// A session that restarts itself is a process ending and beginning under
		// somebody's terminal, which is exactly the behaviour an operator should
		// not have to discover by watching it happen.
		"also takes up a build deployed over it",
		"restarts into what was deployed",
		// And what a deploy does to a bound the operator set, which is the half
		// somebody reading "caps what one session spends" would otherwise have to
		// find out from a bill.
		"never hands a bounded session its cap back",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("work usage = %q, want it to say %q", usage.String(), want)
		}
	}
}

// What a bounded session hands to the build it restarts into. The bound is the
// operator's and a deploy is not them raising it, so what crosses is the
// remainder: a session $45 into $50 comes back with $4.99 left rather than with
// $50 again, and the same for a count of runs.
func TestARestartCarriesWhatIsLeftOfTheBoundsRatherThanTheWholeOfThem(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args    []string
		budget  float64
		spent   float64
		limit   int
		started int
		want    []string
	}{
		"a budget and a limit given as separate arguments": {
			args:    []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "50", "--limit", "10"},
			budget:  50,
			spent:   45.01,
			limit:   10,
			started: 6,
			want:    []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "4.99", "--limit", "4"},
		},
		"the same bounds written with equals signs and one dash": {
			args:    []string{"./bin/yoyo", "work", "--watch", "--budget=50", "-limit=10"},
			budget:  50,
			spent:   12.5,
			limit:   10,
			started: 1,
			want:    []string{"./bin/yoyo", "work", "--watch", "--budget=37.50", "-limit=9"},
		},
		"an unbounded session, which has nothing to reduce": {
			args: []string{"./bin/yoyo", "work", "--watch", "--json"},
			want: []string{"./bin/yoyo", "work", "--watch", "--json"},
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := continued(test.args, test.budget, test.spent, test.limit, test.started)
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("continued() = %v, want %v", got, test.want)
			}
		})
	}
}

// The remainder is written exactly rather than rounded to cents, because a
// remainder under a cent rounded to "0.00" is how an unbounded session is asked
// for -- a bound that quietly became no bound at all.
func TestARemainderUnderACentIsStillABound(t *testing.T) {
	t.Parallel()

	got := continued([]string{"yoyo", "work", "--watch", "--budget", "50"}, 50, 49.999, 0, 0)
	if got[4] == "0.00" || got[4] == "0" {
		t.Fatalf("continued() left the budget as %q, which asks for an unbounded session", got[4])
	}
	if _, err := strconv.ParseFloat(got[4], 64); err != nil {
		t.Fatalf("budget %q does not read back as a number: %v", got[4], err)
	}
}

// The session's account of itself lands in the product's own watch log, under
// an identifier nothing else is using. It is the seam between a scheduler that
// knows nothing about products and a record that is keyed by one.
func TestAWatchSessionWritesIntoTheProductsOwnWatchLog(t *testing.T) {
	// Not parallel: the state root the log is written under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	sessions, err := openWatchSession(configPath)
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	at := time.Now().UTC()
	if err := sessions.Record(runstate.WatchWatching, at, "watching the backlog until stopped"); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	recorded, err := watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one transition the session recorded", recorded)
	}
	if recorded[0].State != runstate.WatchWatching || recorded[0].ProductID != "yoyodyne" {
		t.Fatalf("transition = %#v, want this product's session recorded as watching", recorded[0])
	}
	if recorded[0].Reason != "watching the backlog until stopped" {
		t.Fatalf("reason = %q, want what the session said about itself", recorded[0].Reason)
	}
	// A second session is a second identity in the same log, so two of them
	// interleaved can still be read apart.
	other, err := openWatchSession(configPath)
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	if err := other.Record(runstate.WatchIdle, at, "the backlog is empty"); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	recorded, err = watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[0].SessionID == recorded[1].SessionID {
		t.Fatalf("recorded = %#v, want two sessions told apart in one log", recorded)
	}
}

// A watching session says what it is doing where somebody who is not at the
// terminal can read it, and `yoyo status` is one of the two places that read it.
// This drives the record and the surface rather than the loop: what the loop
// does with an empty queue is the scheduler's own test.
func TestStatusReportsWhereTheSessionChoosingWorkGotTo(t *testing.T) {
	// Not parallel: the state root the commands address is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// A product nobody has watched says nothing about a session, rather than
	// asserting that nothing is running.
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "session choosing work") {
		t.Fatalf("status stdout = %q, want nothing claimed about a session nobody has run", stdout)
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         runstate.WatchIdle,
		At:            time.Now().UTC(),
		Reason:        "the backlog is empty",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"the session choosing work is idle", "the backlog is empty"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status stdout = %q, want it to say %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Watch *runstate.WatchTransition `json:"watch"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if output.Watch == nil || output.Watch.State != runstate.WatchIdle {
		t.Fatalf("watch = %#v, want the session's state carried machine-readably too", output.Watch)
	}
}

// The answer to "when is a capacity change picked up?" is documented where an
// operator looks for it. It is a decision rather than an accident, so the usage
// states it rather than leaving it to be inferred from behavior.
func TestWorkUsageSaysWhenAConfigurationChangeTakesEffect(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printWorkUsage(&usage)
	for _, want := range []string{
		"re-read before every pull",
		"takes effect at the next selection",
		"keep the configuration they started under",
		"max_concurrent_developers",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("work usage = %q, want it to say %q", usage.String(), want)
		}
	}
}
