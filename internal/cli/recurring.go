package cli

// Where the schedule meets a role's conversation, and where an operator reads
// what the schedule produced.
//
// A recurring task is wired into the pull for the reason the stopped-work
// delivery beside it is: a firing is conversation turns, and the pull is where
// the harness is already deciding what to do next. It is deliberately not a
// separate daemon, a cron entry, or a launchd job. Every one of those is a second
// thing to install, a second thing to notice has died, and — worst of the three —
// a second invoker of a role, which the harness is the only thing that does.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// recurringTrigger wires the schedule over parts that are already built, so a
// firing happens in the role's own recorded conversation and its report lands
// beside the run state everything else durable lives in.
//
// A project that schedules nothing gets no trigger at all rather than one with an
// empty schedule: the pull then does not carry the field, and nothing about a
// pass mentions a schedule that does not exist. It returns the interface rather
// than the concrete trigger so that "nothing scheduled" is a nil the pull can
// actually test — a typed nil pointer in an interface field is not nil, and the
// pass would call through it.
func recurringTrigger(parts components, configPath string, stderr io.Writer) orchestrator.ScheduleRecurring {
	if len(parts.config.RecurringTasks) == 0 {
		return nil
	}
	return &orchestrator.Trigger{
		// The schedule as this pull read the configuration, so a cadence changed
		// under a running session takes effect at the next pull like every other
		// configured value.
		Tasks: parts.config.RecurringTasks,
		// Where a firing is claimed and paced, so one task fires once per cadence
		// however many sessions are polling.
		Claims:  parts.store.Sweeps(),
		Reports: parts.store.Sweeps(),
		Roles:   roleConversation{configPath: configPath, stderr: stderr},
		// The same pause every run, turn, and delivery reads. A firing is a
		// provider invocation, so `yoyo pause` covers it exactly as it covers them.
		Holds: parts.holds,
	}
}

// roleConversation is a role's own conversation, reached the way an operator
// reaches it: the recorded conversation resumed, one message sent, the reply
// read. Nothing about the turn is special — it is held under the same lease,
// recorded in the same log, charged to the same account, and reads the same
// persona as the conversation an operator opens by hand.
//
// That last part is the whole of why a scheduled wakeup is safe to do at all. A
// turn that read a different personality, or held a different authority, because
// something woke it on a timer would be a second version of a role nobody
// configured.
type roleConversation struct {
	configPath string
	// stderr is where opening the conversation says what it could not read, so a
	// session that started with a warning says so where the operator running it
	// can see it.
	stderr io.Writer
}

// Wake puts one message into a role's conversation and reads the account it gave
// of the pass.
//
// A conversation that could not be opened is reported as unreachable rather than
// as a failed firing, and the difference is what the caller records: nothing was
// asked, so the firing produced no account rather than a bad one. The ordinary
// reasons opening fails — the operator is mid-turn with the role, the provider is
// not signed in, no agent fills the role — are all of that kind.
//
// An answer with no sweep block is not a failed turn. The role answered; what is
// lost is the structure, which the caller says out loud rather than losing the
// turn over.
func (r roleConversation) Wake(ctx context.Context, role domain.AgentRole, message string) (orchestrator.Turn, error) {
	session, lease, err := openChat(ctx, role, "", r.configPath, false, false, r.errors())
	if err != nil {
		return orchestrator.Turn{}, fmt.Errorf("%w: %w", orchestrator.ErrRoleUnreachable, err)
	}
	defer lease.Release()

	reply, err := session.Send(ctx, message)
	// The conversation and what the turn cost are carried whichever way it went: a
	// turn that failed still happened in a conversation somebody can go and read,
	// and the provider charged for it exactly as it charges for one that answered.
	turn := orchestrator.Turn{
		ConversationID: session.Evidence().ConversationID,
		CostUSD:        session.TurnCostUSD(),
	}
	if err != nil {
		return turn, notWoken(err)
	}
	_, result, err := sweep.Extract(reply.Text)
	switch {
	case err != nil:
		turn.ResultProblem = fmt.Sprintf("the %s answered with a sweep block the harness cannot read, so what the pass found is only in the conversation: %v", role, err)
	case result == nil:
		turn.ResultProblem = fmt.Sprintf("the %s answered in prose without a sweep block, so what the pass found is only in the conversation", role)
	default:
		turn.Result = result
	}
	return turn, nil
}

// notWoken marks the failures where the turn asked the role nothing, so what is
// recorded about the firing says so rather than claiming a pass that produced
// nothing. They are the same three the stopped-work delivery names, for the same
// reasons: a provider with no capacity never put the message in front of the
// role, a pause placed between the claim and the turn refused it before the
// provider was reached, and a cancellation is the harness's own death rather than
// anything about the role.
//
// Unlike the delivery, none of them gives anything back. A recurring task's
// cadence is not a budget of attempts at one specific thing: the next firing
// looks at everything this one would have, so it waits for the next cadence
// rather than being retried at once against a provider that is still out of
// capacity.
func notWoken(err error) error {
	var held *chat.OperatorHoldError
	if errors.Is(err, chat.ErrProviderCapacity) || errors.As(err, &held) || errors.Is(err, chat.ErrTurnAbandoned) {
		return fmt.Errorf("%w: %w", orchestrator.ErrRoleUnreachable, err)
	}
	return err
}

func (r roleConversation) errors() io.Writer {
	if r.stderr == nil {
		return io.Discard
	}
	return r.stderr
}

// maxRenderedSweeps bounds how many recorded sweeps one listing shows. The most
// recent are kept rather than the oldest: what an operator needs from a schedule
// is what it has been doing lately, and the count says how much of it they are
// not looking at.
const maxRenderedSweeps = 20

