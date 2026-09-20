package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/launchd"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/shutdown"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// productHelperVariable is how the test below tells the process `yoyo start`
// detaches to be the harness rather than to run tests. The supervisor is a
// process of its own by design — it has to outlive the verb that started it —
// so the verb is exercised by actually detaching one, from this binary, and
// stopping it again.
const productHelperVariable = "YOYODYNE_PRODUCT_TEST_COMMAND"

func TestMain(m *testing.M) {
	if os.Getenv(productHelperVariable) == "" {
		os.Exit(m.Run())
	}
	// The process answers a stop signal exactly as the real binary does, and it
	// carries a bound of its own: a helper the test failed to stop ends itself
	// rather than running on the machine after the test is over.
	ctx, stop := shutdown.Answering(context.Background(), os.Stderr)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	code := RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr, "test")
	cancel()
	stop()
	os.Exit(code)
}

// A product with every part off, so starting it exercises the supervisor and
// nothing that would spawn a scheduler or ask a keychain.
const quietProductConfig = validConfig + `services:
  slack:
    enabled: false
  dashboard:
    enabled: false
  scheduler:
    enabled: false
  maintenance:
    enabled: false
`

// One verb starts the product and one stops it; a second start while it runs
// says so and does nothing; and what is started is a process of its own, found
// again by its lease and its record rather than by anything the first verb
// kept.
func TestTheProductStartsOnceStopsOnceAndASecondStartDoesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the supervisor is detached and signalled on the Unix hosts Yoyodyne supports")
	}
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(productHelperVariable, "1")
	configPath := writeConfig(t, quietProductConfig)
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the test finds, the process it started does not outlive it.
	t.Cleanup(func() {
		if recorded, found, err := store.Load(); err == nil && found && recorded.PID > 0 {
			_ = syscall.Kill(recorded.PID, syscall.SIGKILL)
		}
	})

	stdout, stderr, code := runCLI(t, "start", "--config", configPath)
	if code != 0 {
		t.Fatalf("start code = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"started the supervisor for yoyodyne as pid", "slack: off", "dashboard: off", "scheduler: off", "maintenance: off", "yoyo stop"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("start said %q, want %q in it", stdout, want)
		}
	}
	recorded, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v after start, want the supervisor's record", found, err)
	}
	if running, err := store.Running(); err != nil || !running {
		t.Fatalf("Running() = %t, %v after start, want the detached supervisor holding its lease", running, err)
	}

	stdout, stderr, code = runCLI(t, "start", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("second start code = %d, stderr %q", code, stderr)
	}
	var second startReport
	if err := json.Unmarshal([]byte(stdout), &second); err != nil {
		t.Fatalf("second start wrote %q, want JSON: %v", stdout, err)
	}
	if second.Started || second.PID != recorded.PID || second.Recorded == nil {
		t.Fatalf("second start = %+v, want nothing started and the running supervisor named", second)
	}
	if running, err := store.Running(); err != nil || !running {
		t.Fatalf("Running() = %t, %v after a second start, want the first supervisor still there", running, err)
	}

	// The reading `yoyo status` takes is wired to the same record.
	if sources := standingSources(configPath); sources.Supervision == nil {
		t.Error("standingSources() wired nothing to read the supervisor, so `yoyo status` cannot say a part is degraded")
	}

	stdout, stderr, code = runCLI(t, "stop", "--config", configPath)
	if code != 0 {
		t.Fatalf("stop code = %d, stderr %q stdout %q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, fmt.Sprintf("supervisor: stopped pid %d", recorded.PID)) {
		t.Errorf("stop said %q, want the supervisor stopped by pid", stdout)
	}
	if running, err := store.Running(); err != nil || running {
		t.Fatalf("Running() = %t, %v after stop, want the lease let go", running, err)
	}

	stdout, _, code = runCLI(t, "stop", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "supervisor: was not running") {
		t.Errorf("stopping a stopped product = %d %q, want it said to be not running and no failure", code, stdout)
	}
}

