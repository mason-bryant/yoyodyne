package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/research"
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
	// Proposals are what the turn proposed and nothing more. Nothing was created
	// for any of them: a single message has nobody standing at a prompt, so what
	// it proposes is reported and the decision arrives as its own message. What is
	// still waiting once this one is over is Pending, which is the wider list.
	Proposals []chat.PendingProposal `json:"proposals,omitempty"`
	// Admitted are the work items the turn put in the queue without asking, each
	// with what let it through. Unlike proposals these already exist, so they are
	// reported here for the same reason the actions are: a one-shot message has
	// nobody to tell afterwards, and this is the telling.
	Admitted []chat.AdmittedItem `json:"admitted,omitempty"`
	// Concerns are what the product manager would not propose until somebody
	// answers it. A one-shot message has nobody to answer, so they are reported
	// as the open questions they are rather than as work.
	Concerns []chat.PendingConcern `json:"concerns,omitempty"`
	// Actions are the tracker changes the product manager made while answering.
	// Unlike proposals they already happened, so they are reported rather than
	// offered.
	Actions []chat.TrackerOutcome `json:"actions,omitempty"`
	// Exchanges are the rounds of asking another role the reply conducted. They
	// are reported for the reason the actions are, and for one more: a
	// conversation that went and asked another agent something is exactly what an
	// operator must never have to discover afterwards.
	Exchanges []chat.ExchangeRound `json:"exchanges,omitempty"`
	// Research are the rounds of evidence-gathering the reply set off, and
	// Evaluation the recommendation it recorded. Both already happened, so they
	// are reported for the reason the actions are: research spends the operator's
	// money outside this machine, and an evaluation is durable. EvaluationProblem
	// names one that could not be kept, because a lost evaluation is reasoning
	// nobody can find afterwards.
	Research          []chat.ResearchRound   `json:"research,omitempty"`
	Evaluation        *evaluation.Evaluation `json:"evaluation,omitempty"`
	EvaluationProblem string                 `json:"evaluation_problem,omitempty"`
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
	// Decisions are what the message decided about proposals the conversation was
	// waiting on. Like a command they are the harness's own answer rather than
	// anything that was said: the decision is carried out here, no turn is spent,
	// and the agent hears about it when it is next spoken to.
	Decisions []chat.DecisionOutcome `json:"decisions,omitempty"`
	// Pending are the proposals still awaiting a decision once this message is
	// over, which is what a script deciding them next has to name. It is not the
	// same list as Proposals: that one is what this turn proposed, and this one is
	// everything nobody has decided, including proposals from earlier messages.
	Pending []chat.PendingProposal `json:"pending,omitempty"`
	Error   string                 `json:"error,omitempty"`
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
	session, hold, err := openChat(ctx, role, request.agentName, request.configPath, request.fresh, true, stderr)
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}
	defer hold.Release()

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
	// An answer to a proposal this conversation is still waiting on decides it
	// here rather than being said to the agent. Without this the operator's "y"
	// arrived as ordinary speech to a role that cannot create the item, so the
	// approval was spent, nothing reached the queue, and nothing said so.
	if outcomes, decided, err := session.Decide(ctx, message); decided {
		return reportChatDecisions(stdout, stderr, jsonOutput, role, session, outcomes, err)
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
			Evidence:  &evidence,
			Reply:     reply.Text,
			Proposals: reply.Proposals,
			// Everything still awaiting a decision, exactly as the text output
			// below lists it. A script reads this document where a person reads
			// that, so a turn that reported only what it had just proposed would
			// hide the earlier proposals from the reader least able to go
			// looking for them.
			Pending:            session.Proposals(),
			Admitted:           reply.Admitted,
			Concerns:           reply.Concerns,
			Actions:            reply.Actions,
			Exchanges:          reply.Exchanges,
			Research:           reply.Research,
			Evaluation:         reply.Evaluation,
			EvaluationProblem:  reply.EvaluationProblem,
			ResultsCarriedOver: reply.ResultsCarriedOver,
			Reports:            reply.Reports,
			ReportProblem:      reply.ReportProblem,
		})
	}
	fmt.Fprintln(stdout, reply.Text)
	// A one-shot message has no console to ask, so what it may be dressed with is
	// asked of the stream it is writing to. A redirected one is undressed, which
	// is the same answer an interactive conversation over the same stream gives.
	theme := console.ThemeFor(stdout, os.Getenv)
	printChatActions(stdout, role, reply.Actions, reply.ResultsCarriedOver)
	printChatResearch(stdout, reply.Research)
	printChatEvaluation(stdout, reply.Evaluation, reply.EvaluationProblem)
	printChatExchanges(stdout, role, reply.Exchanges)
	printChatAdmitted(stdout, reply.Admitted)
	printChatReports(stdout, theme, role, reply.Reports, reply.ReportProblem)
	printChatConcerns(stdout, theme, role, reply.Concerns)
	// Everything undecided is listed rather than only what this turn proposed: a
	// decision arrives as its own message, so what the operator has to be able to
	// name is the whole of what is still waiting on them.
	printChatProposals(stdout, role, session.Proposals())
	printChatEvidence(stdout, reply.Evidence)
	return 0
}

