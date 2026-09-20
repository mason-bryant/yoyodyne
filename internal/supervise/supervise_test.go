package supervise

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeChild is one part of the product as a test drives it: it is running when
// the test says so, it counts what the supervisor asked of it, and it can be
// made to die by the test flipping one field.
type fakeChild struct {
	mu          sync.Mutex
	name        config.ServiceName
	running     bool
	starts      int
	stops       int
	pid         int
	unstartable string
	startErr    error
	runningErr  error
}

func (f *fakeChild) Name() config.ServiceName { return f.name }

func (f *fakeChild) Running(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.runningErr
}

func (f *fakeChild) Ensure(context.Context) (Ensured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return Ensured{Started: false, PID: f.pid}, nil
	}
	if f.startErr != nil {
		return Ensured{}, f.startErr
	}
	if f.unstartable != "" {
		return Ensured{Unstartable: f.unstartable}, nil
	}
	f.starts++
	f.pid = 1000 + f.starts
	f.running = true
	return Ensured{Started: true, PID: f.pid, Log: "/state/" + string(f.name) + ".log"}, nil
}

func (f *fakeChild) Stop(context.Context) (Stopped, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if !f.running {
		return Stopped{}, nil
	}
	f.running = false
	return Stopped{WasRunning: true, PID: f.pid}, nil
}

// die is the test killing the child: whatever it was, it no longer holds its
// lease.
func (f *fakeChild) die() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
}

func (f *fakeChild) isRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeChild) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// clock is a clock the test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

func newStore(t *testing.T) *runstate.SupervisionStore {
	t.Helper()
	store, err := runstate.NewSupervisionStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewSupervisionStore() error = %v", err)
	}
	return store
}

func newSupervisor(t *testing.T, store *runstate.SupervisionStore, clock *clock, children ...Child) *Supervisor {
	t.Helper()
	return &Supervisor{
		Records:  store,
		Product:  "yoyodyne",
		Children: children,
		NotYet:   []NotYet{{Name: config.ServiceDashboard, Reason: "its adoption is yoyodyne-ifd.414"}},
		Off:      []config.ServiceName{config.ServiceMaintenance},
		Now:      clock.Now,
		Log:      t.Logf,
		PID:      4242,
		Build:    "abc1234",
	}
}

func loaded(t *testing.T, store *runstate.SupervisionStore) runstate.Supervision {
	t.Helper()
	recorded, found, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatal("Load() found no record, want the supervisor to have written one")
	}
	return recorded
}

func child(t *testing.T, recorded runstate.Supervision, name config.ServiceName) runstate.SupervisedChild {
	t.Helper()
	found, ok := recorded.Child(name)
	if !ok {
		t.Fatalf("the record carries no %s child: %+v", name, recorded.Children)
	}
	return found
}

// One look starts every enabled child, and the record says so beside the part
// that is off and the part that is enabled but not yet a child — the whole
// shape of the product, so `yoyo start` can say what came up and what did not.
func TestTheFirstLookStartsEveryEnabledChildAndRecordsTheWholeShape(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	scheduler := &fakeChild{name: config.ServiceScheduler}
	supervisor := newSupervisor(t, store, clock, slack, scheduler)

	supervisor.Tick(context.Background())

	if slack.startCount() != 1 || scheduler.startCount() != 1 {
		t.Fatalf("starts = %d and %d, want each enabled child started once", slack.startCount(), scheduler.startCount())
	}
	recorded := loaded(t, store)
	if recorded.PID != 4242 || recorded.Build != "abc1234" {
		t.Errorf("record names pid %d build %q, want the supervisor's own", recorded.PID, recorded.Build)
	}
	if got := child(t, recorded, config.ServiceSlack); got.State != runstate.ChildRunning || got.PID != 1001 || got.Starts != 1 || got.Reattached {
		t.Errorf("slack = %+v, want running as the pid it was started with, started once, not reattached", got)
	}
	if got := child(t, recorded, config.ServiceDashboard); got.State != runstate.ChildNotYet || !strings.Contains(got.Reason, "yoyodyne-ifd.414") {
		t.Errorf("dashboard = %+v, want not yet a child with the adopting work named", got)
	}
	if got := child(t, recorded, config.ServiceMaintenance); got.State != runstate.ChildOff {
		t.Errorf("maintenance = %+v, want off", got)
	}
	// In the section's order, whatever order the children were given in.
	var order []config.ServiceName
	for _, entry := range recorded.Children {
		order = append(order, entry.Service)
	}
	want := []config.ServiceName{config.ServiceSlack, config.ServiceDashboard, config.ServiceScheduler, config.ServiceMaintenance}
	if strings.Join(names(order), ",") != strings.Join(names(want), ",") {
		t.Errorf("children recorded in order %v, want the section's %v", order, want)
	}
}

