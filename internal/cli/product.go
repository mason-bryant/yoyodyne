package cli

// `yoyo start` and `yoyo stop`: the product started once, and stopped once.
//
// Before this, Slack, the scheduler, and the dashboard were each a verb an
// operator ran and left running, and a machine that rebooted was a machine
// where each of them had to be started again by hand. The operator's direction
// of 2026-09-19 was that they are parts of one product: declared in its
// configuration, started together, stopped together. This is the verb that
// does it. `yoyo start` starts the product's supervisor, which reads the
// services section and starts every enabled part through the same lease-
// checked, repeat-safe path each part already had — `yoyo slack ensure` is
// the pattern, and the scheduler is started as `yoyo work --watch` under its
// own watch lease — and keeps each running within the supervisor's bounds.
// `yoyo stop` stops the supervisor first, so nothing restarts a part on its
// way down, and then the parts in reverse order.
//
// The supervisor is one verb rather than two: `yoyo start` detaches a
// `yoyo start --foreground` into a session of its own and returns, and the
// foreground form is the resident — what the launch agent `yoyo setup`
// installs runs with the machine, and what a deploy replaces in place. Where
// that agent is installed and loaded, `yoyo start` asks launchd to start it
// rather than detaching a supervisor of its own, so the resident is always the
// one launchd restarts. What the supervisor holds is exactly what it needs and
// no more: it never has a Slack token, because the sink's launch reads the
// pair out of the keychain into the sink's own environment and nowhere else,
// and every other child is started with the Slack variables taken out.
//
// The maintenance pass is the supervisor's own rather than a child: every
// `services.maintenance.every` it runs `yoyo reconcile`, rebuilds and takes up
// a build that landed, and records each step in the sweep log, holding every
// restart while the provider is not answering. It is what the interim
// maintenance job did by hand, and what retires it.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/launchd"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/redeploy"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// startWait bounds how long `yoyo start` waits for the detached supervisor to
// record what it started before reporting. The supervisor writes its record on
// its first look, which is immediate, but the sink's start asks the keychain
// and a keychain that prompts can take a while; past this the verb says the
// supervisor is still bringing the parts up and where to read how they stand.
const startWait = 15 * time.Second

// schedulerLogFile is where the supervised scheduler says what it is doing,
// beside the watch log it writes its transitions to.
const schedulerLogFile = "scheduler.log"

// product is one product as the two verbs act on it: its configuration, its
// supervisor's state, and what it takes to start and stop each part. It is
// assembled from the real environment by openProduct and from fakes by the
// tests.
type product struct {
	resolved  config.Resolved
	stateRoot string
	store     *runstate.SupervisionStore
	// program is the binary the supervisor and its children are started from —
	// this one, so what comes up is the build that started it.
	program  string
	launcher slack.Launcher
	environ  []string
	goos     string
	// children assembles the parts to drive, in start order, with the enabled
	// parts nothing can start yet and the parts that are off.
	children func() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error)
	// pass assembles the maintenance pass for a supervisor, or nothing where
	// the section leaves it off, and deployment resolves the binary the
	// supervisor runs so the pass can take an installed build up.
	pass       func(residents maintain.Residents, deployment maintain.Deployment, log func(string, ...any)) (supervise.Pass, error)
	deployment func() (deploymentBinary, error)
	// agent is the launch agent as installed on this machine, or nil where the
	// platform has none, so `yoyo start` can ask launchd for the resident
	// rather than detaching a second kind of one.
	agent *launchd.Agent
	now   func() time.Time
}

// deploymentBinary is what the supervisor needs of the file it is executing:
// what the pass reads to tell a deploy, and what the supervisor restarts
// through. It is satisfied by *redeploy.Binary.
type deploymentBinary interface {
	maintain.Deployment
	supervise.Deployment
}

func runStart(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	foreground := flags.Bool("foreground", false, "run the supervisor in this process rather than detaching it")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "start does not accept positional arguments")
		printStartUsage(stderr)
		return 2
	}
	p, err := openProduct(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	if *foreground {
		return p.supervise(ctx, stdout, stderr)
	}
	return p.start(ctx, stdout, stderr, *jsonOutput)
}

