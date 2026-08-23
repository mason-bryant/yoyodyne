package cli

// The operator's view of the agents themselves: who is configured, what each
// one is currently in the middle of, and how to address one directly.
//
// An agent here is a durable logical identity rather than a process. The
// provider process that answers a turn is ephemeral and is gone by the time
// this command runs; what survives it is the configuration that says who the
// agent is and the conversation record that says what it has been told. So this
// reads both and says what it found, rather than looking for anything running.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// agentReport is one configured agent as the operator reads it: what it is,
// what it decides, and what durable state it has.
type agentReport struct {
	Name    string           `json:"name"`
	Role    domain.AgentRole `json:"role"`
	Backend domain.Backend   `json:"backend"`
	Model   string           `json:"model"`
	// Account is the provider account this agent runs under. Which role runs
	// where is the operator's and it is fixed, so it is read here beside the
	// model rather than reconstructed from the configuration by hand.
	Account        string `json:"account,omitempty"`
	Instances      int    `json:"instances"`
	PersonaPath    string `json:"persona_path,omitempty"`
	PersonaVersion string `json:"persona_version,omitempty"`
	// Owns is what this role decides, from the authority table rather than from
	// the persona: a project can rewrite the persona and cannot rewrite this.
	Owns string `json:"owns,omitempty"`
	// Addressable reports whether the harness holds conversations with this
	// role at all. A role with no contract has no conversation, and saying so is
	// better than a command that fails when it is tried.
	Addressable bool `json:"addressable"`
	// Conversation is the durable record, absent when this agent has never been
	// spoken to.
	Conversation *conversationReport `json:"conversation,omitempty"`
	// InUse reports that another process is holding this agent's conversation
	// right now, which is why a second one cannot be opened.
	InUse bool `json:"in_use,omitempty"`
	// Problem is why the durable state could not be read, when it could not.
	Problem string `json:"problem,omitempty"`
}

