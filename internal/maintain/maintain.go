// Package maintain is the product supervisor's periodic pass: what the interim
// maintenance job did by hand every ten minutes, taken by the resident on a
// configured cadence and written down each time.
//
// The job it absorbs ran as a launchd script outside the repository, and its
// history is the reason each rule here is what it is. It rewrote four tracker
// statuses with a bare `bd update --status` nobody could see, so this pass
// writes no tracker status at all: the one tracker-touching step is `yoyo
// reconcile`, whose every write is a recorded settlement with a note. It
// force-restarted the watch 158 times while the provider's login was expired,
// so nothing here restarts anything while the provider cannot be reached or is
// not logged in. It bounced a live watch session for a redeploy and cancelled
// a run, so the scheduler is never stopped by this pass: a session takes up a
// build itself, between the runs it hosts, on its own drain rules. And it ran
// as a job with no record of its own, so each pass here is a sweep record
// beside the recurring tasks', with every step it took and every step it
// skipped saying why.
//
// The steps, in the order they are taken: read whether the provider is
// answering, which decides the restarts later on; `yoyo reconcile`, which
// settles what interrupted runs left behind, converges the checkout onto the
// forge, and takes the stall reading; rebuild, which builds and installs the
// binary when the checkout has moved past the running build and the running
// binary is this repository's own; redeploy, which stops a sink started from
// the previous build so the supervisor starts it again from the new one and
// then restarts the supervisor itself in place; and the sink, which is `yoyo
// slack ensure` as the supervisor's own look already takes it.
package maintain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The step names, as the sweep record carries them.
const (
	StepProvider  = "provider"
	StepReconcile = "reconcile"
	StepRebuild   = "rebuild"
	StepRedeploy  = "redeploy"
	StepSlack     = "slack"
)

// The bounds on what the pass runs. Reconcile asks the forge about every
// unsettled publication and waits a dropped connection out on its own backoff,
// so it is given long enough to do that and not long enough to hold the
// supervisor's looks at the children for the rest of the day; a build is
// bounded the way a check is.
const (
	ReconcileTimeout = 15 * time.Minute
	BuildTimeout     = 15 * time.Minute
	// maxOutputBytes bounds how much of a command's output one step's detail
	// carries. The whole of it is in the supervisor's log.
	maxOutputBytes = 1 << 10
)