func runStop(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("stop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "stop does not accept positional arguments")
		printStopUsage(stderr)
		return 2
	}
	p, err := openProduct(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "stop failed: %v\n", err)
		return 1
	}
	return p.stop(ctx, stdout, stderr, *jsonOutput)
}

// openProduct assembles the product from the configuration and the machine.
func openProduct(configPath string) (*product, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, err
	}
	store, err := runstate.NewSupervisionStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, err
	}
	// Started from the binary that is starting it rather than from whatever
	// `yoyo` is on the PATH, for the reason the sink is: what comes up is the
	// build that noticed it was needed.
	program, err := os.Executable()
	if err != nil {
		return nil, err
	}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   program,
		launcher:  slack.DetachedLauncher{},
		environ:   os.Environ(),
		goos:      runtime.GOOS,
		now:       time.Now,
	}
	p.children = p.realChildren
	p.pass = p.realPass
	p.deployment = func() (deploymentBinary, error) {
		binary, err := redeploy.Running()
		if err != nil {
			return nil, err
		}
		return binary, nil
	}
	if runtime.GOOS == "darwin" {
		p.agent = launchd.AgentFor(resolved.Config.Product.ID, launchd.Controller{Runner: execution.OSProcessRunner{}, UserHomeDir: os.UserHomeDir, Getuid: os.Getuid})
	}
	return p, nil
}

// startReport is what `yoyo start` says: whether it started a supervisor or
// found one, and what the supervisor recorded about the parts.
type startReport struct {
	Product domain.ProductID `json:"product"`
	// Started reports that this invocation started the supervisor. False is a
	// product that was already running, which is the ordinary answer to a
	// second start and not a failure.
	Started bool   `json:"started"`
	PID     int    `json:"pid,omitempty"`
	Log     string `json:"log,omitempty"`
	// Recorded is the supervisor's record where one was read in time, and
	// Pending says why it was not.
	Recorded *runstate.Supervision `json:"recorded,omitempty"`
	Pending  string                `json:"pending,omitempty"`
	// LaunchAgent names the launchd job the supervisor was started through,
	// where it was, so a reader knows launchd is what restarts it.
	LaunchAgent string `json:"launch_agent,omitempty"`
}

// start starts the supervisor detached, unless one is running, and reports
// what the supervisor then recorded about the parts.
func (p *product) start(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) int {
	productID := p.resolved.Config.Product.ID
	running, err := p.store.Running()
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	if running {
		// A second start while one is running says so and does nothing. What
		// it says is what the running supervisor recorded, because that is what
		// the person typing it wanted to know.
		report := startReport{Product: productID, Started: false}
		if recorded, found, err := p.store.Load(); err != nil {
			report.Pending = fmt.Sprintf("its record could not be read: %v", err)
		} else if found {
			report.Recorded = &recorded
			report.PID = recorded.PID
		} else {
			report.Pending = "it has not recorded the parts yet"
		}
		return p.reportStart(stdout, stderr, jsonOutput, report)
	}
	logRoot, logPath := p.store.SupervisorLog()
	// Where the launch agent is installed and loaded, the resident is launchd's
	// to start: asked for here, it comes up as the job launchd restarts if it
	// dies and starts again with the machine, which a supervisor detached from
	// this terminal would not be.
	if p.agent != nil {
		// The agent has to be this checkout's: one written for another checkout
		// of a product with the same id is that checkout's resident.
		runs, err := p.agent.Runs(p.resolved.Path)
		if err != nil {
			fmt.Fprintf(stderr, "the launch agent %s could not be read, so the supervisor is started from here instead: %v\n", p.agent.Label, err)
		} else if runs {
			if loaded, err := p.agent.Loaded(ctx); err != nil {
				fmt.Fprintf(stderr, "whether the launch agent %s is loaded could not be read, so the supervisor is started from here instead: %v\n", p.agent.Label, err)
			} else if loaded {
				return p.startThroughLaunchd(ctx, stdout, stderr, jsonOutput, filepath.Join(logRoot, filepath.FromSlash(logPath)))
			}
		}
	}
	pid, err := p.launcher.Launch(slack.Launch{
		Program: p.program,
		Args:    []string{"start", "--foreground", "--config", p.resolved.Path},
		Dir:     config.ProjectDirectory(p.resolved.Path),
		// The Slack variables are taken out whatever the shell had exported:
		// the supervisor never holds a token, and the sink's launch puts this
		// product's own pair into the sink's environment and nowhere else.
		Env:     slack.WithoutSecrets(p.environ),
		LogRoot: logRoot,
		Log:     logPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "start failed: start the supervisor for %s: %v\n", productID, err)
		return 1
	}
	report := startReport{
		Product: productID,
		Started: true,
		PID:     pid,
		Log:     filepath.Join(logRoot, filepath.FromSlash(logPath)),
	}
	// The supervisor records the parts on its first look. Waiting for that is
	// what lets this verb say what came up rather than only that something was
	// started to find out.
	recorded, found := p.awaitRecord(ctx, func(recorded runstate.Supervision) bool { return recorded.PID == pid })
	if found {
		report.Recorded = &recorded
	} else {
		report.Pending = "the supervisor is still bringing the parts up; `yoyo status` says how they stand, and its log says what it is doing"
	}
	return p.reportStart(stdout, stderr, jsonOutput, report)
}