// reportChatDecisions reports what a message decided about the proposals a
// conversation was waiting on. What was decided is written whether or not the
// answer went on to fail, for the reason a failed command still writes what it
// did: a decision that was recorded happened, and the failure is about the rest
// of the answer.
func reportChatDecisions(stdout, stderr io.Writer, jsonOutput bool, role domain.AgentRole, session *chat.Session, outcomes []chat.DecisionOutcome, err error) int {
	pending := session.Proposals()
	if jsonOutput {
		evidence := session.Evidence()
		output := chatOutput{Evidence: &evidence, Decisions: outcomes, Pending: pending}
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
	printChatDecisions(stdout, outcomes)
	printChatProposals(stdout, role, pending)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// printChatDecisions says what the operator's message did to each proposal it
// named. Nothing here was said by the agent, so it is written plainly rather
// than as part of a reply: an approval that created an item and one the tracker
// refused are the two things the operator has to be able to tell apart.
func printChatDecisions(writer io.Writer, decisions []chat.DecisionOutcome) {
	if len(decisions) == 0 {
		return
	}
	fmt.Fprintf(writer, "You decided %d proposal(s):\n\n", len(decisions))
	for _, made := range decisions {
		fmt.Fprint(writer, made.Render())
	}
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
// previous process left behind. The returned hold is this process's claim on
// that conversation: taken here, put down whenever nobody is talking to the
// agent, and released for good by the caller.
//
// Everything the operator's own commands need is wired for every role, because
// those commands are the operator's authority rather than the agent's and they
// mean the same thing in every conversation. What differs between roles is what
// the role itself may ask for, and that is the contract and the authority table
// in the chat package rather than anything decided here.
//
// waitForTurn is what to do about a turn already in flight, and it is the one
// thing the two callers genuinely disagree about. The operator waits: they have
// already typed the command, a turn ends on its own, and being turned away by
// one was the seam that made the product manager unreachable. A background
// delivery does not: nothing was asked of the agent yet, the attempt is given
// back and a later pass makes it, and a dispatcher holding its lease and its
// budget open for the length of somebody else's turn is a worse answer than
// coming back.
func openChat(ctx context.Context, role domain.AgentRole, agentName, configPath string, fresh, waitForTurn bool, stderr io.Writer) (*chat.Session, *runstate.ConversationHold, error) {
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
	// A conversation runs on whatever adapter this build has, so what is asked is
	// whether the backend the agent named resolves to one — which a provider the
	// project declared does, and a backend nothing can launch does not.
	if !providerRuns(cfg, agent.Backend) {
		return nil, nil, fmt.Errorf("a conversation requires a claude-code agent, and the %s agent %s is configured for %q", role, name, agent.Backend)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		return nil, nil, fmt.Errorf("%s agent %s %s", role, name, err)
	}

	processRunner := parts.runner
	// The conversation is held under the account its agent is configured for, and
	// the availability below is asked of that same account: a check made against
	// the machine's own login would report a conversation ready to open on an
	// account nobody had signed in to.
	account, err := conversationAccount(cfg, parts.stateRoot, name)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve the account %s agent %s runs under: %w", role, name, err)
	}
	// The provider is built from what the project declares — its adapter, its
	// executable, and its dialect — and then pointed at the account's own
	// provider home. The two are separate decisions: which provider runs the
	// agent is the project's, and which login it runs under is this machine's.
	provider := providerBackend(cfg, agent.Backend, processRunner)
	provider.ConfigDir = account.Directory
	availability, err := provider.CheckAvailability(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !availability.Installed {
		return nil, nil, errors.New("Claude Code is not installed")
	}
	if !availability.Authenticated {
		return nil, nil, fmt.Errorf("Claude Code is not authenticated for account %q; run `%s` before starting a conversation (auth method: %s)",
			account.Alias, accountLoginCommand(account), availability.AuthMethod)
	}

	store, err := runstate.NewConversationStore(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return nil, nil, err
	}
	// The conversation is held, recorded, and resumed under the agent that holds
	// it, so two agents configured for one role are two conversations rather than
	// one they would take turns overwriting.
	//
	// Claiming queues behind whoever is mid-turn rather than refusing, which is
	// the whole of what makes the product manager reachable: `yoyo chat
	// --message` from another terminal, and a second window the operator opens
	// beside the first, both wait out a turn instead of being turned away by one.
	// Ctrl-C ends the wait, and the wait is said out loud once it lasts long
	// enough to notice, so an operator whose command has not come back knows what
	// it is behind. The refusing claim comes back at once and so says nothing.
	identity := runstate.ConversationIdentity{Agent: name, Role: role}
	var hold *runstate.ConversationHold
	if err := chat.AwaitConversation(role, stderr, func() error {
		var err error
		if waitForTurn {
			hold, err = store.Claim(ctx, identity)
		} else {
			hold, err = store.TryClaim(identity)
		}
		return err
	}); err != nil {
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
		return nil, nil, errors.Join(err, hold.Release())
	}
	for _, problem := range briefing.Problems {
		fmt.Fprintf(stderr, "warning: %s\n", problem)
	}
	// Provider below is the backend the agent named rather than the adapter that
	// launches it: a conversation record has to say which provider answered it,
	// and a project that declared its own has a different answer from the
	// built-in. Providers is the set that name is checked against, so a
	// conversation on a backend nothing can run is refused before the provider is
	// invoked rather than on its first turn.
	session, err := chat.Open(chat.Options{
		Role:    role,
		Backend: provider,
		Store:   store,
		// This process's claim on the conversation, handed over so an interactive
		// one can put it down while the operator is at the prompt. What has to be
		// exclusive is a turn: an idle console that never let go was the reason
		// nothing else could reach the product manager until the operator closed
		// their window.
		Hold: hold,
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
		// Where a provider refusing this conversation for want of capacity is
		// written down. It is the same log every other process outside a run
		// records one in, so an exhausted limit reaches the channel from wherever
		// it was met rather than only from a run.
		UsageLimits: parts.usageLimits,
		// The operator's switch over the work the harness chooses for itself, so
		// holding intake is something they can do from the conversation they are
		// already in rather than from a second tool.
		Intake: parts.intake,
		// The inter-role ask channel, wired for the roles that are on it. A
		// question one role cannot answer itself reaches the role that can through
		// here, rather than through the operator or through a work item.
		Exchanges:           conversationExchanges(parts, role, provider),
		AskRoundsPerMessage: cfg.Exchange.MaxRounds,
		// The durable budget the development manager's triage decisions spend.
		// It is wired for that role alone, like the docket those decisions are
		// about, so a repair grant or a re-run is bounded by what the operator
		// configured wherever it is decided from.
		Triage: conversationTriage(parts, role),
		// The run records those decisions are checked against, so a decision lands
		// on the item whose work actually stopped: the run a docket entry names is
		// the run the decision has to be about. They are also what says where a
		// change is, so work carved out of a stoppage waits for the change it was
		// written against rather than reading as ready without it.
		Stoppages: conversationStoppages(parts, role),
		// The changes other roles have proposed to the documents this one owns.
		// They are read here so the owner hears the argument; deciding them is the
		// operator's, through `yoyo amendment`.
		Amendments: parts.amendments,
		// How evidence from outside the repository is gathered on the role's
		// behalf, bounded by what the operator configured. It is the harness's own
		// hand like the tracker is: the role names a question and a permitted
		// source, and nothing about what runs or where it reaches is the role's.
		Research: conversationResearch(parts),
		// Where a recorded recommendation about an operator's idea is kept. It is
		// wired for every role because the authority to record one is decided in
		// the chat package's table rather than here, and a store nobody may write
		// to is never written to.
		Evaluations: parts.evaluations,
		// What work admitted here has to name. It is read from the repository
		// rather than from the conversation, so a goal retired since the
		// conversation opened stops being one work can be admitted under.
		Goals: goals,
		// What this project asks the operator about before work reaches the queue.
		// It is read from the configuration rather than decided here, so the same
		// answer governs a proposal and a direct admission.
		Admission: chat.Admission{
			WorkItems: cfg.Approvals.WorkItems,
			// And the classes of work this project has said it does not want to be
			// asked about, which is the operator narrowing their own gate rather than
			// the harness deciding anything.
			Exempt: cfg.Approvals.WorkItemExemptions,
		},
		// What every turn of this conversation spends, recorded where a run's spend
		// is recorded. A conversation costs money and the record has to say so:
		// what an operator asked to see is what the harness spends on their behalf,
		// and the management conversation is part of that rather than beside it.
		Spend: parts.spend,
		// The alias every turn's spend is charged to is the one this conversation
		// was actually opened under, which under a pool is the agent's own rather
		// than a configuration-wide single account there is none of.
		AccountAlias:   account.Alias,
		ConfigRevision: cfg.Revision(),
		// And which harness is holding it, read once here because a process does not
		// change binary while it lives — which is the whole reason a conversation
		// somebody leaves open for days is worth stamping.
		Build:        buildinfo.Commit(),
		Model:        agent.Model,
		Persona:      agent.Persona.Text,
		Agent:        name,
		Provider:     agent.Backend,
		Providers:    providerRegistry(cfg),
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
		return nil, nil, errors.Join(err, hold.Release())
	}
	return session, hold, nil
}

// conversationAccount picks the provider account one agent's conversation is
// held under, and is the one place that decision is made. Three things are told
// the answer and all three have to be told the same one: the provider, which
// authenticates in that account's own home; the conversation's durable record,
// which says whose subscription is serving it; and every turn's cost line, which
// is what the money is attributed by.
//
// It is the agent's own account rather than a configuration-wide one, because
// under a pool there is no configuration-wide account to name — the answering
// agent's is the only alias that is true of the conversation. It is the same
// resolution the answering half of an exchange makes for the same reason.
func conversationAccount(cfg config.Config, stateRoot, agentName string) (config.AccountEndpoint, error) {
	return cfg.Endpoint(stateRoot, cfg.AgentAccountAlias(agentName))
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

// conversationResearch is the evidence-gathering capability a conversation
// performs on the role's behalf, built from what this project permitted. It is
// always built, including for a project that permitted nothing: the runner then
// reports that there are no sources, which is what the role has to be told so it
// says it could not check rather than answering from memory.
//
// The source commands run in the repository, so a source an operator wrote as a
// script in their own project works the way they expect, and the redact values
// are the same ones every other provider-facing path uses — a source is the one
// thing here that reaches outside the machine, so what must not leave it is
// removed from the question before it does.
func conversationResearch(parts components) research.Runner {
	return research.Runner{
		Process:      parts.runner,
		Directory:    parts.repository,
		Policy:       parts.config.Research.Policy(),
		RedactValues: parts.redactValues,
	}
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
		// Work admitted before the turn failed is in the queue, so it travels with
		// the failure for the reason the tracker actions do: it already happened,
		// and nobody was asked about it.
		output.Admitted = reply.Admitted
		// A concern is recorded before the turn goes on to fail, and it is the one
		// thing in the reply that is waiting on a person, so it travels with the
		// failure rather than behind it.
		output.Concerns = reply.Concerns
		// A turn that failed may still have changed the tracker before it did, so
		// what it changed is reported with the failure rather than lost behind it.
		// The same is true of anything it reported: the report is already
		// collected, and it never had anything to do with the failure.
		output.Actions = reply.Actions
		// Research already happened and an evaluation was already recorded, so both
		// travel with the failure for the same reason the actions do.
		output.Research = reply.Research
		output.Evaluation = reply.Evaluation
		output.EvaluationProblem = reply.EvaluationProblem
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
	theme := console.ThemeFor(stdout, os.Getenv)
	printChatActions(stdout, role, output.Actions, output.ResultsCarriedOver)
	printChatResearch(stdout, output.Research)
	printChatEvaluation(stdout, output.Evaluation, output.EvaluationProblem)
	printChatExchanges(stdout, role, output.Exchanges)
	printChatAdmitted(stdout, output.Admitted)
	printChatReports(stdout, theme, role, output.Reports, output.ReportProblem)
	printChatConcerns(stdout, theme, role, output.Concerns)
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

// printChatExchanges reports what one role asked another while it answered. It
// is printed wherever the actions are, and for the same reason with one added:
// the exchange already happened and was already paid for, and a question put to
// another agent that the operator is not told about is the side conversation
// this channel exists not to be. The whole thread is durable; this is the line
// that says to go and read it.
func printChatExchanges(writer io.Writer, role domain.AgentRole, exchanges []chat.ExchangeRound) {
	if len(exchanges) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s asked another role (%d round(s)):\n", chat.RoleTitle(role), len(exchanges))
	for _, round := range exchanges {
		fmt.Fprintf(writer, "  asked the %s: %s\n", chat.RoleTitle(round.Asked), round.Question)
		if round.ID != "" {
			fmt.Fprintf(writer, "    %s, %s, round %d of %d, $%.4f\n", round.ID, round.State, round.Round, round.Rounds, round.CostUSD)
		}
		if round.Settled != "" {
			fmt.Fprintf(writer, "    settled: %s\n", round.Settled)
		}
		if round.Problem != "" {
			fmt.Fprintf(writer, "    unanswered: %s\n", round.Problem)
		}
	}
	fmt.Fprintln(writer, "  `yoyo exchange show <id>` is the whole of what was said.")
}

// printChatResearch names what the harness went and looked up while the reply
// was being written. It lists the questions and what each one returned in size
// and provenance rather than reprinting the evidence: the answer is in the reply
// above, and a page of retrieved text under it would bury the answer in its own
// sources.
func printChatResearch(writer io.Writer, rounds []chat.ResearchRound) {
	if len(rounds) == 0 {
		return
	}
	fmt.Fprintln(writer)
	for _, round := range rounds {
		fmt.Fprint(writer, round.Render())
	}
}

// printChatEvaluation names the recommendation that went into the record, and
// says in the same breath that it changed nothing. The second half is the part
// that matters: an evaluation is the one durable thing a conversation writes that
// decides nothing, and "recorded" read as "settled" is a decision the operator
// never made.
func printChatEvaluation(writer io.Writer, recorded *evaluation.Evaluation, problem string) {
	if recorded != nil {
		fmt.Fprintf(writer, "\nEvaluation recorded: %s — %s\n", recorded.Entry.Recommendation, recorded.Entry.Recommendation.Headline())
		fmt.Fprintf(writer, "  [%s] %s\n", recorded.ID, recorded.Entry.Idea)
		fmt.Fprintln(writer, "  advice only: nothing was admitted, approved, or changed by recording it")
		fmt.Fprintf(writer, "  `yoyo evaluation show %s` has the reasoning and the sources\n", recorded.ID)
	}
	if problem != "" {
		fmt.Fprintf(writer, "\nAn evaluation could not be kept: %s\n", problem)
	}
}

// printChatReports names what the product manager reported for the operator
// while it answered. It is printed for a one-shot message as well as a
// conversation: the report is already collected, and one that is only in the
// pile is one nobody has been told about yet.
func printChatReports(writer io.Writer, theme console.Theme, role domain.AgentRole, reports []report.Report, problem string) {
	if len(reports) == 0 && problem == "" {
		return
	}
	if len(reports) > 0 {
		fmt.Fprintf(writer, "\nThe %s reported %d thing(s) for you:\n", chat.RoleTitle(role), len(reports))
		for _, reported := range reports {
			fmt.Fprint(writer, theme.Severity(console.Severity(reported.Severity), reported.Render()))
		}
	}
	if problem != "" {
		fmt.Fprintf(writer, "\na report was not collected: %s\n", problem)
	}
}

// printChatAdmitted reports what a one-shot message put in the queue without
// asking anybody. Unlike a proposal it is not something to decide, and that is
// exactly why it is printed: this is the one moment the operator is told, and a
// message that admitted work and said nothing about it would leave the queue
// having moved with no account of it anywhere they look.
func printChatAdmitted(writer io.Writer, admitted []chat.AdmittedItem) {
	if len(admitted) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d work item(s) were admitted to the queue without asking you, and each one says why:\n\n", len(admitted))
	for _, item := range admitted {
		fmt.Fprint(writer, item.Render())
	}
}

// printChatProposals reports what is awaiting the operator's decision, and says
// how to make one. Nothing here was created, and the proposals are named by
// their own identifiers rather than numbered: a number is a position in a
// listing, and the listing an operator sees next is whatever their next command
// prints, while the identifier is the same word in every invocation. A decision
// sent as its own message has to name something that survives between the two.
func printChatProposals(writer io.Writer, role domain.AgentRole, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d proposal(s) from the %s are awaiting your decision, and nothing was created for them:\n\n", len(proposals), chat.RoleTitle(role))
	for _, proposal := range proposals {
		fmt.Fprint(writer, proposal.Render())
	}
	first := proposals[0].ID
	fmt.Fprintf(writer, "\nDecide one from here: `yoyo chat --message \"approve %s\"` creates it, and\n`yoyo chat --message \"decline %s <reason>\"` turns it down. `yoyo chat` puts them\nto you as a prompt instead.\n", first, first)
}

// printChatConcerns reports what a one-shot message would not propose. There is
// nobody to answer it here, so the questions are printed with what they are:
// raised, unanswered, and holding work that was never proposed.
func printChatConcerns(writer io.Writer, theme console.Theme, role domain.AgentRole, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s will not propose %d thing(s) until it is answered. Nothing was proposed or created: answer it in `yoyodyne chat`.\n\n", chat.RoleTitle(role), len(concerns))
	for _, concern := range concerns {
		fmt.Fprint(writer, concern.Render(theme))
	}
}

// printOpenConcerns names the questions a conversation ended without answering,
// so one nobody answered is a visible loose end rather than silence that reads
// as agreement.
// Each one is marked and dressed by what its kind asks for rather than the
// whole list being dressed as questions, because this is the listing where a
// conversation's loose ends are counted together: they are all unanswered, and
// which of them says the work would cut against a goal is the thing a count
// cannot say.
func printOpenConcerns(writer io.Writer, theme console.Theme, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "%d question(s) from the product manager were left unanswered, and the work behind them was never proposed:\n", len(concerns))
	for _, concern := range concerns {
		severity := concern.Concern.Kind.Severity()
		var one strings.Builder
		// The question itself is printed rather than only named, because what an
		// operator has to come back to is what was asked and not that something
		// was.
		fmt.Fprintf(&one, "  %-*s [%s] %s: %s\n", report.MarkerWidth, severity.Marker(),
			concern.ID, concern.Concern.Kind.Headline(), concern.Concern.Subject)
		fmt.Fprintf(&one, "      %s\n", strings.TrimSpace(concern.Concern.Question))
		fmt.Fprint(writer, theme.Severity(console.Severity(severity), theme.Questions(one.String())))
	}
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

A --message that decides a proposal this conversation is waiting on is carried
out here too, and no turn is spent on it. Two shapes decide: one that names the
proposal, as "approve 3.1" or "decline 3.1 <reason>", and one that is nothing but
decision words, as "y", "no", or "approve 1,3". Everything else is said to the
product manager as it always was and leaves every proposal where it was —
including a reply that opens with one of those words, because "no, let us look at
the resolver instead" is a sentence rather than a decline.

This is the product manager's conversation. Every other configured agent is
reached the same way through "yoyo agent chat <name>", which takes the same
options; "yoyo agent list" says who there is and what each one is in the middle
of.`)
}