// recordingLauncher stands in for the detached launcher: it records what it
// was asked to start and runs a supervisor in this process against the same
// store, which is what the detached one would do.
type recordingLauncher struct {
	launched []slack.Launch
	store    *runstate.SupervisionStore
	children []supervise.Child
	cancel   context.CancelFunc
	done     chan struct{}
}

func (l *recordingLauncher) Launch(spec slack.Launch) (int, error) {
	l.launched = append(l.launched, spec)
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.done = make(chan struct{})
	supervisor := &supervise.Supervisor{
		Records:  l.store,
		Product:  "yoyodyne",
		Children: l.children,
		NotYet:   []supervise.NotYet{{Name: config.ServiceDashboard, Reason: "its adoption is yoyodyne-ifd.414"}},
		Off:      []config.ServiceName{config.ServiceMaintenance},
		Poll:     time.Millisecond,
		PID:      4242,
	}
	go func() {
		defer close(l.done)
		_ = supervisor.Run(ctx)
	}()
	return 4242, nil
}

type startedChild struct {
	name    config.ServiceName
	running bool
}

func (c *startedChild) Name() config.ServiceName              { return c.name }
func (c *startedChild) Running(context.Context) (bool, error) { return c.running, nil }
func (c *startedChild) Ensure(context.Context) (supervise.Ensured, error) {
	c.running = true
	return supervise.Ensured{Started: true, PID: 77, Log: "/state/" + string(c.name) + ".log"}, nil
}
func (c *startedChild) Stop(context.Context) (supervise.Stopped, error) {
	c.running = false
	return supervise.Stopped{WasRunning: true, PID: 77}, nil
}

// What `yoyo start` says is what the supervisor recorded: each part by name,
// running as the process it was started as, not yet a child where its
// adoption has not landed, off where the section leaves it off. The
// supervisor is started with the Slack variables taken out of its environment
// and told which configuration to read.
func TestStartSaysWhatTheSupervisorRecordedAboutEachPart(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{store: store, children: []supervise.Child{
		&startedChild{name: config.ServiceSlack},
		&startedChild{name: config.ServiceScheduler},
	}}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   "/opt/yoyo/bin/yoyo",
		launcher:  launcher,
		environ:   []string{"HOME=/Users/mason", slack.BotTokenVariable + "=xoxb-secret", slack.AppTokenVariable + "=xapp-secret"},
		now:       time.Now,
	}
	var stdout, stderr strings.Builder
	if code := p.start(context.Background(), &stdout, &stderr, false); code != 0 {
		t.Fatalf("start code = %d, stderr %q", code, stderr.String())
	}
	defer func() {
		launcher.cancel()
		<-launcher.done
	}()
	for _, want := range []string{
		"started the supervisor for yoyodyne as pid 4242",
		"slack: running as pid 77, logging to /state/slack.log",
		"dashboard: enabled, and not yet a child of the supervisor: its adoption is yoyodyne-ifd.414",
		"scheduler: running as pid 77",
		"maintenance: off",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("start said:\n%s\nwant %q in it", stdout.String(), want)
		}
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("launched %d processes, want the one supervisor", len(launcher.launched))
	}
	launch := launcher.launched[0]
	if launch.Program != "/opt/yoyo/bin/yoyo" || strings.Join(launch.Args, " ") != "start --foreground --config "+resolved.Path {
		t.Errorf("launched %s %v, want this binary as the foreground supervisor reading this configuration", launch.Program, launch.Args)
	}
	if strings.Join(launch.Env, " ") != "HOME=/Users/mason" {
		t.Errorf("supervisor environment = %v, want the Slack variables taken out and nothing else changed", launch.Env)
	}
	if launch.LogRoot != stateRoot || !strings.HasSuffix(launch.Log, "supervisor/supervisor.log") {
		t.Errorf("supervisor log = %q under %q, want it beside the supervisor's record", launch.Log, launch.LogRoot)
	}
}