func names(services []config.ServiceName) []string {
	named := make([]string, 0, len(services))
	for _, service := range services {
		named = append(named, string(service))
	}
	return named
}

// A child that dies is restarted, after a backoff rather than in the same
// look, and the record says it is down and when it comes back in between.
func TestAChildThatDiesIsRestartedAfterItsBackoff(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Tick(context.Background())

	clock.advance(time.Hour)
	slack.die()
	supervisor.Tick(context.Background())
	if slack.startCount() != 1 {
		t.Fatalf("starts = %d after a death, want the restart to wait out its backoff rather than happen in the same look", slack.startCount())
	}
	down := child(t, loaded(t, store), config.ServiceSlack)
	if down.State != runstate.ChildDown || down.Failures != 1 || !strings.Contains(down.Reason, "restart 1 of 5") {
		t.Fatalf("slack = %+v, want down with its first failure counted and the restart named", down)
	}
	if !down.NextStartAt.Equal(clock.Now().Add(time.Second)) {
		t.Fatalf("next start at %s, want one second of backoff after the first failure", down.NextStartAt)
	}

	clock.advance(time.Second)
	supervisor.Tick(context.Background())
	if slack.startCount() != 2 || !slack.isRunning() {
		t.Fatalf("starts = %d running = %v, want the child started again once its backoff passed", slack.startCount(), slack.isRunning())
	}
	up := child(t, loaded(t, store), config.ServiceSlack)
	if up.State != runstate.ChildRunning || up.Starts != 2 || up.PID != 1002 {
		t.Errorf("slack = %+v, want running as its second start", up)
	}
}

// A child that keeps dying is restarted the bounded number of times and then
// left down as degraded, with the reason, and nothing starts it again.
func TestAChildThatKeepsDyingIsLeftDownAndDegraded(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	scheduler := &fakeChild{name: config.ServiceScheduler}
	supervisor := newSupervisor(t, store, clock, scheduler)
	supervisor.Tick(context.Background())

	// Each death within the stable interval is a rapid failure; the backoff is
	// waited out in full each time so every restart the bound permits happens.
	for failure := 1; failure <= MaxRapidFailures; failure++ {
		clock.advance(time.Second)
		scheduler.die()
		supervisor.Tick(context.Background())
		clock.advance(backoff(failure))
		supervisor.Tick(context.Background())
		if scheduler.startCount() != failure+1 {
			t.Fatalf("after rapid failure %d, starts = %d, want %d", failure, scheduler.startCount(), failure+1)
		}
	}
	clock.advance(time.Second)
	scheduler.die()
	supervisor.Tick(context.Background())
	degraded := child(t, loaded(t, store), config.ServiceScheduler)
	if degraded.State != runstate.ChildDegraded {
		t.Fatalf("scheduler = %+v, want degraded after the bound", degraded)
	}
	if !strings.Contains(degraded.Reason, "died 6 times") || !strings.Contains(degraded.Reason, "left down") {
		t.Errorf("reason = %q, want the deaths counted and the child said to be left down", degraded.Reason)
	}

	// Left down means left down: an hour of looks starts nothing.
	for look := 0; look < 10; look++ {
		clock.advance(6 * time.Minute)
		supervisor.Tick(context.Background())
	}
	if scheduler.startCount() != MaxRapidFailures+1 {
		t.Fatalf("starts = %d, want a degraded child never started again", scheduler.startCount())
	}
}