// Claims is where the pass records its cadence and its report: the sweep
// store, which is what every recurring task records in.
type Claims interface {
	Claim(ctx context.Context, task string, every time.Duration, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
	Find(task string) (runstate.SweepClaim, bool, error)
	Append(recorded runstate.Sweep) error
}

// Outages is the product's record of the provider answering nobody, which is
// the guard every restart here reads. It is satisfied by
// *runstate.ProviderOutageStore.
type Outages interface {
	Standing() (runstate.ProviderOutage, bool, error)
}

// Deployment is the binary the supervisor is executing: whether the file has
// been written since, and when. It is satisfied by *redeploy.Binary.
type Deployment interface {
	Replaced() (bool, error)
	InstalledAt() (time.Time, error)
}

// Residents is the supervisor as the pass sees it: what it last knew about each
// child, and the one thing the pass asks of it — to stop a running child so the
// supervisor's own look starts it again from the installed build. It is
// satisfied by *supervise.Supervisor.
type Residents interface {
	ChildState(name config.ServiceName) (runstate.SupervisedChild, bool)
	Restart(ctx context.Context, name config.ServiceName, reason string) (runstate.SupervisedChild, error)
}

// Pass is the supervisor's periodic pass over one product.
type Pass struct {
	Product domain.ProductID
	// Every is the cadence, from the services section.
	Every time.Duration
	// Program is the binary the supervisor is running, which is what
	// `reconcile` is run from, and Config is the configuration file it reads.
	Program string
	Config  string
	// Repository is the product's repository, where reconcile runs and where a
	// rebuild is made.
	Repository string
	// Environ is what the commands the pass runs are given, with the Slack
	// variables already taken out by whoever started the supervisor.
	Environ []string
	// Commit is the revision the running binary was built from, empty where it
	// carries none.
	Commit string

	Claims     Claims
	Outages    Outages
	Deployment Deployment
	Residents  Residents
	Runner     execution.ProcessRunner

	Now func() time.Time
	Log func(format string, args ...any)

	// nextDue is when the cadence next fires, kept so a supervisor looking every
	// few seconds does not open the claim each time.
	nextDue time.Time
	last    *runstate.Sweep
}

// Due reports whether the cadence has come round. It reads the durable claim
// once and then keeps the answer, so the supervisor's every look does not open
// the sweep store.
func (p *Pass) Due(now time.Time) bool {
	if p.nextDue.IsZero() {
		claimed, found, err := p.Claims.Find(config.MaintenanceTaskName)
		if err != nil || !found {
			// A claim that cannot be read is answered by claiming, which refuses
			// on its own reading; a claim nobody made is a pass that is due.
			return true
		}
		p.nextDue = claimed.NextDue(p.every())
	}
	return !now.Before(p.nextDue)
}

// Describe is the pass's line in the supervisor's record: its cadence, and
// what the last pass came to.
func (p *Pass) Describe() string {
	said := fmt.Sprintf("every %s", p.every())
	if p.last != nil {
		said += fmt.Sprintf("; last pass at %s (%s)", p.last.StartedAt.UTC().Format(time.RFC3339), summarize(p.last.Steps))
	}
	if !p.nextDue.IsZero() {
		said += fmt.Sprintf("; next at %s", p.nextDue.UTC().Format(time.RFC3339))
	}
	return said
}

// Run takes one pass if one is due, records it, and reports what the supervisor
// has to do afterwards. It never returns an error: a pass that failed is a fact
// in the record, and the next pass looks at everything this one would have.
func (p *Pass) Run(ctx context.Context) supervise.PassOutcome {
	now := p.now()
	claimed, err := p.Claims.Claim(ctx, config.MaintenanceTaskName, p.every(), now)
	if err != nil {
		if errors.Is(err, runstate.ErrSweepNotDue) {
			var notDue runstate.SweepNotDueError
			if errors.As(err, &notDue) {
				p.nextDue = notDue.NextDue
			}
		} else {
			p.log("the maintenance pass could not be claimed, so it was not taken: %v", err)
		}
		return supervise.PassOutcome{Skipped: true}
	}
	p.nextDue = claimed.NextDue(p.every())
	p.log("maintenance pass %d starting", claimed.Firings)

	recorded := runstate.Sweep{Task: config.MaintenanceTaskName, StartedAt: now}
	var outcome supervise.PassOutcome
	// The provider first, because it decides the restarts below.
	held, reason := p.providerHeld(&recorded)
	p.reconcile(ctx, &recorded)
	replaced := p.rebuild(ctx, &recorded)
	outcome.TakeUpDeploy = p.redeploy(ctx, &recorded, replaced, held, reason)
	p.slack(&recorded)

	recorded.EndedAt = p.now()
	recorded.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: summarize(recorded.Steps)}
	recorded.Problem = bounded(failures(recorded.Steps))
	if err := p.Claims.Append(recorded); err != nil {
		p.log("the maintenance pass could not be recorded, so `yoyo sweeps` will not show it: %v", err)
	}
	if _, err := p.Claims.Settle(ctx, config.MaintenanceTaskName, recorded.Problem); err != nil {
		p.log("the maintenance pass's claim could not be settled: %v", err)
	}
	p.last = &recorded
	return outcome
}