// Where the launch agent `yoyo setup` installed is loaded and runs this
// configuration, `yoyo start` asks launchd for the supervisor rather than
// detaching one: the resident launchd restarts is the one that runs. An agent
// for another checkout of a product with the same id is not this one's, and
// the verb detaches as before.
func TestStartAsksLaunchdForTheSupervisorWhereItsAgentIsLoaded(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	launchctl := &fakeLaunchctl{store: store}
	agent := launchd.AgentFor("yoyodyne", launchd.Controller{Runner: launchctl, UserHomeDir: func() (string, error) { return home, nil }, Getuid: func() int { return 501 }})
	if err := agent.Install(launchd.Render(launchd.Spec{Label: agent.Label, Program: "/opt/yoyo/bin/yoyo", Args: []string{"start", "--foreground", "--config", resolved.Path}})); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{store: store}
	p := &product{resolved: resolved, stateRoot: stateRoot, store: store, program: "/opt/yoyo/bin/yoyo", launcher: launcher, agent: agent, now: time.Now}

	var stdout, stderr strings.Builder
	if code := p.start(context.Background(), &stdout, &stderr, false); code != 0 {
		t.Fatalf("start code = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "started the supervisor for yoyodyne through the launch agent com.yoyodyne.yoyodyne, as pid 4343") {
		t.Errorf("start said:\n%s\nwant the supervisor started through the launch agent", stdout.String())
	}
	if len(launcher.launched) != 0 {
		t.Errorf("start detached %v beside the launch agent's supervisor", launcher.launched)
	}
	if !launchctl.kickstarted {
		t.Error("start did not ask launchd to start the job")
	}

	// The agent runs another checkout's configuration: not this one's.
	other, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	launchctl.kickstarted = false
	p.resolved = other
	detaching := &recordingLauncher{store: store}
	p.launcher = detaching
	stdout.Reset()
	if code := p.start(context.Background(), &stdout, &stderr, false); code != 0 {
		t.Fatalf("second start code = %d, stderr %q", code, stderr.String())
	}
	if detaching.cancel != nil {
		defer func() {
			detaching.cancel()
			<-detaching.done
		}()
	}
	if launchctl.kickstarted || len(detaching.launched) != 1 {
		t.Errorf("start with another checkout's agent: kickstarted %t, launched %v; want the supervisor detached from here", launchctl.kickstarted, detaching.launched)
	}
}

// fakeLaunchctl is launchd holding the product's job: a kickstart writes the
// record a launchd-started supervisor would, and is recorded.
type fakeLaunchctl struct {
	store       *runstate.SupervisionStore
	kickstarted bool
}

func (f *fakeLaunchctl) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	joined := command.Name + " " + strings.Join(command.Args, " ")
	switch {
	case strings.HasPrefix(joined, "launchctl print gui/501/com.yoyodyne.yoyodyne"):
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	case strings.HasPrefix(joined, "launchctl kickstart gui/501/com.yoyodyne.yoyodyne"):
		f.kickstarted = true
		now := time.Now().UTC()
		if err := f.store.Save(runstate.Supervision{PID: 4343, StartedAt: now, ObservedAt: now, Children: []runstate.SupervisedChild{
			{Service: config.ServiceMaintenance, State: runstate.ChildScheduled, Reason: "every 10m0s"},
		}}); err != nil {
			return execution.ProcessResult{}, err
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "unexpected: " + joined}, nil
}

// The foreground form refused because a supervisor already runs exits as the
// detaching form does, without failure: the launch agent runs this form, and
// a refusal launchd read as a failure would be started again every few
// seconds for as long as the other supervisor ran.
func TestTheForegroundSupervisorRefusedByALeaseExitsCleanly(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v", held, err)
	}
	defer lease.Release()
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   "/opt/yoyo/bin/yoyo",
		children: func() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error) {
			return nil, nil, config.ServiceNames, nil
		},
		now: time.Now,
	}
	var stdout, stderr strings.Builder
	if code := p.supervise(context.Background(), &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "a second start while one is running does nothing") {
		t.Fatalf("supervise() = %d, stderr %q; want a clean exit saying a second start does nothing", code, stderr.String())
	}
}