// A child that stayed up past the stable interval and then died is not a
// child that cannot start, so its failures start counting again from one.
func TestAStableRunResetsTheFailureCount(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Tick(context.Background())

	for failure := 1; failure <= 3; failure++ {
		clock.advance(time.Second)
		slack.die()
		supervisor.Tick(context.Background())
		clock.advance(backoff(failure))
		supervisor.Tick(context.Background())
	}
	if got := child(t, loaded(t, store), config.ServiceSlack); got.Failures != 3 {
		t.Fatalf("failures = %d after three rapid deaths, want 3", got.Failures)
	}
	clock.advance(StableAfter)
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceSlack); got.Failures != 0 {
		t.Fatalf("failures = %d after a stable run, want the series forgotten", got.Failures)
	}
	clock.advance(time.Hour)
	slack.die()
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceSlack); got.Failures != 1 || got.State != runstate.ChildDown {
		t.Fatalf("slack = %+v after a death following a stable run, want the first failure of a fresh series", got)
	}
}

// A child that cannot be started at all — the tokens it needs are not stored
// — is degraded at once with that reason, rather than the bound being spent
// finding out five times what the first answer already said.
func TestAChildThatCannotStartIsDegradedWithTheReason(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack, unstartable: "the keychain would not produce its tokens: yoyo-slack-bot.yoyodyne"}
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Tick(context.Background())

	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildDegraded || !strings.Contains(got.Reason, "yoyo-slack-bot.yoyodyne") {
		t.Fatalf("slack = %+v, want degraded with the store's own reason", got)
	}
	if len(loaded(t, store).Degraded()) != 1 {
		t.Errorf("Degraded() = %v, want the one child", loaded(t, store).Degraded())
	}
}

// A launcher that fails is a failure like a death: counted against the bound,
// waited out on the backoff, and degraded past it.
func TestAStartThatFailsCountsTowardTheBound(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	scheduler := &fakeChild{name: config.ServiceScheduler, startErr: errors.New("start /usr/local/bin/yoyo: permission denied")}
	supervisor := newSupervisor(t, store, clock, scheduler)

	for failure := 1; failure <= MaxRapidFailures; failure++ {
		supervisor.Tick(context.Background())
		got := child(t, loaded(t, store), config.ServiceScheduler)
		if got.State != runstate.ChildDown || got.Failures != failure {
			t.Fatalf("after failed start %d, scheduler = %+v, want down with %d failures", failure, got, failure)
		}
		clock.advance(backoff(failure))
	}
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildDegraded || !strings.Contains(got.Reason, "permission denied") {
		t.Fatalf("scheduler = %+v, want degraded with the launcher's own complaint", got)
	}
}

// The supervisor dying leaves its children running — they are processes of
// their own — and the one that comes back finds them through their leases and
// reattaches rather than starting them again.
func TestTheSupervisorDyingLeavesChildrenRunningAndARestartReattachesThem(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	scheduler := &fakeChild{name: config.ServiceScheduler}
	first := newSupervisor(t, store, clock, slack, scheduler)
	first.Poll = time.Millisecond

	// A supervisor that runs and is then stopped where it stands: the context
	// ending is the process dying, as far as the children are concerned.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.Run(ctx) }()
	waitFor(t, func() bool { return slack.isRunning() && scheduler.isRunning() })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slack.isRunning() || !scheduler.isRunning() {
		t.Fatal("the supervisor stopping took its children down with it, and they are meant to survive it")
	}
	if slack.stops != 0 || scheduler.stops != 0 {
		t.Fatal("the supervisor stopping asked a child to stop, which is `yoyo stop`'s to do and not the supervisor's")
	}
	if running, err := store.Running(); err != nil || running {
		t.Fatalf("Running() = %v, %v after the supervisor returned, want its lease let go", running, err)
	}

	second := newSupervisor(t, store, clock, slack, scheduler)
	second.PID = 4343
	clock.advance(time.Minute)
	second.Tick(context.Background())
	if slack.startCount() != 1 || scheduler.startCount() != 1 {
		t.Fatalf("starts = %d and %d after the supervisor came back, want the running children reattached rather than started again", slack.startCount(), scheduler.startCount())
	}
	recorded := loaded(t, store)
	if recorded.PID != 4343 {
		t.Errorf("record names pid %d, want the supervisor that came back", recorded.PID)
	}
	for _, name := range []config.ServiceName{config.ServiceSlack, config.ServiceScheduler} {
		got := child(t, recorded, name)
		if got.State != runstate.ChildRunning || !got.Reattached || got.Starts != 0 {
			t.Errorf("%s = %+v, want running, reattached, and never started by this supervisor", name, got)
		}
	}
}

