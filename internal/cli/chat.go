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
	// Harness is what an operator command printed when the message turned out to
	// be one. It is a separate field from the reply because nothing said it: the
	// harness answered, the product manager was never asked, and no turn was
	// spent.
	Harness string `json:"harness,omitempty"`
	Error   string `json:"error,omitempty"`
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

	return converse(ctx, domain.RoleProductManager, conversationRequest{
		configPath: *configPath,
		message:    *message,
		fresh:      *fresh,
		jsonOutput: *jsonOutput,
	}, stdin, stdout, stderr)
}

// conversationRequest is what an operator asked of one conversation, whichever
// command they reached it through. `yoyo chat` and `yoyo agent chat` differ in
// which role they address and in nothing else, so they share this rather than
// growing two conversations that drift apart.
type conversationRequest struct {
	// agentName is the configured agent the operator named, and is empty when
	// they named none. It is carried rather than resolved back from the role
	// because a role two agents fill has no single answer: an operator who named
	// one of them must reach that one, with its persona and its model, and a
	// command that resolved the role again would silently reach whichever sorted
	// first.
	agentName  string
	configPath string
	message    string
	fresh      bool
	jsonOutput bool
}

// converse holds one conversation with one role: a single message and its reply,
// or the interactive conversation the operator stays inside.
func converse(ctx context.Context, role domain.AgentRole, request conversationRequest, stdin io.Reader, stdout, stderr io.Writer) int {
	session, lease, err := openChat(ctx, role, request.agentName, request.configPath, request.fresh, stderr)
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}
	defer lease.Release()

	if request.message != "" {
		return runChatMessage(ctx, session, role, request.message, request.jsonOutput, stdout, stderr)
	}

	// The conversation is held over a console rather than the raw streams: on a
	// terminal that gives the operator's typing a region output never writes
	// into, and anywhere else it is the same conversation as an ordinary stream
	// of text. Either way what is recorded is identical.
	screen := console.Open(console.Options{In: stdin, Out: stdout})
	defer screen.Close()
	printChatHeader(screen, role, session.Evidence(), session.Freshness(ctx))
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

