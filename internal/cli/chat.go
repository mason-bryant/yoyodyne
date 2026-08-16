package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"yoyodyne/internal/backend/claudecode"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/chat"
	"yoyodyne/internal/config"
	"yoyodyne/internal/contextbundle"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/runstate"
)

// chatWorkItemStatus is the tracker slice a product conversation is built from.
// Open work is what product intent is currently being spent on; the closed
// history is not what the operator is deciding about.
const chatWorkItemStatus = "open"

// chatTrackerTimeout bounds reading tracker state while assembling the product
// context, so an unresponsive tracker delays a conversation rather than
// preventing one.
const chatTrackerTimeout = 30 * time.Second

type chatOutput struct {
	Evidence *chat.Evidence `json:"evidence,omitempty"`
	Reply    string         `json:"reply,omitempty"`
	Error    string         `json:"error,omitempty"`
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
		reply, err := session.Send(ctx, *message)
		if err != nil {
			evidence := reply.Evidence
			return reportChatFailure(stdout, stderr, *jsonOutput, &evidence, err)
		}
		if *jsonOutput {
			evidence := reply.Evidence
			return writeJSON(stdout, stderr, chatOutput{Evidence: &evidence, Reply: reply.Text})
		}
		fmt.Fprintln(stdout, reply.Text)
		printChatEvidence(stdout, reply.Evidence)
		return 0
	}

	printChatHeader(stdout, session.Evidence())
	converseErr := session.Converse(ctx, stdin, stdout)
	printChatEvidence(stdout, session.Evidence())
	if converseErr != nil {
		fmt.Fprintf(stderr, "conversation ended: %v\n", converseErr)
		return 1
	}
	return 0
}

// openChat builds the product manager's conversation from configuration: the
// configured agent, the repository's own Markdown, the tracker state as it
// stands, and the durable record a previous process left behind. The returned
// lease is this process's exclusive hold on that conversation.
func openChat(ctx context.Context, configPath string, fresh bool, stderr io.Writer) (*chat.Session, *runstate.Lease, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, nil, err
	}
	cfg := resolved.Config
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), cfg.Product.Repository)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve product repository: %w", err)
	}

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

	processRunner := execution.OSProcessRunner{}
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

	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := runstate.NewConversationStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return nil, nil, err
	}
	lease, err := store.Hold(domain.RoleProductManager)
	if err != nil {
		return nil, nil, err
	}

	briefing, err := assembleProductContext(ctx, repository, processRunner, stderr)
	if err != nil {
		return nil, nil, errors.Join(err, lease.Release())
	}
	session, err := chat.Open(chat.Options{
		Backend:      provider,
		Store:        store,
		Model:        agent.Model,
		Persona:      agent.Persona.Text,
		Provider:     domain.BackendClaudeCode,
		Repository:   repository,
		ProductID:    cfg.Product.ID,
		RepositoryID: string(cfg.Product.RepositoryID),
		Briefing:     briefing,
		RedactValues: execution.SensitiveEnvironmentValues(os.Environ()),
		Fresh:        fresh,
	})
	if err != nil {
		return nil, nil, errors.Join(err, lease.Release())
	}
	return session, lease, nil
}

// assembleProductContext gathers what the product manager reasons over. A
// tracker that cannot be read is reported in the context and to the operator
// rather than silently rendered as a product with no work in flight.
func assembleProductContext(ctx context.Context, repository string, runner execution.ProcessRunner, stderr io.Writer) (string, error) {
	trackerCtx, cancel := context.WithTimeout(ctx, chatTrackerTimeout)
	defer cancel()
	items, listErr := beads.Client{Runner: runner, Dir: repository}.List(trackerCtx, chatWorkItemStatus)
	unavailable := ""
	if listErr != nil {
		unavailable = listErr.Error()
		fmt.Fprintf(stderr, "warning: Beads state is unavailable, continuing without it: %v\n", listErr)
	}
	bundle, err := contextbundle.AssembleProduct(contextbundle.ProductRequest{
		RepositoryRoot:       repository,
		WorkItems:            items,
		WorkItemsUnavailable: unavailable,
	})
	if err != nil {
		return "", fmt.Errorf("assemble product context: %w", err)
	}
	return bundle.Text, nil
}

func reportChatFailure(stdout, stderr io.Writer, jsonOutput bool, evidence *chat.Evidence, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, chatOutput{Evidence: evidence, Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "chat failed: %v\n", err)
	if evidence != nil && evidence.ConversationID != "" {
		fmt.Fprintf(stderr, "conversation: %s\n", evidence.ConversationID)
	}
	return 1
}

func printChatHeader(writer io.Writer, evidence chat.Evidence) {
	state := "new conversation"
	if evidence.Resumed {
		state = fmt.Sprintf("resumed conversation after %d turn(s)", evidence.Turns)
	}
	fmt.Fprintf(writer, "product manager: %s (%s, model %s)\n", evidence.ConversationID, state, evidence.RequestedModel)
	fmt.Fprintln(writer, "The product manager is advisory here: it changes nothing and approves nothing.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
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
	fmt.Fprintln(writer, `Usage: yoyodyne chat [options]

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --message <text>   send one message and print the reply instead of conversing
  --new              start a new conversation instead of resuming the recorded one
  --json             emit machine-readable JSON (requires --message)`)
}
