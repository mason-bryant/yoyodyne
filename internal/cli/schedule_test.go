package cli

import (
	"encoding/json"
	"fmt"
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

// Neither bound is ever written into a restart as zero, because zero is how both
// of these flags ask for no bound at all -- a cap that came back as its own
// absence. The scheduler stops a session on the bound it reached before it can
// redeploy, and this is the guard that says so out loud rather than assuming it.
func TestARestartIsRefusedWhereABoundHasNothingLeftOfIt(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		budget  float64
		spent   float64
		limit   int
		started int
		want    string
	}{
		"something left of both bounds":     {budget: 50, spent: 45, limit: 10, started: 6, want: ""},
		"no bounds at all":                  {want: ""},
		"the budget exactly spent":          {budget: 50, spent: 50, want: "budget"},
		"more spent than the budget allows": {budget: 50, spent: 51.20, want: "budget"},
		"the last permitted run started":    {limit: 4, started: 4, want: "runs"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := exhausted(test.budget, test.spent, test.limit, test.started)
			if test.want == "" && got != "" {
				t.Fatalf("exhausted() = %q, want a session that may still be restarted", got)
			}
			if test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("exhausted() = %q, want the %s bound named as the one with nothing left of it", got, test.want)
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

// A restart that does not happen leaves nothing in the log saying the session is
// coming back.
//
// The stop is marked as a restart before the restart is known to happen, and it
// has to be: a restart that works never returns to write anything, so the moment
// the session stops is the only moment there is. Both branches that then decline
// to re-execute -- a bound with nothing left of it, and a re-execution the
// operating system refuses -- correct that claim in the same log, rather than
// exiting on a line of stderr that only whoever is at the terminal ever reads.
func TestARestartThatDoesNotHappenLeavesNoRecordSayingTheSessionIsComingBack(t *testing.T) {
	// Not parallel: the state root the log is written under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	sessions, err := openWatchSession(configPath)
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	// The scheduler's own last line, exactly as it writes it: a stop that says the
	// session is on its way back.
	if err := sessions.Record(orchestrator.SessionState{
		State:      runstate.WatchStopped,
		At:         time.Now().UTC(),
		Reason:     orchestrator.ScheduleRedeployed,
		Restarting: true,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	// The operating system refusing the re-execution, which is the case the
	// redeploy package's own tests reach for real: a replaced image that is not
	// executable, a path that went away between the stat and the exec.
	refused := &refusedRestart{args: []string{"/usr/local/bin/yoyo", "work", "--watch"}}
	var said strings.Builder
	if code := takeUpTheDeploy(refused, sessions, &said, 0, 0, 0, 0, 0); code != 1 {
		t.Fatalf("takeUpTheDeploy() = %d, want the failed restart to fail the command", code)
	}
	if !strings.Contains(said.String(), "could not") {
		t.Fatalf("stderr = %q, want whoever is at the terminal told the restart did not happen", said.String())
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	latest, found, err := watch.Latest()
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !found {
		t.Fatalf("Latest() found nothing, want the correction the command recorded")
	}
	if latest.State != runstate.WatchStopped || latest.Restarting {
		t.Fatalf("latest = %#v, want a stop that is an ending rather than one claiming a restart", latest)
	}
	if !strings.Contains(latest.Reason, "could not") {
		t.Fatalf("reason = %q, want it to say why the restart did not happen", latest.Reason)
	}
	// The claim it corrects stays where it was. The log is append-only and read
	// forward, so what a surface gets is the line saying the session is coming
	// back and then the line saying it is not -- rather than a rewritten history
	// in which the claim was never made.
	recorded, err := watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || !recorded[0].Restarting {
		t.Fatalf("recorded = %#v, want the restart claim kept and the ending appended after it", recorded)
	}

	// The other refusal: a bound with nothing left of it, which declines the
	// restart before the re-execution is attempted at all. It leaves the same
	// correction behind, and the command exits on the schedule's own code rather
	// than on a failure, because nothing failed.
	unspent := &refusedRestart{args: []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "50"}}
	said.Reset()
	if code := takeUpTheDeploy(unspent, sessions, &said, 0, 50, 50, 0, 0); code != 0 {
		t.Fatalf("takeUpTheDeploy() = %d, want a session stopped on its bound rather than a failure", code)
	}
	if unspent.attempts != 0 {
		t.Fatalf("Take() was called %d time(s), want a bound that is gone to refuse the restart before it", unspent.attempts)
	}
	latest, _, err = watch.Latest()
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.Restarting || !strings.Contains(latest.Reason, "budget") {
		t.Fatalf("latest = %#v, want an ending naming the bound that refused the restart", latest)
	}
}

// refusedRestart is a deployed binary whose re-execution the operating system
// will not make. It is the only shape of this a test can observe: a Take that
// works never returns.
type refusedRestart struct {
	args     []string
	attempts int
}

func (r *refusedRestart) Args() []string { return append([]string(nil), r.args...) }

func (r *refusedRestart) Take(args []string) error {
	r.attempts++
	return fmt.Errorf("re-execute %s: permission denied", args[0])
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
	if err := sessions.Record(orchestrator.SessionState{State: runstate.WatchWatching, At: at, Reason: "watching the backlog until stopped"}); err != nil {
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
	if err := other.Record(orchestrator.SessionState{State: runstate.WatchIdle, At: at, Reason: "the backlog is empty"}); err != nil {
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

// `yoyo status` says the one stop that is not an ending as itself. Both of the
// two places that read the watch log have to, and this is the other one: a
// reader told only that the session is "stopped" goes looking for somebody to
// start it, which is precisely the move the session took off them by restarting
// itself.
func TestStatusSaysAStopThatIsARestartIsNotAnEnding(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	at := time.Now().UTC()
	record := func(restarting bool, reason string, when time.Time) {
		t.Helper()
		if err := watch.Record(runstate.WatchTransition{
			SchemaVersion: runstate.WatchSchemaVersion,
			ProductID:     "yoyodyne",
			SessionID:     sessionID,
			State:         runstate.WatchStopped,
			At:            when,
			Reason:        reason,
			Restarting:    restarting,
		}); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	record(true, orchestrator.ScheduleRedeployed, at)
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "stopped to restart into the build deployed over it") {
		t.Fatalf("status stdout = %q, want the stop said as a session coming back", stdout)
	}

	// The same log with an ordinary ending on the end of it reads as one. Without
	// this the line above would pass on a command that called every stop a
	// restart, which is the same false reassurance in the other direction.
	record(false, "the operator stopped the session", at.Add(time.Minute))
	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "stopped to restart") {
		t.Fatalf("status stdout = %q, want an ending said as an ending", stdout)
	}
	if !strings.Contains(stdout, "the session choosing work is stopped") {
		t.Fatalf("status stdout = %q, want the session reported as stopped", stdout)
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