// providerHeld reads whether the provider is answering. While it is not,
// nothing is restarted: a restart cannot renew a login or bring a network back,
// and it can kill a process that is waiting one of them out. The record says
// which it is, so a reader sees the hold rather than a pass that quietly did
// less.
func (p *Pass) providerHeld(recorded *runstate.Sweep) (bool, string) {
	if p.Outages == nil {
		p.step(recorded, StepProvider, runstate.StepSkipped, "no outage record is wired, so the pass cannot tell whether the provider is answering and restarts nothing")
		return true, "whether the provider is answering could not be read"
	}
	outage, standing, err := p.Outages.Standing()
	if err != nil {
		reason := fmt.Sprintf("whether the provider is answering could not be read: %v", err)
		p.step(recorded, StepProvider, runstate.StepFailed, reason)
		return true, reason
	}
	if !standing {
		p.step(recorded, StepProvider, runstate.StepRan, "answering")
		return false, ""
	}
	reason := outage.Says()
	p.step(recorded, StepProvider, runstate.StepRan, reason+"; nothing is restarted this pass")
	return true, reason
}

// reconcile runs `yoyo reconcile` from the running binary. It is a subprocess
// rather than a call because the verb is the whole of the settlement — runs,
// publications, convergence, the stall reading — and a second copy of its
// wiring here would be a second reconcile to keep in step with the first.
func (p *Pass) reconcile(ctx context.Context, recorded *runstate.Sweep) {
	if p.Runner == nil {
		p.step(recorded, StepReconcile, runstate.StepSkipped, "no process runner is wired, so nothing can be run")
		return
	}
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    p.Program,
		Args:    []string{"reconcile", "--config", p.Config},
		Dir:     p.Repository,
		Env:     execution.WithGitMaintenanceFence(p.Environ),
		Timeout: ReconcileTimeout,
	}, nil)
	if err != nil {
		p.step(recorded, StepReconcile, runstate.StepFailed, fmt.Sprintf("yoyo reconcile could not be run: %v", err))
		return
	}
	detail := describeResult(result)
	if result.Status != execution.ProcessSucceeded {
		p.step(recorded, StepReconcile, runstate.StepFailed, detail)
		return
	}
	p.step(recorded, StepReconcile, runstate.StepRan, detail)
}

// rebuild builds and installs the binary when the checkout has moved past the
// build that is running, so the self-redeploy has a build to take up and the
// staleness warning is transient with no human step. It reports whether a
// build newer than the running one is now installed, whichever of the two put
// it there.
//
// It applies to one shape of installation: the running binary is inside the
// product's own repository, which is a harness developing itself. An installed
// release is not rebuilt from a repository it was not built from.
func (p *Pass) rebuild(ctx context.Context, recorded *runstate.Sweep) bool {
	replaced, err := p.replaced()
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepFailed, err.Error())
		return false
	}
	if replaced {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("a build newer than the running one is already installed at %s and waiting to be taken up", p.Program))
		return true
	}
	if !within(p.Repository, p.Program) {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("the running binary %s is not inside the repository %s, so there is nothing here to rebuild; a release is installed by hand", p.Program, p.Repository))
		return false
	}
	if p.Runner == nil {
		p.step(recorded, StepRebuild, runstate.StepSkipped, "no process runner is wired, so nothing can be built")
		return false
	}
	if strings.TrimSpace(p.Commit) == "" {
		p.step(recorded, StepRebuild, runstate.StepSkipped, "the running binary carries no revision, so whether the checkout has moved past it cannot be told")
		return false
	}
	head, err := p.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepFailed, fmt.Sprintf("the checkout's tip could not be read: %v", err))
		return false
	}
	head = strings.TrimSpace(head)
	if head == p.Commit {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("the running build is the checkout's tip %s", short(head)))
		return false
	}
	changed, err := p.git(ctx, "diff", "--name-only", p.Commit, head, "--", "cmd", "internal", "go.mod", "go.sum", "Makefile")
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("what changed between the running build %s and the tip %s could not be read, so no build was made: %v", short(p.Commit), short(head), err))
		return false
	}
	if strings.TrimSpace(changed) == "" {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("nothing that goes into the binary changed between the running build %s and the tip %s", short(p.Commit), short(head)))
		return false
	}
	// The tracker's export is dirty in the ordinary course of things and goes
	// into no binary; anything else uncommitted would be built into a resident
	// as nobody's revision.
	dirty, err := p.git(ctx, "status", "--porcelain", "--untracked-files=no", "--", ".", ":(exclude).beads")
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepFailed, fmt.Sprintf("whether the checkout is clean could not be read: %v", err))
		return false
	}
	if strings.TrimSpace(dirty) != "" {
		p.step(recorded, StepRebuild, runstate.StepSkipped, fmt.Sprintf("the checkout has uncommitted changes to tracked files (%s), and a build of them would be a build of nobody's revision", firstLine(dirty)))
		return false
	}
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    "make",
		Args:    []string{"build"},
		Dir:     p.Repository,
		Env:     execution.WithGitMaintenanceFence(p.Environ),
		Timeout: BuildTimeout,
	}, nil)
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepFailed, fmt.Sprintf("make build could not be run: %v", err))
		return false
	}
	if result.Status != execution.ProcessSucceeded {
		p.step(recorded, StepRebuild, runstate.StepFailed, "make build: "+describeResult(result))
		return false
	}
	replaced, err = p.replaced()
	if err != nil {
		p.step(recorded, StepRebuild, runstate.StepFailed, fmt.Sprintf("built, and then: %v", err))
		return false
	}
	p.step(recorded, StepRebuild, runstate.StepRan, fmt.Sprintf("built and installed %s at the tip %s over the running build %s", p.Program, short(head), short(p.Commit)))
	return replaced
}