// Two supervisors over one product would each restart what the other stopped,
// so the second is refused while the first holds the lease.
func TestASecondSupervisorIsRefusedWhileOneRuns(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	first := newSupervisor(t, store, clock, &fakeChild{name: config.ServiceSlack})
	first.Poll = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- first.Run(ctx) }()
	waitFor(t, func() bool { running, _ := store.Running(); return running })

	second := newSupervisor(t, store, clock, &fakeChild{name: config.ServiceSlack})
	if err := second.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run() error = %v, want %v", err, ErrAlreadyRunning)
	}
	cancel()
	<-done
}

// A degraded child somebody started by hand is running again, and the
// supervisor takes it back rather than going on reporting it down.
func TestADegradedChildStartedByHandIsTakenBack(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack, unstartable: "no tokens"}
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceSlack); got.State != runstate.ChildDegraded {
		t.Fatalf("slack = %+v, want degraded", got)
	}

	// The operator stores the tokens and runs `yoyo slack ensure` themselves.
	slack.mu.Lock()
	slack.unstartable = ""
	slack.running = true
	slack.pid = 77
	slack.mu.Unlock()
	clock.advance(time.Minute)
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildRunning || !got.Reattached || got.Reason != "" {
		t.Fatalf("slack = %+v, want running and reattached with no reason left over", got)
	}
}

// Stopping takes the children in reverse start order and carries on past one
// that fails, so the ones after it are still asked.
func TestStopAllTakesTheChildrenInReverseOrder(t *testing.T) {
	t.Parallel()

	var order []config.ServiceName
	slack := &orderedChild{fakeChild: &fakeChild{name: config.ServiceSlack, running: true, pid: 11}, order: &order}
	scheduler := &orderedChild{fakeChild: &fakeChild{name: config.ServiceScheduler, running: true, pid: 12}, order: &order}
	stops, err := StopAll(context.Background(), []Child{slack, scheduler}, t.Logf)
	if err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if len(order) != 2 || order[0] != config.ServiceScheduler || order[1] != config.ServiceSlack {
		t.Fatalf("stopped in order %v, want the reverse of the start order", order)
	}
	if stops[0].Stopped.PID == 0 || !strings.Contains(stops[0].Describe(), "stopped pid") {
		t.Errorf("first stop = %+v, want the process it stopped named", stops[0])
	}

	stopped := &fakeChild{name: config.ServiceSlack}
	stops, err = StopAll(context.Background(), []Child{stopped}, nil)
	if err != nil {
		t.Fatalf("StopAll() over a child that was not running error = %v", err)
	}
	if !strings.Contains(stops[0].Describe(), "was not running") {
		t.Errorf("stop of a child that was not running = %q, want it said", stops[0].Describe())
	}
}

type orderedChild struct {
	*fakeChild
	order *[]config.ServiceName
}

func (o *orderedChild) Stop(ctx context.Context) (Stopped, error) {
	*o.order = append(*o.order, o.name)
	return o.fakeChild.Stop(ctx)
}

// A child whose lease cannot be asked about is not a child that died, and
// starting one over it could start a second: the look leaves it alone and says
// so.
func TestAChildWhoseLeaseCannotBeReadIsLeftAlone(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack, runningErr: errors.New("lock the slack sink lease: permission denied")}
	var said []string
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Log = func(format string, args ...any) { said = append(said, format) }
	supervisor.Tick(context.Background())
	if slack.startCount() != 0 {
		t.Fatal("a child whose lease could not be read was started over it")
	}
	if len(said) == 0 || !strings.Contains(said[0], "could not tell whether") {
		t.Errorf("said %v, want the unreadable lease named", said)
	}
}

