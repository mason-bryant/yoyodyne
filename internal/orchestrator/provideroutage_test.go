package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreachableMessage is what the provider CLI wrote on the runs that died in
// the 2026-09-15..18 outage, kept whole so the wait a test observes is the wait
// that message earns.
const unreachableMessage = "API Error: Can't reach the API server"

// providerAwayBackend refuses the developer's first refusals invocations the way
// a provider nobody can reach does, and serves the work afterwards.
func providerAwayBackend(refusals int, cause domain.ProviderOutageCause, verdicts ...string) *fakeBackend {
	return refusingBackend(refusals, func(result backend.RunResult) backend.RunResult {
		result.StopReason = "api_error"
		result.FinalText = unreachableMessage
		result.ProviderOutage = &backend.ProviderOutage{Cause: cause, Detail: "api_error: " + unreachableMessage}
		return result
	}, verdicts...)
}

func newOutageStore(t *testing.T) *runstate.ProviderOutageStore {
	t.Helper()
	store, err := runstate.NewProviderOutageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	return store
}

// The operator's directive of 2026-09-18: a run interrupted by a provider nobody
// can reach or nobody is logged into is not killed. It waits, spending nothing —
// no relaunch, no repair attempt, no pause budget, no blocker — with its claim,
// branch, worktree, and session kept, and finishes when the provider answers
// with every counter untouched.
func TestRunWaitsOutAProviderNobodyCanReachSpendingNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// More refusals than the relaunch budget could have paid for, so what the run
	// is doing while it waits is provably not relaunching.
	provider := providerAwayBackend(4, domain.ProviderUnreachable, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &pausingClock{now: baseTime}
	// A pause budget far smaller than the wait, so the wait is provably not
	// charged to it: a limit waited this long would have blocked on it.
	pipeline = waiting(automatic(pipeline, provider), clock, 10*time.Minute, 10*time.Minute)
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	pipeline.Config.Execution.UsageLimitUnknownResetPause = config.Duration(30 * time.Minute)
	outages := newOutageStore(t)
	pipeline.ProviderOutages = outages

	// The wait has to be on disk before it starts, so a process that dies
	// mid-wait comes back to a run that is still waiting rather than one that
	// failed; and the product has to say the provider is away while it stands.
	var pausedState runstate.State
	var standing runstate.ProviderOutage
	clock.onSleep = func() {
		loaded, err := store.Load(pipelineRunID)
		if err != nil {
			t.Errorf("Load() during the wait error = %v", err)
			return
		}
		pausedState = loaded
		outage, away, err := outages.Standing()
		if err != nil || !away {
			t.Errorf("Standing() during the wait = %t, %v, want the outage recorded on the product", away, err)
			return
		}
		standing = outage
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if pausedState.PauseCause != runstate.PauseProviderUnreachable || pausedState.UsageLimitResetsAt == nil {
		t.Fatalf("paused state = %#v, want a run recorded as waiting on a provider nobody can reach, with its next probe durable", pausedState)
	}
	if !pausedForUsageLimit(pausedState) {
		t.Fatal("the waiting run is not one a later process would resume, so a process dying mid-wait would strand it")
	}
	// Four refusals, each waited out on the probe interval, and none of the wait
	// charged anywhere: the pause budget is untouched, and so are the counters.
	if clock.waited() != 4*30*time.Minute {
		t.Fatalf("waited %s in total, want four probe intervals", clock.waited())
	}
	if pausedState.UsageLimitPausedSeconds != 0 {
		t.Fatalf("committed %s to the pause budget, want the wait to spend nothing", pausedState.UsageLimitPaused())
	}
	if standing.Cause != domain.ProviderUnreachable || standing.Refusals < 1 || !strings.Contains(standing.Waiting, pipelineRunID) {
		t.Fatalf("outage on the product = %#v, want the run named as what is waiting", standing)
	}
	if outcome.Integration == nil || !tracker.closed || tracker.blocked {
		t.Fatalf("the waited-out run did not complete normally: %#v (blocked=%t)", outcome, tracker.blocked)
	}
	if outcome.TransientRelaunches != 0 || outcome.RepairAttempts != 0 {
		t.Fatalf("outcome = %#v, want the relaunch and repair counters untouched once the provider returned", outcome)
	}
	developerRequests := provider.requestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 5 {
		t.Fatalf("developer invocations = %d, want the four refused attempts and the one that was served", len(developerRequests))
	}
	// Every reissue continues the session the refused attempt established.
	if developerRequests[4].SessionID != provider.developerSession {
		t.Fatalf("reissued attempt session = %q, want %q", developerRequests[4].SessionID, provider.developerSession)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.UsageLimitResetsAt != nil || finished.PauseCause != "" || finished.TransientRelaunches != 0 {
		t.Fatalf("a finished run still reads as waiting or as relaunched: %#v", finished)
	}
	// Two hours of waiting against a ten-minute pause budget, and none of it
	// committed: a later genuine limit on this run would otherwise block at once
	// on a budget the outage spent. Asserted on the finished record rather than
	// only on a mid-sleep snapshot, so every probe's exit path is covered.
	if finished.UsageLimitPausedSeconds != 0 || finished.UsageLimitPaused() != 0 {
		t.Fatalf("finished run committed %s to the pause budget, want nothing", finished.UsageLimitPaused())
	}
	// The attempt the provider served is what ends the outage for every surface.
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after the provider answered = %t, %v, want the outage cleared", away, err)
	}
}

// A dispatch refused at the availability check is a wait too: nothing is
// claimed, the outage is recorded on the product, and the refusal is typed so
// the scheduler counts it toward nothing. A provider that reports itself logged
// in again clears it, on the same check.
func TestADispatchIntoAnExpiredLoginIsAWaitRatherThanAFailure(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &fakeBackend{availability: backend.Availability{Installed: true, Authenticated: false, AuthMethod: "none"}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	outages := newOutageStore(t)
	pipeline.ProviderOutages = outages

	_, err := pipeline.Run(context.Background(), tracker.item.ID)
	var away ProviderOutageError
	if !errors.As(err, &away) || away.Cause != domain.ProviderUnauthenticated {
		t.Fatalf("Run() error = %v, want the dispatch refused as a wait on the login", err)
	}
	if !strings.Contains(err.Error(), "the operator must log in") || !strings.Contains(err.Error(), "the claude-code backend is not authenticated") {
		t.Fatalf("Run() error = %v, want the wait named and the login it needs", err)
	}
	if tracker.claimed {
		t.Fatal("a dispatch the provider turned away claimed the item")
	}
	standing, recorded, err := outages.Standing()
	if err != nil || !recorded || standing.Cause != domain.ProviderUnauthenticated || !strings.Contains(standing.Waiting, tracker.item.ID) {
		t.Fatalf("Standing() = %#v, %t, %v, want the login recorded on the product with the dispatch named", standing, recorded, err)
	}

	// The operator logs in. The next dispatch finds the provider ready, clears
	// the outage on that evidence, and runs the item exactly as it would have.
	provider.availability = backend.Availability{Installed: true, Authenticated: true}
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper {
			if err := os.WriteFile(request.WorkingDirectory+"/feature.txt", []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: "session", ResolvedModel: developerResolved,
			FinalText: "implemented the work item", LastEvent: request.LastSequence,
		}, nil
	}
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() after the login error = %v", err)
	}
	if _, recorded, err := outages.Standing(); err != nil || recorded {
		t.Fatalf("Standing() after the login = %t, %v, want the outage cleared", recorded, err)
	}
}

// A review the provider refused because nobody is logged into it was never
// made, and it is waited out spending nothing exactly as a developer attempt
// is: the change is built and checked, and a relaunch counted against it would
// be the September outage again one phase later.
func TestRunWaitsOutAnExpiredLoginDuringReview(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := refusingBackend(0, nil, approveVerdict)
	served := provider.run
	refused := 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer && refused < 1 {
			refused++
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.reviewerSession, IsError: true,
				StopReason: "api_error", FinalText: "Not logged in",
				ProviderOutage: &backend.ProviderOutage{Cause: domain.ProviderUnauthenticated, Detail: "api_error: Not logged in"},
				LastEvent:      request.LastSequence,
			}, nil
		}
		return served(request)
	}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &pausingClock{now: baseTime}
	pipeline = waiting(automatic(pipeline, provider), clock, 10*time.Minute, 10*time.Minute)
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 0
	pipeline.Config.Execution.UsageLimitUnknownResetPause = config.Duration(30 * time.Minute)
	outages := newOutageStore(t)
	pipeline.ProviderOutages = outages

	var pausedState runstate.State
	clock.onSleep = func() {
		loaded, err := store.Load(pipelineRunID)
		if err != nil {
			t.Errorf("Load() during the wait error = %v", err)
			return
		}
		pausedState = loaded
	}
	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if pausedState.Phase != runstate.PhaseReviewing || pausedState.PauseCause != runstate.PauseProviderUnauthenticated {
		t.Fatalf("paused state = %#v, want the review recorded as waiting on the login", pausedState)
	}
	if clock.waited() != 30*time.Minute {
		t.Fatalf("waited %s, want one probe interval", clock.waited())
	}
	if outcome.Integration == nil || !tracker.closed || outcome.TransientRelaunches != 0 {
		t.Fatalf("outcome = %#v, want the run finished with no relaunch counted", outcome)
	}
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("review invocations = %d, want the refused one and the one that answered", reviews)
	}
	// The review the provider served is the provider answering again, and it is
	// the only invocation this run makes after the outage: a run that landed with
	// the product still saying the provider is away would leave every surface
	// wrong until something else happened to be served.
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after the served review = %t, %v, want the outage cleared", away, err)
	}
}

