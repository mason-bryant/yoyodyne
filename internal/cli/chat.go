package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// chatWorkItemStatus is the tracker slice a product conversation is built from.
// Open work is what product intent is currently being spent on; the closed
// history is not what the operator is deciding about.
const chatWorkItemStatus = "open"

// chatTrackerTimeout bounds one tracker command taken on a conversation's
// behalf, whether that is reading the state the product context is assembled
// from or creating an item the operator approved. An unresponsive tracker
// delays a conversation rather than hanging it at the prompt.
const chatTrackerTimeout = 30 * time.Second

type chatOutput struct {
	Evidence *chat.Evidence `json:"evidence,omitempty"`
	Reply    string         `json:"reply,omitempty"`
	// Proposals are what the turn proposed and nothing more. A one-shot message
	// has nobody to approve anything, so they are reported for a person to
	// decide on in a conversation rather than acted on here.
	Proposals []chat.PendingProposal `json:"proposals,omitempty"`
	// Concerns are what the product manager would not propose until somebody
	// answers it. A one-shot message has nobody to answer, so they are reported
	// as the open questions they are rather than as work.
	Concerns []chat.PendingConcern `json:"concerns,omitempty"`
	// Actions are the tracker changes the product manager made while answering.
	// Unlike proposals they already happened, so they are reported rather than
	// offered.
	Actions []chat.TrackerOutcome `json:"actions,omitempty"`
	// ResultsCarriedOver reports that the reply stopped where it did because the
	// product manager ran out of rounds of tracker actions, with results it has
	// not seen. They are recorded with the conversation and reach it when the
	// conversation is next spoken to, which for a one-shot message is a later
	// invocation.
	ResultsCarriedOver bool `json:"results_carried_over,omitempty"`
	// Reports are what the product manager filed for the operator while it
	// answered, and ReportProblem is one that could not be read or kept. Both
	// are reported here for the same reason the actions are: they already
	// happened, and a report nobody is shown is one nobody reads.
	Reports       []report.Report `json:"reports,omitempty"`
	ReportProblem string          `json:"report_problem,omitempty"`
	Error         string          `json:"error,omitempty"`
}

func runChat(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	message := flags.String("message", "", "send one message and print the reply instead of opening an interactive conversation")
	fresh := flags.Bool("new", false, "start a new conversation instead of resuming the recorded one")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON (requires --message)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "chat does not accept positional arguments; use --message to send one message")
		printChatUsage(stderr)
		return 2
	}
	if *jsonOutput && *message == "" {
		fmt.Fprintln(stderr, "chat --json requires --message: an interactive conversation has no single result to encode")
		return 2
	}

	session, lease, err := openChat(ctx, *configPath, *fresh, stderr)
	if err != nil {
		return reportChatFailure(stdout, stderr, *jsonOutput, nil, err)
	}
	defer lease.Release()

	if *message != "" {
		// A one-shot message resumes the same recorded conversation an
		// interactive one does, and carries the same risk of answering from a
		// picture taken hours ago. It is said on stderr because stdout is the
		// reply and, with --json, a document.
		fmt.Fprintln(stderr, session.Freshness(ctx))
		reply, err := session.Send(ctx, *message)
		if err != nil {
			// The answer travels with the failure. A turn that produced one is
			// worth reading even when what it proposed could not be read.
			return reportChatFailure(stdout, stderr, *jsonOutput, &reply, err)
		}
		if *jsonOutput {
			evidence := reply.Evidence
			return writeJSON(stdout, stderr, chatOutput{
				Evidence:           &evidence,
				Reply:              reply.Text,
				Proposals:          reply.Proposals,
				Concerns:           reply.Concerns,
				Actions:            reply.Actions,
				ResultsCarriedOver: reply.ResultsCarriedOver,
				Reports:            reply.Reports,
				ReportProblem:      reply.ReportProblem,
			})
		}
		fmt.Fprintln(stdout, reply.Text)
		printChatActions(stdout, reply.Actions, reply.ResultsCarriedOver)
		printChatReports(stdout, reply.Reports, reply.ReportProblem)
		printChatConcerns(stdout, reply.Concerns)
		printChatProposals(stdout, reply.Proposals)
		printChatEvidence(stdout, reply.Evidence)
		return 0
	}

	// The conversation is held over a console rather than the raw streams: on a
	// terminal that gives the operator's typing a region output never writes
	// into, and anywhere else it is the same conversation as an ordinary stream
	// of text. Either way what is recorded is identical.
	screen := console.Open(console.Options{In: stdin, Out: stdout})
	defer screen.Close()
	printChatHeader(screen, session.Evidence(), session.Freshness(ctx))
	converseErr := session.Converse(ctx, screen)
	// What was decided about is the console's to dress, and it is asked before
	// the console is closed: restoring the terminal changes how it reads input,
	// not what it is allowed to be shown in.
	theme := screen.Theme()
	// The terminal is handed back before the closing report, so the evidence
	// below is written to a terminal in the state the operator's shell left it.
	if err := screen.Close(); err != nil {
		fmt.Fprintf(stderr, "restore the terminal: %v\n", err)
	}
	printOpenConcerns(stdout, theme, session.Concerns())
	printUndecidedProposals(stdout, theme, session.Proposals())
	printChatEvidence(stdout, session.Evidence())
	if converseErr != nil {
		fmt.Fprintf(stderr, "conversation ended: %v\n", converseErr)
		return 1
	}
	return 0
}