// The words every surface says about a child are one function's.
func TestDescribeChildSaysEachStateInPlainWords(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		child runstate.SupervisedChild
		want  string
	}{
		{runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 12, Log: "/l"}, "slack: running as pid 12, logging to /l"},
		{runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 12, Reattached: true}, "slack: running, reattached as pid 12"},
		{runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildDegraded, Reason: "died 6 times"}, "scheduler: degraded, died 6 times"},
		{runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildDown, Reason: "died at noon"}, "scheduler: down, died at noon"},
		{runstate.SupervisedChild{Service: config.ServiceDashboard, State: runstate.ChildNotYet, Reason: "yoyodyne-ifd.414"}, "dashboard: enabled, and not yet a child of the supervisor: yoyodyne-ifd.414"},
		{runstate.SupervisedChild{Service: config.ServiceMaintenance, State: runstate.ChildOff}, "maintenance: off; set services.maintenance.enabled to start it with the product"},
		{runstate.SupervisedChild{Service: config.ServiceMaintenance, State: runstate.ChildScheduled, Reason: "every 10m0s; next at noon"}, "maintenance: the supervisor's own periodic pass, every 10m0s; next at noon"},
	} {
		if got := DescribeChild(testCase.child); got != testCase.want {
			t.Errorf("DescribeChild(%+v) = %q, want %q", testCase.child, got, testCase.want)
		}
	}
}

// The backoff doubles from a second and is capped, so a child that fails the
// bounded number of times is restarted within a minute rather than left to a
// wait nobody chose.
func TestBackoffDoublesFromASecondAndIsCapped(t *testing.T) {
	t.Parallel()

	for failures, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 5: 16 * time.Second, 6: 30 * time.Second, 20: 30 * time.Second} {
		if got := backoff(failures); got != want {
			t.Errorf("backoff(%d) = %s, want %s", failures, got, want)
		}
	}
}

// A supervisor assembled without what it needs says so rather than running.
func TestASupervisorRefusesWithoutWhatItNeeds(t *testing.T) {
	t.Parallel()

	if err := (&Supervisor{Product: "yoyodyne", PID: 1}).Run(context.Background()); err == nil {
		t.Fatal("Run() with no records ran a supervisor nothing could see")
	}
	store := newStore(t)
	if err := (&Supervisor{Records: store, Product: "yoyodyne"}).Run(context.Background()); err == nil {
		t.Fatal("Run() with no pid ran a supervisor `yoyo stop` could not name")
	}
	twice := &fakeChild{name: config.ServiceSlack}
	if err := (&Supervisor{Records: store, Product: "yoyodyne", PID: 1, Children: []Child{twice, twice}}).Run(context.Background()); err == nil {
		t.Fatal("Run() with one child given twice ran a supervisor that would start it twice")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the condition was not met in time")
}

// fakePass is the maintenance pass as a test drives it: due when the test says
// so, and asking for a restart into a deploy on cue.
type fakePass struct {
	mu       sync.Mutex
	due      bool
	takeUp   bool
	runs     int
	restarts func(ctx context.Context) error
}

func (p *fakePass) Due(time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.due
}

func (p *fakePass) Run(ctx context.Context) PassOutcome {
	p.mu.Lock()
	p.runs++
	p.due = false
	takeUp := p.takeUp
	restarts := p.restarts
	p.mu.Unlock()
	if restarts != nil {
		_ = restarts(ctx)
	}
	return PassOutcome{TakeUpDeploy: takeUp}
}

func (p *fakePass) Describe() string { return "every 10m0s; a fake" }

func (p *fakePass) runCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs
}

// fakeDeployment records the invocation the supervisor tried to restart into,
// and cannot actually replace the process, which is the one half of a
// re-execution a test can observe.
type fakeDeployment struct {
	mu    sync.Mutex
	taken [][]string
}