// loginProbe stands in for the developer's provider as a watch asks it one
// thing: whether the machine is logged in.
type loginProbe struct {
	mu            sync.Mutex
	authenticated bool
	asked         int
}

func (p *loginProbe) CheckAvailability(context.Context) (backend.Availability, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	return backend.Availability{Installed: true, Authenticated: p.authenticated}, nil
}

func (p *loginProbe) login() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authenticated = true
}

func (p *loginProbe) loggedIn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authenticated
}

// The 2026-09-17 shape replayed: dispatches refused because the login expired
// count toward nothing — no brake, no docket, no exclusion — the session waits
// naming the login, and re-authentication resumes the line without a release.
func TestWatchingWaitsOutAnExpiredLoginWithoutTrippingTheBrake(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.blockedRuns = 3
	harness.capacity = 2
	outages := newOutageStore(t)
	probe := &loginProbe{}
	harness.outages, harness.provider = outages, probe
	sessions := &recordedSessions{}
	// Every dispatch is refused at the availability check while the login is
	// expired, which is what the pipeline does: it records the outage on the
	// product and reports the refusal typed. Once the operator logs in the runs
	// complete.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if !probe.loggedIn() {
			if _, err := outages.Notice(runstate.ProviderOutageObservation{
				Cause: domain.ProviderUnauthenticated, Detail: "the claude-code backend is not authenticated", Waiting: "the dispatch of " + id,
			}); err != nil {
				return Outcome{}, err
			}
			return Outcome{}, ProviderOutageError{Cause: domain.ProviderUnauthenticated, Detail: "the claude-code backend is not authenticated"}
		}
		return h.complete(id), nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 2:
			// The operator logs in two polls into the wait.
			probe.login()
			return true
		case 5:
			return false
		default:
			return true
		}
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil || schedule.BlockedInARow != 0 {
		t.Fatalf("schedule = braked %v after %d blocked in a row, want the brake never to count a dispatch the provider turned away: %s", schedule.Braked, schedule.BlockedInARow, schedule.Render())
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is held, want the line left choosing work for when the provider answers")
	}
	if schedule.ProviderAway != 2 {
		t.Fatalf("provider turned away %d dispatch(es), want the two made before the wait was read: %s", schedule.ProviderAway, schedule.Render())
	}
	if attempts := harness.recordedAttempts(); len(attempts) != 0 {
		t.Fatalf("docketed %d attempt(s), want nothing on the docket for a wait no run can end", len(attempts))
	}
	// The session said what it was waiting on, naming the login, and the reason
	// never sent anybody to release anything.
	reason := sessions.said(runstate.WatchIdle)
	if !strings.Contains(reason, "The provider is not authenticated; the operator must log in") {
		t.Fatalf("idle reason = %q, want the login named", reason)
	}
	if strings.Contains(reason, "yoyo release") {
		t.Fatalf("idle reason = %q, want no release prescribed for a wait it does not lift", reason)
	}
	// Re-authentication resumed the line: the outage was cleared on the probe,
	// and both items were started again and completed without anybody releasing
	// anything.
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after the login = %t, %v, want the outage cleared by the watch's own probe", away, err)
	}
	probe.mu.Lock()
	asked := probe.asked
	probe.mu.Unlock()
	if asked == 0 {
		t.Fatal("the watch never asked the provider whether the login was renewed")
	}
	if schedule.ProviderOutage != nil {
		t.Fatalf("schedule still carries the outage after the login: %#v", schedule.ProviderOutage)
	}
	completed := 0
	for _, started := range schedule.Started {
		if started.Outcome.WorkItemClosed {
			completed++
		}
	}
	if completed != 2 {
		t.Fatalf("completed %d run(s), want both items run once the provider answered: %s", completed, schedule.Render())
	}
}