// redeploy takes the installed build up: the sink is stopped so the
// supervisor's next look starts it from the new binary, the scheduler is left
// to take the build up itself between the runs it hosts, and the supervisor
// restarts in place once this record is written. None of it happens while the
// provider is not answering.
func (p *Pass) redeploy(ctx context.Context, recorded *runstate.Sweep, replaced, held bool, reason string) bool {
	if !replaced {
		p.step(recorded, StepRedeploy, runstate.StepSkipped, "the running build is the one installed; nothing to take up")
		return false
	}
	if held {
		p.step(recorded, StepRedeploy, runstate.StepSkipped, fmt.Sprintf("a build is installed and waiting, and nothing is restarted while %s", reason))
		return false
	}
	var said []string
	installed, err := p.Deployment.InstalledAt()
	if err != nil {
		p.step(recorded, StepRedeploy, runstate.StepFailed, err.Error())
		return false
	}
	if sink, known := p.Residents.ChildState(config.ServiceSlack); known && sink.State == runstate.ChildRunning {
		// A sink this supervisor started after the build was installed is
		// already the new build. One it started before, or reattached and never
		// started, predates it.
		if sink.StartedAt.IsZero() || sink.StartedAt.Before(installed) {
			why := fmt.Sprintf("stopped to be started again from the build installed at %s", installed.UTC().Format(time.RFC3339))
			if _, err := p.Residents.Restart(ctx, config.ServiceSlack, why); err != nil {
				said = append(said, fmt.Sprintf("the sink could not be stopped for the new build: %v", err))
			} else {
				said = append(said, "the sink was stopped and the supervisor's next look starts it from the installed build")
			}
		} else {
			said = append(said, "the sink is already running from the installed build")
		}
	}
	if scheduler, known := p.Residents.ChildState(config.ServiceScheduler); known && scheduler.State == runstate.ChildRunning {
		said = append(said, "the scheduler is left to take the build up itself, between the runs it hosts; a session is never stopped for a deploy")
	}
	said = append(said, "the supervisor restarts into the installed build once this record is written")
	p.step(recorded, StepRedeploy, runstate.StepRan, strings.Join(said, "; "))
	return true
}