// runChatMessage answers one thing the operator said from a command line. It is
// either a command the harness carries out or a message the role answers, and
// which of the two it is is the conversation's own rule rather than a second one
// written here: a slash means the same thing in a single message as it does at
// the prompt.
func runChatMessage(ctx context.Context, session *chat.Session, role domain.AgentRole, message string, jsonOutput bool, stdout, stderr io.Writer) int {
	// Without this the command would be said to the agent, which has no way to
	// carry one out and every reason to be confused by one — and the operator
	// would pay for the turn.
	if chat.IsCommand(message) {
		return runChatCommand(ctx, session, message, jsonOutput, stdout, stderr)
	}
	// A one-shot message resumes the same recorded conversation an interactive
	// one does, and carries the same risk of answering from a picture taken hours
	// ago. It is said on stderr because stdout is the reply and, with --json, a
	// document.
	fmt.Fprintln(stderr, session.Freshness(ctx))
	reply, err := session.Send(ctx, message)
	if err != nil {
		// The answer travels with the failure. A turn that produced one is worth
		// reading even when what it proposed could not be read.
		return reportChatFailure(stdout, stderr, jsonOutput, role, &reply, err)
	}
	if jsonOutput {
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
	printChatActions(stdout, role, reply.Actions, reply.ResultsCarriedOver)
	printChatReports(stdout, role, reply.Reports, reply.ReportProblem)
	printChatConcerns(stdout, role, reply.Concerns)
	printChatProposals(stdout, role, reply.Proposals)
	printChatEvidence(stdout, reply.Evidence)
	return 0
}

// runChatCommand carries out an operator command that arrived as a single
// message. The harness answers it rather than the product manager, so there is
// no turn, no reply, and nothing spent — which is also why the conversation's
// freshness is not printed here: how old its picture of the product is says
// nothing about an answer it had no part in.
func runChatCommand(ctx context.Context, session *chat.Session, line string, jsonOutput bool, stdout, stderr io.Writer) int {
	var rendered strings.Builder
	err := session.Command(ctx, line, &rendered)
	return reportChatCommand(stdout, stderr, jsonOutput, session.Evidence(), rendered.String(), err)
}

// reportChatCommand shows what a command did. What it printed is written
// whether or not it went on to fail, for the same reason a failed turn still
// carries its reply: a command that recorded something and then could not
// report it recorded it all the same. The failure itself is the command's,
// which is what the exit code says.
func reportChatCommand(stdout, stderr io.Writer, jsonOutput bool, evidence chat.Evidence, rendered string, err error) int {
	if jsonOutput {
		output := chatOutput{Evidence: &evidence, Harness: rendered}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprint(stdout, rendered)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// openChat builds a role's conversation from configuration: the configured
// agent filling that role, the repository's own Markdown, the tracker state as
// it stands, the harness the operator steers work with, and the durable record a
// previous process left behind. The returned lease is this process's exclusive
// hold on that conversation.
//
// Everything the operator's own commands need is wired for every role, because
// those commands are the operator's authority rather than the agent's and they
// mean the same thing in every conversation. What differs between roles is what
// the role itself may ask for, and that is the contract and the authority table
// in the chat package rather than anything decided here.
func openChat(ctx context.Context, role domain.AgentRole, agentName, configPath string, fresh bool, stderr io.Writer) (*chat.Session, *runstate.Lease, error) {
	// The conversation is built over the same components a run is, because
	// steering work from inside it means executing exactly the runs
	// `yoyodyne run` would have executed.
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, nil, err
	}
	cfg := parts.config
	repository := parts.repository

	name, agent, err := conversationAgent(cfg, role, agentName)
	if err != nil {
		return nil, nil, err
	}
	if agent.Backend != domain.BackendClaudeCode {
		return nil, nil, fmt.Errorf("a conversation requires a claude-code agent, and the %s agent %s is configured for %q", role, name, agent.Backend)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		return nil, nil, fmt.Errorf("%s agent %s %s", role, name, err)
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
	// The conversation is held, recorded, and resumed under the agent that holds
	// it, so two agents configured for one role are two conversations rather than
	// one they would take turns overwriting.
	lease, err := store.Hold(runstate.ConversationIdentity{Agent: name, Role: role})
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

	ground := newConversationGround(parts, role)
	briefing, err := ground.Gather(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, lease.Release())
	}
	for _, problem := range briefing.Problems {
		fmt.Fprintf(stderr, "warning: %s\n", problem)
	}
	session, err := chat.Open(chat.Options{
		Role:    role,
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
		// The same directives every run reads before it commits to work. One
		// recorded from this conversation is not this conversation's: it belongs
		// to the product, and it reaches runs in other processes exactly as it
		// reaches this one.
		Directives: conversationDirectives{store: parts.directives, productID: cfg.Product.ID},
		// The same hold every run reads before it spends. A turn is a provider
		// invocation, so `yoyo pause` covers this conversation exactly as it covers
		// the work steered from it.
		Holds: parts.holds,
		// The operator's switch over the work the harness chooses for itself, so
		// holding intake is something they can do from the conversation they are
		// already in rather than from a second tool.
		Intake: parts.intake,
		// The durable budget the development manager's triage decisions spend.
		// It is wired for that role alone, like the docket those decisions are
		// about, so a repair grant or a re-run is bounded by what the operator
		// configured wherever it is decided from.
		Triage: conversationTriage(parts, role),
		// The changes other roles have proposed to the documents this one owns.
		// They are read here so the owner hears the argument; deciding them is the
		// operator's, through `yoyo amendment`.
		Amendments: parts.amendments,
		// What work admitted here has to name. It is read from the repository
		// rather than from the conversation, so a goal retired since the
		// conversation opened stops being one work can be admitted under.
		Goals:        goals,
		Model:        agent.Model,
		Persona:      agent.Persona.Text,
		Agent:        name,
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

// conversationAgent picks the agent a conversation is actually held with, and
// is the one place that decision is made. A named agent is used as named, so an
// operator who picked one of two architects gets that one's persona and model
// rather than whichever the role happens to resolve to; a conversation that
// names none takes the agent filling the role, which is what `yoyo chat` has
// always done. An agent named for one role and configured for another is
// refused rather than quietly answered by the wrong contract, because the role
// decides the authority and the name decides the persona and they must agree.
func conversationAgent(cfg config.Config, role domain.AgentRole, name string) (string, config.AgentConfig, error) {
	if strings.TrimSpace(name) == "" {
		resolved := agentNameForRole(cfg, role)
		if resolved == "" {
			return "", config.AgentConfig{}, fmt.Errorf("no %s agent is configured; there is nobody to talk to", role)
		}
		return resolved, cfg.Agents[resolved], nil
	}
	agent, configured := cfg.Agents[name]
	if !configured {
		return "", config.AgentConfig{}, fmt.Errorf("no agent named %q is configured; `yoyo agent list` names them", name)
	}
	if agent.Role != role {
		return "", config.AgentConfig{}, fmt.Errorf("agent %s fills the %s role, not %s", name, agent.Role, role)
	}
	return name, agent, nil
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
func reportChatFailure(stdout, stderr io.Writer, jsonOutput bool, role domain.AgentRole, reply *chat.Reply, err error) int {
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
	printChatActions(stdout, role, output.Actions, output.ResultsCarriedOver)
	printChatReports(stdout, role, output.Reports, output.ReportProblem)
	printChatConcerns(stdout, role, output.Concerns)
	printChatProposals(stdout, role, output.Proposals)
	fmt.Fprintf(stderr, "chat failed: %v\n", err)
	if output.Evidence != nil && output.Evidence.ConversationID != "" {
		fmt.Fprintf(stderr, "conversation: %s\n", output.Evidence.ConversationID)
	}
	return 1
}

func printChatHeader(writer io.Writer, role domain.AgentRole, evidence chat.Evidence, freshness string) {
	state := "new conversation"
	if evidence.Resumed {
		state = fmt.Sprintf("resumed conversation after %d turn(s)", evidence.Turns)
	}
	fmt.Fprintf(writer, "%s: %s (%s, model %s)\n", chat.RoleTitle(role), evidence.ConversationID, state, evidence.RequestedModel)
	// How old its picture of the product is goes at the top, where an operator
	// cannot miss it, because everything below is what it will say about a
	// repository it may have read hours ago.
	fmt.Fprintln(writer, freshness)
	if role != domain.RoleProductManager {
		printOtherRoleHeader(writer, role)
		return
	}
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
	fmt.Fprintln(writer, "shows you what has been collected, and `yoyo reports` shows the same pile")
	fmt.Fprintln(writer, "without a conversation.")
	fmt.Fprintln(writer, "Changes other roles have proposed to the brief and the goals are carried into")
	fmt.Fprintln(writer, "this conversation for it to argue; you decide them with `yoyo amendment`.")
	fmt.Fprintln(writer, "Its picture of the repository and the tracker is the one gathered above; /refresh")
	fmt.Fprintln(writer, "reads them again into this conversation without discarding what has been said.")
	fmt.Fprintln(writer, "You steer the work yourself: /backlog, /status, /work, /stop, /redirect. /show")
	fmt.Fprintln(writer, "reads one item in full and /diff says what a run changed, both without leaving")
	fmt.Fprintln(writer, "this conversation. /help lists them.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
}

// printOtherRoleHeader opens a conversation with a role that is not the product
// manager. It says three things an operator needs before they spend a turn: what
// this role decides, what it may do from here, and where the thing they probably
// want next actually happens — because a conversation that lets somebody talk to
// the architect for ten minutes before they discover it cannot write the design
// has wasted their afternoon.
func printOtherRoleHeader(writer io.Writer, role domain.AgentRole) {
	authority, known := chat.AuthorityFor(role)
	if !known {
		fmt.Fprintf(writer, "The harness holds no conversation contract for %s.\n\n", role)
		return
	}
	fmt.Fprintf(writer, "It owns %s.\n", authority.Owns)
	fmt.Fprintln(writer, "It has no files, commands, or network, and it can read the work tracker but")
	switch {
	case len(authority.TrackerActions) > 2:
		fmt.Fprintln(writer, "may only build structure underneath work the product manager has admitted:")
		fmt.Fprintln(writer, "it decomposes, links, and reparents, and it cannot admit work, reorder the")
		fmt.Fprintln(writer, "backlog, close an item, or retire one. Every change it makes is reported here.")
	default:
		fmt.Fprintln(writer, "changes nothing in it: it reads items and surveys the queue, and that is all.")
	}
	switch role {
	case domain.RoleArchitect:
		fmt.Fprintln(writer, "It cannot edit the documents it owns from here. Decide the change with it, then")
		fmt.Fprintln(writer, "record it yourself: `yoyo invariant` for an invariant, and a revision to the")
		fmt.Fprintln(writer, "design for the rest. Changes other roles proposed to its documents are carried")
		fmt.Fprintln(writer, "into this conversation for it to argue; you decide them with `yoyo amendment`.")
	case domain.RoleDeveloper, domain.RoleReviewer:
		fmt.Fprintln(writer, "Its real work happens inside runs, with a worktree, checks, and a verdict, and")
		fmt.Fprintln(writer, "none of that is happening here. What you get here is its judgement.")
	}
	fmt.Fprintln(writer, "Anything it reports reaches `yoyo reports`, and `/reports` shows the same pile.")
	fmt.Fprintln(writer, "Its picture of the repository is the one gathered above; /refresh takes a new one.")
	fmt.Fprintln(writer, "The operator commands are the same ones `yoyo chat` has: /help lists them.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
}

// printChatActions reports what the role changed in the tracker while it
// answered. It is printed for a one-shot message as well as a conversation: the
// changes are already made, and a caller who is not told about them is reading a
// queue that moved without them.
func printChatActions(writer io.Writer, role domain.AgentRole, actions []chat.TrackerOutcome, resultsCarriedOver bool) {
	if len(actions) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s acted on the tracker (%d action(s)):\n", chat.RoleTitle(role), len(actions))
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
func printChatReports(writer io.Writer, role domain.AgentRole, reports []report.Report, problem string) {
	if len(reports) == 0 && problem == "" {
		return
	}
	if len(reports) > 0 {
		fmt.Fprintf(writer, "\nThe %s reported %d thing(s) for you:\n", chat.RoleTitle(role), len(reports))
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
func printChatProposals(writer io.Writer, role domain.AgentRole, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s proposes %d work item(s). Nothing was created: approve them in `yoyodyne chat`.\n\n", chat.RoleTitle(role), len(proposals))
	for _, proposal := range proposals {
		fmt.Fprint(writer, proposal.Render())
	}
}

// printChatConcerns reports what a one-shot message would not propose. There is
// nobody to answer it here, so the questions are printed with what they are:
// raised, unanswered, and holding work that was never proposed.
func printChatConcerns(writer io.Writer, role domain.AgentRole, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s will not propose %d thing(s) until it is answered. Nothing was proposed or created: answer it in `yoyodyne chat`.\n\n", chat.RoleTitle(role), len(concerns))
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

A conversation also carries out operator commands: /backlog, /status, /show,
/diff, /reports, /refresh, /work, /wait, /stop, and /redirect. Ask it for /help
once it is open.

A --message that begins with a slash is carried out as one of those commands
rather than said to the product manager. The commands that only mean something
inside a conversation — /work, /wait, /stop, and /exit (alias /quit) — are refused there and
say what to reach for instead.

This is the product manager's conversation. Every other configured agent is
reached the same way through "yoyo agent chat <name>", which takes the same
options; "yoyo agent list" says who there is and what each one is in the middle
of.`)
}