// openChat builds the product manager's conversation from configuration: the
// configured agent, the repository's own Markdown, the tracker state as it
// stands, the harness the operator steers work with, and the durable record a
// previous process left behind. The returned lease is this process's exclusive
// hold on that conversation.
func openChat(ctx context.Context, configPath string, fresh bool, stderr io.Writer) (*chat.Session, *runstate.Lease, error) {
	// The conversation is built over the same components a run is, because
	// steering work from inside it means executing exactly the runs
	// `yoyodyne run` would have executed.
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, nil, err
	}
	cfg := parts.config
	repository := parts.repository

	agent := agentForRole(cfg, domain.RoleProductManager)
	if agent.Role != domain.RoleProductManager {
		return nil, nil, errors.New("no product-manager agent is configured; chat has nobody to talk to")
	}
	if agent.Backend != domain.BackendClaudeCode {
		return nil, nil, fmt.Errorf("chat requires a claude-code product manager, configured backend is %q", agent.Backend)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		return nil, nil, fmt.Errorf("product-manager agent %s", err)
	}

	processRunner := parts.runner
	provider := claudecode.Backend{Runner: processRunner}
	availability, err := provider.CheckAvailability(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !availability.Installed {
		return nil, nil, errors.New("Claude Code is not installed")
	}
	if !availability.Authenticated {
		return nil, nil, fmt.Errorf("Claude Code is not authenticated; run `claude auth login` before starting a conversation (auth method: %s)", availability.AuthMethod)
	}

	store, err := runstate.NewConversationStore(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return nil, nil, err
	}
	lease, err := store.Hold(domain.RoleProductManager)
	if err != nil {
		return nil, nil, err
	}

	// The goals the repository records, which is what work admitted in this
	// conversation has to name. A repository whose goals cannot be read still
	// opens a conversation: what it costs is the check, and every action that
	// would have been checked says so rather than reading as approved.
	goals, err := loadGoals(repository, cfg.Product)
	if err != nil {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	for _, problem := range goals.Problems {
		fmt.Fprintf(stderr, "warning: goals not read: %s\n", problem)
	}

	ground := newConversationGround(parts)
	briefing, err := ground.Gather(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, lease.Release())
	}
	for _, problem := range briefing.Problems {
		fmt.Fprintf(stderr, "warning: %s\n", problem)
	}
	session, err := chat.Open(chat.Options{
		Backend: provider,
		Store:   store,
		// The tracker and the harness behind the operator's commands are the
		// harness's own hands, not the product manager's: they are used only
		// where an operator approved a proposal or asked for something.
		Tracker: chatTracker(processRunner, repository),
		Work:    newConversationWork(parts),
		// The collected reports are the same pile the runs fill, read and written
		// from here because this conversation is where the operator already is.
		Reports: parts.reports,
		// What work admitted here has to name. It is read from the repository
		// rather than from the conversation, so a goal retired since the
		// conversation opened stops being one work can be admitted under.
		Goals:        goals,
		Model:        agent.Model,
		Persona:      agent.Persona.Text,
		Agent:        agentNameForRole(cfg, domain.RoleProductManager),
		Provider:     domain.BackendClaudeCode,
		Repository:   repository,
		ProductID:    cfg.Product.ID,
		RepositoryID: string(cfg.Product.RepositoryID),
		Briefing:     briefing,
		// The repository and the tracker are kept reachable so the conversation
		// can say how old its picture is and take a new one when the operator
		// asks. The product manager reaches neither: this is the harness's hand,
		// like the work it steers.
		Ground:       ground,
		RedactValues: parts.redactValues,
		Fresh:        fresh,
	})
	if err != nil {
		return nil, nil, errors.Join(err, lease.Release())
	}
	return session, lease, nil
}

// chatTracker is the work-item client a conversation acts through: it reads the
// tracker state the product context is built from, and it is what an approved
// proposal is created with. Both are bounded the same way, so no tracker call a
// conversation makes can outlast the operator's patience.
func chatTracker(runner execution.ProcessRunner, repository string) beads.Client {
	return beads.Client{Runner: runner, Dir: repository, Timeout: chatTrackerTimeout}
}