// The real parts are assembled from the services section: a part that is off
// is off, the sink and the scheduler are children, and the two whose adoption
// has not landed are named as such with the work that adopts them.
func TestTheRealPartsFollowTheServicesSection(t *testing.T) {
	t.Parallel()

	resolved, err := config.LoadResolved(writeConfig(t, validConfig+`services:
  slack:
    enabled: false
  dashboard:
    enabled: true
  scheduler:
    enabled: true
  maintenance:
    enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := &product{resolved: resolved, stateRoot: t.TempDir(), program: "/opt/yoyo/bin/yoyo", launcher: slack.DetachedLauncher{}, goos: "darwin"}
	children, notYet, off, err := p.realChildren()
	if err != nil {
		t.Fatalf("realChildren() error = %v", err)
	}
	if len(children) != 1 || children[0].Name() != config.ServiceScheduler {
		t.Errorf("children = %v, want the scheduler alone", children)
	}
	if len(notYet) != 1 || notYet[0].Name != config.ServiceDashboard || !strings.Contains(notYet[0].Reason, "yoyodyne-ifd.414") {
		t.Errorf("notYet = %+v, want the dashboard alone with its adopting work named", notYet)
	}
	// The maintenance pass is the supervisor's own rather than a child: it is
	// assembled from the section as a pass on its cadence, or not at all.
	pass, err := p.realPass(nil, nil, t.Logf)
	if err != nil || pass == nil {
		t.Fatalf("realPass() = %v, %v, want the enabled pass", pass, err)
	}
	if got := pass.Describe(); !strings.Contains(got, "every 10m0s") {
		t.Errorf("pass.Describe() = %q, want the configured cadence", got)
	}
	p.resolved.Config.Services.Maintenance.Enabled = false
	if pass, err := p.realPass(nil, nil, t.Logf); err != nil || pass != nil {
		t.Errorf("realPass() with the pass off = %v, %v, want none", pass, err)
	}
	if len(off) != 1 || off[0] != config.ServiceSlack {
		t.Errorf("off = %v, want slack", off)
	}

	// A scheduler that is not running is nothing to stop, and says so.
	stopped, err := children[0].Stop(context.Background())
	if err != nil || stopped.WasRunning {
		t.Errorf("Stop() of a scheduler that is not running = %+v, %v, want nothing stopped and no failure", stopped, err)
	}
}

// The sink cannot be started from a keychain a machine does not have, and that
// is the operator's to arrange rather than something to retry: the child is
// unstartable with the same words `yoyo slack ensure` uses.
func TestTheSinkIsUnstartableWhereThereIsNoKeychain(t *testing.T) {
	t.Parallel()

	store, err := slack.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	ensured, err := slackChild{store: store, goos: "linux"}.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !strings.Contains(ensured.Unstartable, "macOS keychain") || !strings.Contains(ensured.Unstartable, "slack.env") {
		t.Errorf("Unstartable = %q, want the keychain named and the environment file offered", ensured.Unstartable)
	}
}

// Both verbs are in the command list and the help, because they are what an
// operator reaches for first.
func TestStartAndStopAreListedAmongTheCommands(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printUsage(&usage)
	for _, want := range []string{"  start ", "  stop "} {
		if !strings.Contains(usage.String(), want) {
			t.Errorf("usage does not list %q", strings.TrimSpace(want))
		}
	}
	var start, stop strings.Builder
	printStartUsage(&start)
	printStopUsage(&stop)
	for _, want := range []string{"yoyo slack ensure", "yoyo work --watch", "degraded", "--foreground", "second start", "yoyo reconcile", "launch agent", "never stopped for a deploy"} {
		if !strings.Contains(start.String(), want) {
			t.Errorf("start usage does not say %q", want)
		}
	}
	for _, want := range []string{"reverse", "cancels the runs", "yoyo pause"} {
		if !strings.Contains(stop.String(), want) {
			t.Errorf("stop usage does not say %q", want)
		}
	}
}