// A drain is a command somebody is waiting on the return of, so it stops on a
// standing outage rather than sleeping through a login.
func TestADrainStopsOnAProviderAnsweringNobody(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	outages := newOutageStore(t)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Waiting: "an earlier dispatch"}); err != nil {
		t.Fatal(err)
	}
	harness.outages, harness.provider = outages, &loginProbe{}
	started := false
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		started = true
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleProviderAway || started {
		t.Fatalf("stopped = %q (started=%t), want the drain stopped on the outage with nothing dispatched into it: %s", schedule.Stopped, started, schedule.Render())
	}
	if schedule.ProviderOutage == nil || !strings.Contains(schedule.Render(), "the operator must log in") {
		t.Fatalf("schedule does not name the wait: %s", schedule.Render())
	}
}

// A provider nobody can reach is asked about by dispatching into it, once the
// probe interval has passed: nothing cheaper says whether the network is back.
func TestWatchingProbesAnUnreachableProviderByPullingAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	outages := newOutageStore(t)
	harness.outages = outages
	harness.outageProbe = 3 * time.Minute
	harness.now = time.Date(2026, 9, 17, 18, 17, 0, 0, time.UTC)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnreachable, Waiting: "an earlier run", At: harness.now}); err != nil {
		t.Fatal(err)
	}
	dispatched := 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		dispatched++
		return h.complete(id), nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool { return sleeps < 6 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: func() time.Time {
		harness.mu.Lock()
		defer harness.mu.Unlock()
		return harness.now
	}}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched %d time(s), want the item pulled once the probe interval had passed: %s", dispatched, schedule.Render())
	}
	// Three one-minute polls were waited out before the pull, which is what the
	// three-minute probe interval is worth.
	if schedule.Polls < 3 {
		t.Fatalf("polled %d time(s) before pulling, want the probe interval waited out first", schedule.Polls)
	}
}