type sweepsOutput struct {
	Sweeps []runstate.Sweep `json:"sweeps"`
	Error  string           `json:"error,omitempty"`
}

// readSweeps shows what the recurring tasks have produced. It is read-only: a
// sweep is written once and never revised, and nothing here fires one, retires
// one, or decides anything about what a pass found.
//
// It exists for the same reason `yoyo reports` does. A pass that runs at three in
// the morning tells nobody anything if reading it costs an interactive
// conversation with a provider behind it, and a schedule whose output is out of
// reach of anything scripted is one nobody will ever summarize.
func readSweeps(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sweeps", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	task := flags.String("task", "", "show only this recurring task's sweeps (default: all of them)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sweeps does not accept positional arguments: name a task with --task")
		printSweepsUsage(stderr)
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportSweepFailure(stdout, stderr, *jsonOutput, err)
	}
	recorded, err := parts.store.Sweeps().List()
	if err != nil {
		return reportSweepFailure(stdout, stderr, *jsonOutput, err)
	}
	if named := strings.TrimSpace(*task); named != "" {
		filtered := make([]runstate.Sweep, 0, len(recorded))
		for _, entry := range recorded {
			if entry.Task == named {
				filtered = append(filtered, entry)
			}
		}
		recorded = filtered
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, sweepsOutput{Sweeps: recorded})
	}
	fmt.Fprint(stdout, renderSweeps(recorded))
	return 0
}

func reportSweepFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, sweepsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "sweeps failed: %v\n", err)
	return 1
}

// renderSweeps writes the pile most recent first, because what a reader wants
// from a schedule is what it has been doing lately.
func renderSweeps(recorded []runstate.Sweep) string {
	if len(recorded) == 0 {
		return "no recurring task has recorded a sweep yet\n"
	}
	var rendered strings.Builder
	shown := recorded
	if len(shown) > maxRenderedSweeps {
		shown = shown[len(shown)-maxRenderedSweeps:]
		fmt.Fprintf(&rendered, "%d sweep(s) recorded; the most recent %d follow\n\n", len(recorded), len(shown))
	}
	for index := len(shown) - 1; index >= 0; index-- {
		rendered.WriteString(renderSweep(shown[index]))
		if index > 0 {
			rendered.WriteString("\n")
		}
	}
	return rendered.String()
}

// renderSweep writes one pass: what it was, what it found, and what it needs.
// The questions come first among the findings for the reason the report exists —
// a report with no questions asks for nothing, and an operator reading these at
// leisure has to be able to see that at a glance.
func renderSweep(recorded runstate.Sweep) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s  %s (%s), %d turn(s)",
		recorded.StartedAt.UTC().Format(time.RFC3339), recorded.Task, recorded.Role, recorded.Turns)
	if recorded.CostUSD > 0 {
		fmt.Fprintf(&rendered, ", $%.4f", recorded.CostUSD)
	}
	rendered.WriteString("\n")
	if recorded.Result == nil {
		fmt.Fprintf(&rendered, "  no account of this pass was recorded: %s\n", nonEmptySweepProblem(recorded.Problem))
		return rendered.String()
	}
	for _, question := range recorded.Result.Questions {
		fmt.Fprintf(&rendered, "  ? %s\n", question)
	}
	if summary := strings.TrimSpace(recorded.Result.Summary); summary != "" {
		fmt.Fprintf(&rendered, "  %s\n", summary)
	}
	for _, finding := range recorded.Result.Findings {
		fmt.Fprintf(&rendered, "  - [%s] %s\n", finding.Disposition, finding.Issue)
		if detail := strings.TrimSpace(finding.Detail); detail != "" {
			fmt.Fprintf(&rendered, "      %s\n", detail)
		}
		if len(finding.Filed) > 0 {
			fmt.Fprintf(&rendered, "      filed: %s\n", strings.Join(finding.Filed, ", "))
		}
		if finding.SilentRepair() {
			fmt.Fprintf(&rendered, "      fixed with nothing filed for the root cause\n")
		}
	}
	if recorded.Problem != "" {
		fmt.Fprintf(&rendered, "  %s\n", recorded.Problem)
	}
	return rendered.String()
}

func nonEmptySweepProblem(problem string) string {
	if trimmed := strings.TrimSpace(problem); trimmed != "" {
		return trimmed
	}
	return "nothing was recorded about what stopped it"
}

func printSweepsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo sweeps [options]

What the recurring tasks found on their own cadence. A recurring task wakes a
role every so often to look at its own domain -- the development manager over
work that has stopped moving, say -- and nobody is watching those turns, so each
firing ends in a durable report. This is where they are read.

Each pass leads with the questions it could not settle itself, because that is
the only part of a report that asks for anything: a report with no questions
needs no attention. Below them come the pass's summary and what it found, each
finding with what the role did about it -- fixed, filed, consulted, or left --
and the work it filed for the root cause. A fix that filed nothing is named as
one, which is the whole of what a run of these reports is read for.

Three outcomes look alike and are not: a pass that found nothing shows its own
summary and no findings, which on a healthy harness is most of them; a pass that
produced no account says so and names what stopped it; and a pass stopped by its
turn bound is recorded as partial, so it is never mistaken for a finished one.

It is read-only. A sweep is written once and never revised, and nothing here
fires one, retires one, or decides anything about what a pass found. Which tasks
run, how often, and what they are told is configuration; see the recurring tasks
section of docs/configuration.md.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --task <name>     show only this recurring task's sweeps
  --json            emit machine-readable JSON`)
}