// reportChatFailure reports a failed conversation, carrying whatever the turn
// still produced. A reply is nil when the conversation never opened.
func reportChatFailure(stdout, stderr io.Writer, jsonOutput bool, reply *chat.Reply, err error) int {
	output := chatOutput{Error: err.Error()}
	if reply != nil {
		evidence := reply.Evidence
		output.Evidence = &evidence
		output.Reply = reply.Text
		output.Proposals = reply.Proposals
		// A concern is recorded before the turn goes on to fail, and it is the one
		// thing in the reply that is waiting on a person, so it travels with the
		// failure rather than behind it.
		output.Concerns = reply.Concerns
		// A turn that failed may still have changed the tracker before it did, so
		// what it changed is reported with the failure rather than lost behind it.
		// The same is true of anything it reported: the report is already
		// collected, and it never had anything to do with the failure.
		output.Actions = reply.Actions
		output.ResultsCarriedOver = reply.ResultsCarriedOver
		output.Reports = reply.Reports
		output.ReportProblem = reply.ReportProblem
	}
	if jsonOutput {
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		return 1
	}
	if output.Reply != "" {
		fmt.Fprintln(stdout, output.Reply)
	}
	printChatActions(stdout, output.Actions, output.ResultsCarriedOver)
	printChatReports(stdout, output.Reports, output.ReportProblem)
	printChatConcerns(stdout, output.Concerns)
	printChatProposals(stdout, output.Proposals)
	fmt.Fprintf(stderr, "chat failed: %v\n", err)
	if output.Evidence != nil && output.Evidence.ConversationID != "" {
		fmt.Fprintf(stderr, "conversation: %s\n", output.Evidence.ConversationID)
	}
	return 1
}