// startThroughLaunchd asks launchd to start the agent's job, and waits for the
// supervisor it starts to record the parts. Which process launchd started is
// read from the record rather than from launchd, so the wait is for a record
// newer than any an earlier supervisor left.
func (p *product) startThroughLaunchd(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool, log string) int {
	productID := p.resolved.Config.Product.ID
	before := 0
	if recorded, found, err := p.store.Load(); err == nil && found {
		before = recorded.PID
	}
	if err := p.agent.Kickstart(ctx); err != nil {
		fmt.Fprintf(stderr, "start failed: ask launchd to start %s: %v\n", p.agent.Label, err)
		return 1
	}
	report := startReport{Product: productID, Started: true, Log: log, LaunchAgent: p.agent.Label}
	recorded, found := p.awaitRecord(ctx, func(recorded runstate.Supervision) bool { return recorded.PID != before })
	if found {
		report.Recorded = &recorded
		report.PID = recorded.PID
	} else {
		report.Pending = "launchd was asked to start the supervisor and it has not recorded the parts yet; `yoyo status` says how they stand, and its log says what it is doing"
	}
	return p.reportStart(stdout, stderr, jsonOutput, report)
}

// awaitRecord waits, bounded, for the supervisor just started to write its
// record. A record an earlier supervisor left is not the one being waited
// for, which is why the caller says which record it is waiting for.
func (p *product) awaitRecord(ctx context.Context, wanted func(runstate.Supervision) bool) (runstate.Supervision, bool) {
	deadline := p.now().Add(startWait)
	for {
		recorded, found, err := p.store.Load()
		if err == nil && found && wanted(recorded) {
			return recorded, true
		}
		if !p.now().Before(deadline) || ctx.Err() != nil {
			return runstate.Supervision{}, false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return runstate.Supervision{}, false
		}
	}
}

func (p *product) reportStart(stdout, stderr io.Writer, jsonOutput bool, report startReport) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, report)
	}
	switch {
	case report.Started && report.LaunchAgent != "" && report.PID > 0:
		fmt.Fprintf(stdout, "started the supervisor for %s through the launch agent %s, as pid %d, logging to %s\n", report.Product, report.LaunchAgent, report.PID, report.Log)
	case report.Started && report.LaunchAgent != "":
		fmt.Fprintf(stdout, "asked launchd to start the supervisor for %s through the launch agent %s, logging to %s\n", report.Product, report.LaunchAgent, report.Log)
	case report.Started:
		fmt.Fprintf(stdout, "started the supervisor for %s as pid %d, logging to %s\n", report.Product, report.PID, report.Log)
	case report.PID > 0:
		fmt.Fprintf(stdout, "the product %s is already running: its supervisor is pid %d; nothing was started\n", report.Product, report.PID)
	default:
		fmt.Fprintf(stdout, "the product %s is already running; nothing was started\n", report.Product)
	}
	if report.Recorded != nil {
		for _, child := range report.Recorded.Children {
			fmt.Fprintf(stdout, "  %s\n", supervise.DescribeChild(child))
		}
	}
	if report.Pending != "" {
		fmt.Fprintf(stdout, "  %s\n", report.Pending)
	}
	fmt.Fprintf(stdout, "stop it with `yoyo stop`; `yoyo status` says how each part stands\n")
	return 0
}