type conversationReport struct {
	ID                string    `json:"id"`
	Turns             int       `json:"turns"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ProviderSessionID string    `json:"provider_session_id,omitempty"`
	RequestedModel    string    `json:"requested_model,omitempty"`
	ResolvedModel     string    `json:"resolved_model,omitempty"`
	ContextGatheredAt time.Time `json:"context_gathered_at,omitempty"`
	ContextCommit     string    `json:"context_commit,omitempty"`
	LastRunWorkItemID string    `json:"last_run_work_item_id,omitempty"`
	// Resumable says whether a later process can continue this conversation. A
	// record whose first turn never completed has no provider session, and
	// speaking to it starts again rather than carrying on.
	Resumable bool `json:"resumable"`
}

// runReport is one run this agent's work is recorded in. Runs are not owned by
// a role — a run is a developer attempt, its checks, and a reviewer verdict —
// so they are reported against the roles that actually execute inside one
// rather than against every configured agent.
type runReport struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func runAgentCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printAgentUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listAgents(args[1:], stdout, stderr)
	case "show":
		return showAgent(args[1:], stdout, stderr)
	case "chat":
		return chatWithAgent(ctx, args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown agent command %q\n\n", args[0])
		printAgentUsage(stderr)
		return 2
	}
}

func listAgents(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "agent list does not accept positional arguments; use `yoyo agent show <name>` for one agent")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reports, err := readAgents(parts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product": parts.config.Product.ID,
			"agents":  reports,
		})
	}
	fmt.Fprintf(stdout, "%d configured agent(s) for %s:\n", len(reports), parts.config.Product.ID)
	for _, report := range reports {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, renderAgent(report))
	}
	return 0
}

func showAgent(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "name the agent to show, as `yoyo agent show <name>`; `yoyo agent list` names them")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	name, _, err := resolveAgent(parts.config, flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reports, err := readAgents(parts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var found agentReport
	for _, report := range reports {
		if report.Name == name {
			found = report
			break
		}
	}
	// The runs are the other half of durable state: what the harness is in the
	// middle of doing, as opposed to what it has been told. They are reported
	// for the roles that execute inside a run and left out for the roles that do
	// not, rather than shown empty for every agent as though the developer were
	// idle.
	var runs []runReport
	if found.Role == domain.RoleDeveloper || found.Role == domain.RoleReviewer {
		runs, err = readRunsInFlight(parts.store)
		if err != nil {
			found.Problem = appendProblem(found.Problem, err.Error())
		}
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product": parts.config.Product.ID,
			"agent":   found,
			"runs":    runs,
		})
	}
	fmt.Fprint(stdout, renderAgent(found))
	if found.Role == domain.RoleDeveloper || found.Role == domain.RoleReviewer {
		if len(runs) == 0 {
			fmt.Fprintln(stdout, "  no run is in flight")
		}
		for _, run := range runs {
			fmt.Fprintf(stdout, "  run %s on %s: %s", run.RunID, run.WorkItemID, run.Status)
			if run.Phase != "" {
				fmt.Fprintf(stdout, " (%s)", run.Phase)
			}
			fmt.Fprintf(stdout, ", last moved %s\n", run.UpdatedAt.UTC().Format(time.RFC3339))
		}
	}
	return 0
}

func chatWithAgent(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	role, request, code := agentConversationRequest(args, stderr)
	if code != 0 {
		return code
	}
	return converse(ctx, role, request, stdin, stdout, stderr)
}

// agentConversationRequest turns the command line into the conversation it asks
// for: which role answers, and which configured agent fills it. Both travel on,
// because the role decides the contract and the agent decides the persona and
// the model — resolving the name here and looking the role up again later would
// address whichever agent sorted first in a project that configured two.
//
// It is separate from holding the conversation so that what the operator named
// is checked before a lease is taken or a provider is started, and so that what
// it resolved can be tested without either.
func agentConversationRequest(args []string, stderr io.Writer) (domain.AgentRole, conversationRequest, int) {
	flags := flag.NewFlagSet("agent chat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	message := flags.String("message", "", "send one message and print the reply instead of opening an interactive conversation")
	fresh := flags.Bool("new", false, "start a new conversation instead of resuming the recorded one")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON (requires --message)")
	if err := flags.Parse(args); err != nil {
		return "", conversationRequest{}, 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "name the agent to talk to, as `yoyo agent chat <name>`; `yoyo agent list` names them")
		return "", conversationRequest{}, 2
	}
	if *jsonOutput && *message == "" {
		fmt.Fprintln(stderr, "agent chat --json requires --message: an interactive conversation has no single result to encode")
		return "", conversationRequest{}, 2
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", conversationRequest{}, 1
	}
	name, role, err := resolveAgent(resolved.Config, flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", conversationRequest{}, 1
	}
	if _, known := chat.AuthorityFor(role); !known {
		fmt.Fprintf(stderr, "the harness holds no conversation contract for role %q, so there is nothing to say to it\n", role)
		return "", conversationRequest{}, 1
	}
	return role, conversationRequest{
		agentName:  name,
		configPath: *configPath,
		message:    *message,
		fresh:      *fresh,
		jsonOutput: *jsonOutput,
	}, 0
}

// readAgents reports every configured agent with whatever durable state it has.
// A conversation that cannot be read is reported against the agent it belongs
// to rather than failing the listing: an operator asking who is configured is
// owed the answer even when one record is unreadable.
func readAgents(parts components) ([]agentReport, error) {
	store, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parts.config.Agents))
	for name := range parts.config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	reports := make([]agentReport, 0, len(names))
	for _, name := range names {
		agent := parts.config.Agents[name]
		report := agentReport{
			Name:           name,
			Role:           agent.Role,
			Backend:        agent.Backend,
			Model:          agent.Model,
			Account:        agent.Account,
			Instances:      agent.Instances,
			PersonaPath:    agent.Persona.Path,
			PersonaVersion: agent.Persona.Version,
		}
		if authority, known := chat.AuthorityFor(agent.Role); known {
			report.Addressable = true
			report.Owns = authority.Owns
		}
		identity := runstate.ConversationIdentity{Agent: name, Role: agent.Role}
		recorded, err := store.Load(identity)
		switch {
		case err == nil:
			report.Conversation = &conversationReport{
				ID:                recorded.ConversationID,
				Turns:             recorded.Turns,
				StartedAt:         recorded.StartedAt,
				UpdatedAt:         recorded.UpdatedAt,
				ProviderSessionID: recorded.ProviderSessionID,
				RequestedModel:    recorded.ProviderModel,
				ResolvedModel:     recorded.ProviderResolvedModel,
				ContextGatheredAt: recorded.ContextGatheredAt,
				ContextCommit:     recorded.ContextCommit,
				LastRunWorkItemID: recorded.LastRunWorkItemID,
				Resumable:         recorded.ProviderSessionID != "",
			}
		case errors.Is(err, runstate.ErrNoConversation):
		default:
			report.Problem = err.Error()
		}
		inUse, problem := conversationInUse(store, identity)
		report.InUse = inUse
		if problem != "" {
			report.Problem = appendProblem(report.Problem, problem)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// conversationInUse reports whether another process is holding this role's
// conversation, and why the question could not be answered when it could not.
// It answers by taking the same lease a conversation would and letting it go
// again immediately: an advisory lock is the only thing that actually decides
// this, so asking it is the only answer that is not a guess.
//
// A failure to ask is not an answer. Reporting one as "in use" would tell the
// operator that every agent at once was mid-conversation whenever the state
// directory could not be opened, which is both wrong and the opposite of what
// they would do about it.
func conversationInUse(store *runstate.ConversationStore, identity runstate.ConversationIdentity) (bool, string) {
	lease, err := store.Hold(identity)
	switch {
	case errors.Is(err, runstate.ErrConversationHeld):
		return true, ""
	case err != nil:
		return false, err.Error()
	}
	// A lease that cannot be released leaves this process holding it until it
	// exits, which is the safe direction: a one-shot command exits immediately
	// and the operating system drops the lock with it.
	_ = lease.Release()
	return false, ""
}

// readRunsInFlight reports the runs that have not finished, which is what the
// roles that execute inside a run are currently in the middle of.
func readRunsInFlight(store *runstate.Store) ([]runReport, error) {
	states, err := store.Incomplete()
	if err != nil {
		return nil, err
	}
	reports := make([]runReport, 0, len(states))
	for _, state := range states {
		reports = append(reports, runReport{
			RunID:      state.RunID,
			WorkItemID: state.WorkItemID,
			Status:     string(state.Status),
			Phase:      string(state.Phase),
			Branch:     state.Branch,
			UpdatedAt:  state.UpdatedAt,
		})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].UpdatedAt.After(reports[j].UpdatedAt) })
	return reports, nil
}

// resolveAgent turns what the operator typed into the agent it names. An agent
// name is what a project actually configured, and a role name is what an
// operator is more likely to remember, so both work — and a role filled by more
// than one agent is refused rather than resolved to whichever sorted first,
// because addressing "the developer" when there are three of them is a question
// rather than a request.
func resolveAgent(cfg config.Config, requested string) (string, domain.AgentRole, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", "", errors.New("name the agent")
	}
	if agent, ok := cfg.Agents[trimmed]; ok {
		return trimmed, agent.Role, nil
	}
	var matches []string
	for name, agent := range cfg.Agents {
		if string(agent.Role) == trimmed {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 1:
		return matches[0], cfg.Agents[matches[0]].Role, nil
	case 0:
		return "", "", fmt.Errorf("no agent named %q is configured; `yoyo agent list` names them", requested)
	default:
		return "", "", fmt.Errorf("%d agents fill the %s role (%s); name the one you mean",
			len(matches), trimmed, strings.Join(matches, ", "))
	}
}

func renderAgent(report agentReport) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s (%s) %s, model %s, account %s, %d instance(s)\n",
		report.Name, report.Role, report.Backend, report.Model,
		recorded(report.Account, "none the configuration names"), report.Instances)
	if report.Owns != "" {
		fmt.Fprintf(&rendered, "  owns %s\n", report.Owns)
	}
	if report.PersonaPath != "" {
		fmt.Fprintf(&rendered, "  persona %s (%s)\n", report.PersonaPath, report.PersonaVersion)
	}
	if !report.Addressable {
		fmt.Fprintln(&rendered, "  the harness holds no conversation with this role")
	}
	switch {
	case report.Conversation == nil:
		fmt.Fprintln(&rendered, "  no conversation recorded")
	default:
		conversation := report.Conversation
		fmt.Fprintf(&rendered, "  conversation %s: %d turn(s), last spoken to %s\n",
			conversation.ID, conversation.Turns, conversation.UpdatedAt.UTC().Format(time.RFC3339))
		if conversation.ResolvedModel != "" && conversation.ResolvedModel != conversation.RequestedModel {
			fmt.Fprintf(&rendered, "  last answered by %s (asked for %s)\n", conversation.ResolvedModel, conversation.RequestedModel)
		}
		if !conversation.Resumable {
			fmt.Fprintln(&rendered, "  no provider session recorded, so saying something starts it again")
		}
		if !conversation.ContextGatheredAt.IsZero() {
			fmt.Fprintf(&rendered, "  working from a picture taken %s", conversation.ContextGatheredAt.UTC().Format(time.RFC3339))
			if conversation.ContextCommit != "" {
				fmt.Fprintf(&rendered, " at %s", conversation.ContextCommit)
			}
			fmt.Fprintln(&rendered)
		}
		if conversation.LastRunWorkItemID != "" {
			fmt.Fprintf(&rendered, "  last started work on %s\n", conversation.LastRunWorkItemID)
		}
	}
	if report.InUse {
		fmt.Fprintln(&rendered, "  in use by another process right now")
	}
	if report.Problem != "" {
		fmt.Fprintf(&rendered, "  durable state could not be read: %s\n", report.Problem)
	}
	return rendered.String()
}

// appendProblem joins what could not be read, so a second failure does not
// replace the first.
func appendProblem(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "; " + addition
}

func printAgentUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo agent <list|show|chat> [options] [<name>]

  list                       the configured agents, and what each is in the middle of
  show [options] <name>      one agent in full, with the work its role is executing
  chat [options] <name>      talk to one agent; <name> is its name or its role

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

agent chat options:
  --message <text>  send one message and print the reply instead of conversing
  --new             start a new conversation instead of resuming the recorded one

An agent is a durable logical identity: the provider process that answers is
started for a turn and gone afterwards, and what survives it is the conversation
recorded here. Talking to the product manager is what "yoyo chat" does, and
"yoyo agent chat product-manager" is the same conversation reached the long way.

What each role may do is fixed by the harness rather than by its persona: the
product manager owns the backlog, the development manager decomposes admitted
work underneath it and cannot admit or reorder any, the architect owns the
designs and invariants and edits nothing from a conversation, and the developer
and reviewer do their real work inside runs.`)
}