func printChatHeader(writer io.Writer, evidence chat.Evidence, freshness string) {
	state := "new conversation"
	if evidence.Resumed {
		state = fmt.Sprintf("resumed conversation after %d turn(s)", evidence.Turns)
	}
	fmt.Fprintf(writer, "product manager: %s (%s, model %s)\n", evidence.ConversationID, state, evidence.RequestedModel)
	// How old its picture of the product is goes at the top, where an operator
	// cannot miss it, because everything below is what it will say about a
	// repository it may have read hours ago.
	fmt.Fprintln(writer, freshness)
	fmt.Fprintln(writer, "It owns the backlog: what is admitted to it, and the order work is pulled in.")
	fmt.Fprintln(writer, "It manages the work tracker itself: it can read, create, attribute to a goal,")
	fmt.Fprintln(writer, "update, reparent, reprioritize, link, unlink, close, and retire items, and every")
	fmt.Fprintln(writer, "change it makes is reported to you here. It has no files, commands, or network,")
	fmt.Fprintln(writer, "and it proposes changes to the brief and the goals rather than making them.")
	fmt.Fprintln(writer, "It may also propose work items; one is created only when you approve it by name,")
	fmt.Fprintln(writer, "and every one of them names a goal your goals state, checked rather than taken.")
	fmt.Fprintln(writer, "Work admitted before that check names none, and it says which items those are.")
	fmt.Fprintln(writer, "Several proposals are decided in one answer: approve 1,3 and decline 2 <reason>.")
	fmt.Fprintln(writer, "Work it cannot place under a goal, work it says would cut against one, and work")
	fmt.Fprintln(writer, "it judges to be against the product's intent are not proposed at all: it stops")
	fmt.Fprintln(writer, "and asks you instead.")
	fmt.Fprintln(writer, "Any agent can report something without it stopping their work; /reports")
	fmt.Fprintln(writer, "shows you what has been collected.")
	fmt.Fprintln(writer, "Its picture of the repository and the tracker is the one gathered above; /refresh")
	fmt.Fprintln(writer, "reads them again into this conversation without discarding what has been said.")
	fmt.Fprintln(writer, "You steer the work yourself: /backlog, /status, /work, /stop, /redirect. /show")
	fmt.Fprintln(writer, "reads one item in full and /diff says what a run changed, both without leaving")
	fmt.Fprintln(writer, "this conversation. /help lists them.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
}

// printChatActions reports what the product manager changed in the tracker while
// it answered. It is printed for a one-shot message as well as a conversation:
// the changes are already made, and a caller who is not told about them is
// reading a queue that moved without them.
func printChatActions(writer io.Writer, actions []chat.TrackerOutcome, resultsCarriedOver bool) {
	if len(actions) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe product manager acted on the tracker (%d action(s)):\n", len(actions))
	for _, action := range actions {
		fmt.Fprint(writer, action.Render())
	}
	// A reply that ran out of rounds stopped for a reason nobody can see in the
	// text, so it is said here rather than left to look like a finished thought.
	if resultsCarriedOver {
		fmt.Fprintln(writer, "It ran out of rounds of actions; what the last ones returned is recorded with")
		fmt.Fprintln(writer, "the conversation and reaches it the next time you say something to it.")
	}
}

// printChatReports names what the product manager reported for the operator
// while it answered. It is printed for a one-shot message as well as a
// conversation: the report is already collected, and one that is only in the
// pile is one nobody has been told about yet.
func printChatReports(writer io.Writer, reports []report.Report, problem string) {
	if len(reports) == 0 && problem == "" {
		return
	}
	if len(reports) > 0 {
		fmt.Fprintf(writer, "\nThe product manager reported %d thing(s) for you:\n", len(reports))
		for _, reported := range reports {
			fmt.Fprint(writer, reported.Render())
		}
	}
	if problem != "" {
		fmt.Fprintf(writer, "\na report was not collected: %s\n", problem)
	}
}

// printChatProposals reports what a one-shot message proposed. There is nobody
// to approve it here, so the proposals are printed with what they are: recorded,
// and not created.
func printChatProposals(writer io.Writer, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe product manager proposes %d work item(s). Nothing was created: approve them in `yoyodyne chat`.\n\n", len(proposals))
	for _, proposal := range proposals {
		fmt.Fprint(writer, proposal.Render())
	}
}

// printChatConcerns reports what a one-shot message would not propose. There is
// nobody to answer it here, so the questions are printed with what they are:
// raised, unanswered, and holding work that was never proposed.
func printChatConcerns(writer io.Writer, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe product manager will not propose %d thing(s) until it is answered. Nothing was proposed or created: answer it in `yoyodyne chat`.\n\n", len(concerns))
	for _, concern := range concerns {
		fmt.Fprint(writer, concern.Render())
	}
}

// printOpenConcerns names the questions a conversation ended without answering,
// so one nobody answered is a visible loose end rather than silence that reads
// as agreement.
func printOpenConcerns(writer io.Writer, theme console.Theme, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	var open strings.Builder
	fmt.Fprintf(&open, "%d question(s) from the product manager were left unanswered, and the work behind them was never proposed:\n", len(concerns))
	for _, concern := range concerns {
		// The question itself is printed rather than only named, because what an
		// operator has to come back to is what was asked and not that something
		// was.
		fmt.Fprintf(&open, "  [%s] %s: %s\n", concern.ID, concern.Concern.Kind.Headline(), concern.Concern.Subject)
		fmt.Fprintf(&open, "      %s\n", strings.TrimSpace(concern.Concern.Question))
	}
	fmt.Fprint(writer, theme.Questions(open.String()))
}

// printUndecidedProposals names what a conversation left open, so a proposal
// nobody decided on ends as a visible loose end rather than as silence. It is
// dressed as what it is — something still waiting on the operator — and the
// text says so without the colour.
func printUndecidedProposals(writer io.Writer, theme console.Theme, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	var undecided strings.Builder
	fmt.Fprintf(&undecided, "%d proposal(s) were left undecided and nothing was created for them:\n", len(proposals))
	for _, proposal := range proposals {
		fmt.Fprintf(&undecided, "  [%s] %s\n", proposal.ID, proposal.Proposal.Title)
	}
	fmt.Fprint(writer, theme.Proposal(undecided.String()))
}

func printChatEvidence(writer io.Writer, evidence chat.Evidence) {
	fmt.Fprintf(writer, "conversation: %s\n", evidence.ConversationID)
	fmt.Fprintf(writer, "model: %s\n", renderChatModel(evidence))
	if evidence.SessionID != "" {
		fmt.Fprintf(writer, "provider session: %s\n", evidence.SessionID)
	}
	fmt.Fprintf(writer, "turns: %d\n", evidence.Turns)
}

// renderChatModel reports the requested selector alongside what the provider
// resolved it to, because a floating alias only becomes evidence once the
// served model is named.
func renderChatModel(evidence chat.Evidence) string {
	if evidence.ResolvedModel == "" || evidence.ResolvedModel == evidence.RequestedModel {
		return evidence.RequestedModel
	}
	return evidence.RequestedModel + " (resolved: " + evidence.ResolvedModel + ")"
}

func printChatUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo chat [options]

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --message <text>   send one message and print the reply instead of conversing
  --new              start a new conversation instead of resuming the recorded one
  --json             emit machine-readable JSON (requires --message)

An interactive conversation also carries out operator commands: /backlog,
/status, /show, /diff, /refresh, /work, /wait, /stop, and /redirect. Ask it for
/help once it is open.`)
}