// supervise is the foreground form: this process is the supervisor, until it
// is asked to stop. It is what `yoyo start` detaches, and what the launch
// agent runs.
func (p *product) supervise(ctx context.Context, stdout, stderr io.Writer) int {
	children, notYet, off, err := p.children()
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	log := func(format string, args ...any) {
		fmt.Fprintf(stdout, "%s %s\n", p.now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
	supervisor := &supervise.Supervisor{
		Records:  p.store,
		Product:  p.resolved.Config.Product.ID,
		Children: children,
		NotYet:   notYet,
		Off:      off,
		Now:      p.now,
		Log:      log,
		PID:      os.Getpid(),
		Build:    buildinfo.Commit(),
	}
	// Which file this process was started from, read before anything runs, for
	// the reason the watch session reads it then: this is the only moment the
	// answer is known to be about the build that is actually running. A platform
	// that cannot replace a running process supervises without it, and says so.
	var binary deploymentBinary
	if p.deployment != nil {
		resolved, err := p.deployment()
		switch {
		case errors.Is(err, redeploy.ErrUnsupported):
			log("this supervisor will not take up a build deployed over it, and has to be restarted by hand for one: %v", err)
		case err != nil:
			fmt.Fprintf(stderr, "start failed: %v\n", err)
			return 1
		default:
			binary = resolved
			supervisor.Deployment = binary
		}
	}
	if p.pass != nil {
		// The pass is given the same reading of the binary the supervisor
		// restarts through, or none where the platform has none: an interface
		// holding a nil pointer would read as a deployment that is there.
		var deployment maintain.Deployment
		if binary != nil {
			deployment = binary
		}
		pass, err := p.pass(supervisor, deployment, log)
		if err != nil {
			fmt.Fprintf(stderr, "start failed: %v\n", err)
			return 1
		}
		if pass != nil {
			supervisor.Pass = pass
		}
	}
	err = supervisor.Run(ctx)
	switch {
	case errors.Is(err, supervise.ErrAlreadyRunning):
		// The same answer the detaching form gives, and the exit status matches
		// it, because the launch agent runs this form: a job that exited on a
		// refusal launchd read as a failure would be started again every few
		// seconds for as long as the other supervisor ran.
		fmt.Fprintf(stderr, "start refused: %v; a second start while one is running does nothing\n", err)
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	return 0
}

// realPass assembles the maintenance pass from the services section, or
// nothing where the section leaves it off.
func (p *product) realPass(residents maintain.Residents, deployment maintain.Deployment, log func(string, ...any)) (supervise.Pass, error) {
	services := p.resolved.Config.Services
	if !services.Maintenance.Enabled {
		return nil, nil
	}
	productID := p.resolved.Config.Product.ID
	sweeps, err := runstate.NewSweepStore(p.stateRoot, productID)
	if err != nil {
		return nil, err
	}
	outages, err := runstate.NewProviderOutageStore(p.stateRoot, productID)
	if err != nil {
		return nil, err
	}
	pass := &maintain.Pass{
		Product:    productID,
		Every:      services.Maintenance.Every.Duration(),
		Program:    p.program,
		Config:     p.resolved.Path,
		Repository: doctor.RepositoryPath(config.ProjectDirectory(p.resolved.Path), p.resolved.Config.Product.Repository),
		Environ:    slack.WithoutSecrets(p.environ),
		Commit:     buildinfo.Commit(),
		Claims:     sweeps,
		Outages:    outages,
		Deployment: deployment,
		Residents:  residents,
		Runner:     execution.OSProcessRunner{},
		Now:        p.now,
		Log:        log,
	}
	return pass, nil
}

// stopReport is what `yoyo stop` did, in order: the supervisor, then each
// part.
type stopReport struct {
	Product    domain.ProductID      `json:"product"`
	Supervisor supervise.Stopped     `json:"supervisor"`
	Problem    string                `json:"problem,omitempty"`
	Children   []supervise.ChildStop `json:"children"`
}

// stop stops the supervisor and then every part in reverse start order. The
// supervisor goes first so nothing restarts a part as it goes down, and the
// parts are stopped whether or not a supervisor was running: a supervisor
// that died left them running, and this is what stops them.
func (p *product) stop(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) int {
	productID := p.resolved.Config.Product.ID
	report := stopReport{Product: productID, Children: []supervise.ChildStop{}}
	log := func(format string, args ...any) {
		if !jsonOutput {
			fmt.Fprintf(stdout, format+"\n", args...)
		}
	}
	code := 0
	running, err := p.store.Running()
	switch {
	case err != nil:
		report.Problem = fmt.Sprintf("whether a supervisor is running could not be read: %v", err)
		code = 1
	case running:
		recorded, found, err := p.store.Load()
		switch {
		case err != nil:
			report.Problem = fmt.Sprintf("a supervisor is running and its record could not be read, so it could not be named to stop it: %v", err)
			code = 1
		case !found:
			report.Problem = "a supervisor is running and has not recorded which process it is, so it could not be stopped"
			code = 1
		default:
			log("stopping the supervisor, pid %d", recorded.PID)
			stopped, err := supervise.StopHolder(ctx, recorded.PID, p.store.Running)
			report.Supervisor = stopped
			if err != nil {
				report.Problem = fmt.Sprintf("stop the supervisor: %v", err)
				code = 1
			}
		}
	}
	children, _, _, err := p.children()
	if err != nil {
		report.Problem = joinProblem(report.Problem, fmt.Sprintf("the parts could not be assembled to stop them: %v", err))
		code = 1
	} else {
		stops, err := supervise.StopAll(ctx, children, log)
		report.Children = stops
		if err != nil {
			code = 1
		}
	}
	if jsonOutput {
		if written := writeJSON(stdout, stderr, report); written != 0 {
			return written
		}
		return code
	}
	switch {
	case report.Supervisor.WasRunning && report.Supervisor.Detail != "":
		fmt.Fprintf(stdout, "supervisor: asked pid %d to stop; %s\n", report.Supervisor.PID, report.Supervisor.Detail)
	case report.Supervisor.WasRunning:
		fmt.Fprintf(stdout, "supervisor: stopped pid %d\n", report.Supervisor.PID)
	case report.Problem == "":
		fmt.Fprintln(stdout, "supervisor: was not running")
	}
	for _, stop := range report.Children {
		fmt.Fprintf(stdout, "%s\n", stop.Describe())
	}
	if report.Problem != "" {
		fmt.Fprintf(stderr, "stop: %s\n", report.Problem)
	}
	return code
}

func joinProblem(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

// realChildren assembles the parts from the services section: the ones this
// harness can start, in start order — the sink first so it reports the rest
// coming up, then the scheduler — and the enabled ones it cannot start yet,
// each naming the work that adopts it.
func (p *product) realChildren() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error) {
	services := p.resolved.Config.Services
	productID := p.resolved.Config.Product.ID
	var children []supervise.Child
	var notYet []supervise.NotYet
	var off []config.ServiceName
	for _, name := range config.ServiceNames {
		if !services.Enabled(name) {
			off = append(off, name)
			continue
		}
		switch name {
		case config.ServiceSlack:
			store, err := slack.NewStore(p.stateRoot, productID)
			if err != nil {
				return nil, nil, nil, err
			}
			children = append(children, slackChild{
				store: store,
				goos:  p.goos,
				supervisor: slack.Supervisor{
					Store:    store,
					Secrets:  slack.Keychain{Runner: execution.OSProcessRunner{}},
					Launcher: p.launcher,
					Program:  p.program,
					Config:   p.resolved.Path,
					Dir:      config.ProjectDirectory(p.resolved.Path),
					Environ:  p.environ,
				},
			})
		case config.ServiceScheduler:
			watch, err := runstate.NewWatchStore(p.stateRoot, productID)
			if err != nil {
				return nil, nil, nil, err
			}
			children = append(children, schedulerChild{
				watch:      watch,
				launcher:   p.launcher,
				program:    p.program,
				configPath: p.resolved.Path,
				dir:        config.ProjectDirectory(p.resolved.Path),
				environ:    p.environ,
				logRoot:    p.stateRoot,
				log:        "products/" + string(productID) + "/" + schedulerLogFile,
			})
		case config.ServiceDashboard:
			// The dashboard's adoption as a child is yoyodyne-ifd.414; until it
			// lands the command is started by hand, and the product says so
			// rather than starting a part it does not know how to.
			notYet = append(notYet, supervise.NotYet{
				Name:   name,
				Reason: "its adoption is yoyodyne-ifd.414; until that lands, start it with `yoyo dashboard`",
			})
		case config.ServiceMaintenance:
			// The supervisor's own pass rather than a child: assembled by realPass
			// and recorded as scheduled.
		}
	}
	return children, notYet, off, nil
}

// slackChild is the sink as a part of the product. Starting it is exactly
// `yoyo slack ensure`: lease-checked, this product's own stored pair into the
// sink's environment and nowhere else, and nothing done when one is running.
type slackChild struct {
	store      *slack.Store
	supervisor slack.Supervisor
	goos       string
}

func (slackChild) Name() config.ServiceName { return config.ServiceSlack }

func (c slackChild) Running(context.Context) (bool, error) { return c.store.Running() }

func (c slackChild) Ensure(ctx context.Context) (supervise.Ensured, error) {
	if c.goos != "darwin" {
		// The keychain is macOS's, as `yoyo slack ensure` says in the same
		// words; a machine without it has the sink started from its
		// environment file, which is nothing the supervisor can do for it.
		return supervise.Ensured{Unstartable: fmt.Sprintf("its tokens are read from the macOS keychain and this machine is %s; start the sink from the environment file instead: (set -a; . ~/.config/yoyo/%s/slack.env; %s=%s exec yoyo slack)",
			c.goos, c.store.Product(), slack.SecretNamespaceVariable, c.store.Product())}, nil
	}
	supervision, err := c.supervisor.Ensure(ctx)
	if err != nil {
		return supervise.Ensured{}, err
	}
	switch supervision.Outcome {
	case slack.OutcomeStarted:
		return supervise.Ensured{Started: true, PID: supervision.PID, Log: supervision.Log}, nil
	case slack.OutcomeRunning:
		return supervise.Ensured{PID: c.presencePID()}, nil
	case slack.OutcomeSecretsUnavailable:
		return supervise.Ensured{Unstartable: fmt.Sprintf("the keychain would not produce its tokens, %s: %s; `yoyo doctor` says what to store and the command that stores it",
			strings.Join(supervision.Secrets, " and "), supervision.Detail)}, nil
	case slack.OutcomeReportingOff:
		return supervise.Ensured{Unstartable: "reporting is off: " + supervision.Detail}, nil
	}
	return supervise.Ensured{}, fmt.Errorf("the sink's supervision answered %q, which is not an outcome this harness names", supervision.Outcome)
}

// presencePID is the process a running sink recorded itself as, or nothing
// where the record is absent or unreadable — the lease already answered that
// it is running, and a pid is a convenience for the reader.
func (c slackChild) presencePID() int {
	presence, found, err := c.store.LoadPresence()
	if err != nil || !found {
		return 0
	}
	return presence.PID
}

func (c slackChild) Stop(ctx context.Context) (supervise.Stopped, error) {
	running, err := c.store.Running()
	if err != nil {
		return supervise.Stopped{}, err
	}
	if !running {
		return supervise.Stopped{}, nil
	}
	pid := c.presencePID()
	if pid <= 0 {
		return supervise.Stopped{WasRunning: true}, errors.New("a sink is running and its presence record names no process, so it could not be stopped")
	}
	return supervise.StopHolder(ctx, pid, c.store.Running)
}

// schedulerChild is the watch loop as a part of the product: `yoyo work
// --watch`, started detached under its own watch lease, with the Slack
// variables taken out of its environment.
type schedulerChild struct {
	watch      *runstate.WatchStore
	launcher   slack.Launcher
	program    string
	configPath string
	dir        string
	environ    []string
	logRoot    string
	log        string
}

func (schedulerChild) Name() config.ServiceName { return config.ServiceScheduler }

func (c schedulerChild) Running(context.Context) (bool, error) { return c.watch.Held() }

func (c schedulerChild) Ensure(context.Context) (supervise.Ensured, error) {
	held, err := c.watch.Held()
	if err != nil {
		return supervise.Ensured{}, err
	}
	if held {
		return supervise.Ensured{PID: c.holderPID()}, nil
	}
	pid, err := c.launcher.Launch(slack.Launch{
		Program: c.program,
		Args:    []string{"work", "--watch", "--config", c.configPath},
		Dir:     c.dir,
		Env:     slack.WithoutSecrets(c.environ),
		LogRoot: c.logRoot,
		Log:     c.log,
	})
	if err != nil {
		return supervise.Ensured{}, err
	}
	return supervise.Ensured{Started: true, PID: pid, Log: filepath.Join(c.logRoot, filepath.FromSlash(c.log))}, nil
}

func (c schedulerChild) holderPID() int {
	holder, found, err := c.watch.Holder()
	if err != nil || !found {
		return 0
	}
	return holder.PID
}

func (c schedulerChild) Stop(ctx context.Context) (supervise.Stopped, error) {
	held, err := c.watch.Held()
	if err != nil {
		return supervise.Stopped{}, err
	}
	if !held {
		return supervise.Stopped{}, nil
	}
	pid := c.holderPID()
	if pid <= 0 {
		return supervise.Stopped{WasRunning: true}, errors.New("a session is watching and did not record which process it is, so it could not be stopped")
	}
	return supervise.StopHolder(ctx, pid, c.watch.Held)
}

func printStartUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo start [--config <path>] [--foreground] [--json]

Start the product: its supervisor, and through it every part the configuration's
services section enables. A person starts the product once with this rather than
each part by hand.

The supervisor reads the services section and starts each enabled part the way
that part is started alone -- the Slack sink exactly as `+"`yoyo slack ensure`"+`
starts it, from this product's own stored tokens; the scheduler as
`+"`yoyo work --watch`"+` under its own watch lease -- so a part started here holds
exactly what it holds started by hand. It then keeps each running: a part that
dies is started again with backoff, up to five times within two minutes of a
start, and past that it is left down and shown as degraded by `+"`yoyo status`"+`
with the reason. The supervisor's own death leaves the parts running, and the
next `+"`yoyo start`"+` finds them by their leases and takes them back rather than
starting them again.

The maintenance pass is the supervisor's own: every services.maintenance.every
it runs `+"`yoyo reconcile`"+`, rebuilds and takes up a build that landed on the
checkout, keeps the sink up, and records each step in the sweep log, where
`+"`yoyo sweeps`"+` reads it. Nothing is restarted while the provider cannot be reached
or is not logged in, and the scheduler is never stopped for a deploy: it takes a
build up itself, between the runs it hosts.

A second start while the product is running says so and does nothing. Where the
launch agent `+"`yoyo setup`"+` installs is loaded, the supervisor is started through
it, so launchd is what restarts it. A part the section enables that is not yet a
child of the supervisor -- the dashboard -- is reported as such, with the work
that adopts it.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --foreground      be the supervisor in this process rather than detaching one;
                    what the launch agent runs
  --json            emit machine-readable JSON`)
}

func printStopUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo stop [--config <path>] [--json]

Stop the product: the supervisor first, so nothing restarts a part on its way
down, and then every part in the reverse of the order they were started in. A
part that is not running is reported so, and the parts are stopped whether or
not a supervisor was running -- a supervisor that died left them running, and
this is what stops them.

Stopping the scheduler cancels the runs it is hosting, as stopping a watch
session always has; a run cancelled that way is settled by `+"`yoyo reconcile`"+`.
When what you want is for the runs to keep what they have and carry on later,
`+"`yoyo pause`"+` is the verb, and the product stays up.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