func (d *fakeDeployment) Args() []string { return []string{"yoyo", "start", "--foreground"} }

func (d *fakeDeployment) Take(args []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.taken = append(d.taken, args)
	return errors.New("the test process is not replaced")
}

func (d *fakeDeployment) takes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.taken)
}

// The maintenance pass is the supervisor's own: taken between looks when its
// cadence says so, recorded as scheduled beside the children, and when it
// finds a build installed over the running one the supervisor lets its lease
// go and re-executes itself. Where the re-execution does not happen the
// supervisor takes its lease back and carries on as the build it is, with
// every child left running throughout.
func TestTheSupervisorTakesThePassAndRestartsIntoADeploy(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	scheduler := &fakeChild{name: config.ServiceScheduler}
	supervisor := newSupervisor(t, store, clock, slack, scheduler)
	supervisor.Off = nil
	supervisor.Poll = time.Millisecond
	pass := &fakePass{due: true, takeUp: true}
	deployment := &fakeDeployment{}
	supervisor.Pass = pass
	supervisor.Deployment = deployment

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	waitFor(t, func() bool { return pass.runCount() == 1 && deployment.takes() == 1 })
	// The restart did not happen, and the supervisor is still the product's:
	// its lease is held again and its children were never touched.
	waitFor(t, func() bool { running, _ := store.Running(); return running })
	if !slack.isRunning() || !scheduler.isRunning() || slack.stops != 0 || scheduler.stops != 0 {
		t.Fatal("the takeover touched a child; the children are left running to be reattached")
	}
	if got := deployment.taken[0]; strings.Join(got, " ") != "yoyo start --foreground" {
		t.Errorf("restarted as %v, want the supervisor's own invocation", got)
	}
	recorded := loaded(t, store)
	maintenance := child(t, recorded, config.ServiceMaintenance)
	if maintenance.State != runstate.ChildScheduled || maintenance.Reason != "every 10m0s; a fake" {
		t.Errorf("maintenance = %+v, want it recorded as the supervisor's scheduled pass", maintenance)
	}
	// A pass that is not due is not taken.
	clock.advance(time.Minute)
	time.Sleep(5 * time.Millisecond)
	if pass.runCount() != 1 {
		t.Errorf("the pass was taken %d times while not due, want the one", pass.runCount())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// The pass restarts a child for a deploy through the supervisor, which stops
// it and starts it again on its next look: a stop on purpose, not a death, so
// the failure count and the backoff are untouched and the reason is on the
// record until the child is back.
func TestARestartStopsAChildForTheNextLookToStartAgain(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	slack := &fakeChild{name: config.ServiceSlack}
	supervisor := newSupervisor(t, store, clock, slack)
	supervisor.Tick(context.Background())
	if slack.startCount() != 1 {
		t.Fatalf("starts = %d after the first look, want 1", slack.startCount())
	}

	state, err := supervisor.Restart(context.Background(), config.ServiceSlack, "stopped for the build installed at noon")
	if err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if state.State != runstate.ChildDown || state.Reason != "stopped for the build installed at noon" || state.Failures != 0 || slack.stops != 1 || slack.isRunning() {
		t.Fatalf("after Restart(): state %+v, stops %d, running %t; want the child stopped and recorded down with the reason and no failure counted", state, slack.stops, slack.isRunning())
	}
	if _, err := supervisor.Restart(context.Background(), config.ServiceSlack, "again"); err == nil {
		t.Error("Restart() of a child that is not running was not refused")
	}
	if _, err := supervisor.Restart(context.Background(), config.ServiceDashboard, "again"); err == nil {
		t.Error("Restart() of a part that is not a child was not refused")
	}

	clock.advance(time.Second)
	supervisor.Tick(context.Background())
	if slack.startCount() != 2 || !slack.isRunning() {
		t.Fatalf("starts = %d, running %t after the next look, want the child started again at once", slack.startCount(), slack.isRunning())
	}
	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildRunning || got.Failures != 0 || got.Reason != "" || got.Starts != 2 {
		t.Errorf("slack = %+v, want running again with no failure counted", got)
	}
}