// slack is `yoyo slack ensure`, which the supervisor's own look at the sink
// already takes every few seconds: lease-checked, from this product's own
// stored tokens, nothing done while one is running. The step records what that
// look came to rather than asking the keychain a second time.
func (p *Pass) slack(recorded *runstate.Sweep) {
	sink, known := p.Residents.ChildState(config.ServiceSlack)
	if !known {
		p.step(recorded, StepSlack, runstate.StepSkipped, "the sink is not a child of this supervisor: services.slack is off, or its adoption has not landed")
		return
	}
	switch sink.State {
	case runstate.ChildRunning:
		detail := "running"
		if sink.PID > 0 {
			detail = fmt.Sprintf("running as pid %d", sink.PID)
		}
		p.step(recorded, StepSlack, runstate.StepRan, detail+"; the supervisor's own look keeps it up, which is `yoyo slack ensure` taken every few seconds")
	case runstate.ChildDown:
		p.step(recorded, StepSlack, runstate.StepRan, "down, and the supervisor is restarting it: "+sink.Reason)
	case runstate.ChildDegraded:
		p.step(recorded, StepSlack, runstate.StepFailed, "degraded, and the supervisor has stopped restarting it: "+sink.Reason)
	default:
		p.step(recorded, StepSlack, runstate.StepSkipped, fmt.Sprintf("%s: %s", sink.State, sink.Reason))
	}
}

func (p *Pass) replaced() (bool, error) {
	if p.Deployment == nil {
		return false, errors.New("which binary the supervisor is running is not known on this platform, so no build can be taken up")
	}
	return p.Deployment.Replaced()
}

func (p *Pass) git(ctx context.Context, args ...string) (string, error) {
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    args,
		Dir:     p.Repository,
		Env:     execution.WithGitMaintenanceFence(p.Environ),
		Timeout: time.Minute,
	}, nil)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), describeResult(result))
	}
	return result.Stdout, nil
}

func (p *Pass) step(recorded *runstate.Sweep, name string, outcome runstate.SweepStepOutcome, detail string) {
	detail = bounded(detail)
	recorded.Steps = append(recorded.Steps, runstate.SweepStep{Name: name, Outcome: outcome, Detail: detail})
	p.log("maintenance: %s: %s, %s", name, outcome, detail)
}

func (p *Pass) every() time.Duration {
	if p.Every > 0 {
		return p.Every
	}
	return config.DefaultMaintenanceInterval
}

func (p *Pass) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pass) log(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// summarize is the pass in a sentence: how many steps ran, were skipped, and
// failed.
func summarize(steps []runstate.SweepStep) string {
	ran, skipped, failed := 0, 0, 0
	for _, step := range steps {
		switch step.Outcome {
		case runstate.StepRan:
			ran++
		case runstate.StepSkipped:
			skipped++
		case runstate.StepFailed:
			failed++
		}
	}
	return fmt.Sprintf("%d step(s) ran, %d skipped, %d failed", ran, skipped, failed)
}

// failures is what went wrong, for the record's problem and the claim's, and
// empty on a pass in which nothing did.
func failures(steps []runstate.SweepStep) string {
	var said []string
	for _, step := range steps {
		if step.Outcome == runstate.StepFailed {
			said = append(said, step.Name+": "+step.Detail)
		}
	}
	return strings.Join(said, "; ")
}

// describeResult is a command's ending in a line: its status and what it said,
// bounded.
func describeResult(result execution.ProcessResult) string {
	said := fmt.Sprintf("exit %d", result.ExitCode)
	output := strings.TrimSpace(result.Stdout)
	if result.Status != execution.ProcessSucceeded {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			output = stderr
		}
		said = fmt.Sprintf("%s (exit %d)", result.Status, result.ExitCode)
	}
	if output == "" {
		return said
	}
	return said + ": " + singleLine(output, maxOutputBytes)
}

func within(directory, path string) bool {
	root, err := filepath.Abs(directory)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// singleLine folds output onto one line and cuts it at the bound, never
// mid-character.
func singleLine(text string, limit int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := folded[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "…"
}

// bounded keeps a detail inside what the record accepts.
func bounded(text string) string {
	if len(text) <= runstate.MaxSweepTextBytes {
		return text
	}
	return singleLine(text, runstate.MaxSweepTextBytes-4)
}
