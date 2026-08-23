package cli

// The operator's view of what the roles have asked each other, and the harness's
// hand that carries a question from one to the other.
//
// Both halves are here because both are the harness's rather than any role's.
// The voice below starts the provider that answers, under a prompt that gives it
// no tools and no authority; the command above reads the durable threads. What
// makes the channel safe to have at all is that neither role reaches either one.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// exchangeAnswerTimeout bounds one answering invocation. It is shorter than a
// conversation turn's because an exchange round is one question answered in
// prose with nothing to look up: a round still running past this is one the
// asking conversation is waiting on for no reason it can see.
const exchangeAnswerTimeout = 5 * time.Minute

func runExchange(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printExchangeUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listExchanges(args[1:], stdout, stderr)
	case "show":
		return showExchange(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown exchange command %q\n\n", args[0])
		printExchangeUsage(stderr)
		return 2
	}
}

func listExchanges(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("exchange list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "exchange list does not accept positional arguments; use `yoyo exchange show <id>` for one exchange")
		return 2
	}

	store, productID, err := exchangeStore(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	recorded, err := store.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product":   productID,
			"exchanges": recorded,
		})
	}
	if len(recorded) == 0 {
		fmt.Fprintln(stdout, "exchanges: no role has asked another one anything.")
		return 0
	}
	var spent float64
	for _, one := range recorded {
		spent += one.CostUSD()
	}
	fmt.Fprintf(stdout, "%d exchange(s) for %s, costing $%.4f in total:\n", len(recorded), productID, spent)
	for _, one := range recorded {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, one.Render())
	}
	return 0
}

func showExchange(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("exchange show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "name the exchange to show, as `yoyo exchange show <id>`; `yoyo exchange list` names them")
		return 2
	}

	store, _, err := exchangeStore(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A prefix names one exchange the way it names one directive, because nobody
	// types thirty-two hex digits out of a listing.
	found, err := store.Find(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, found)
	}
	fmt.Fprint(stdout, found.RenderThread())
	return 0
}

func exchangeStore(configPath string) (*runstate.ExchangeStore, string, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, "", err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, "", err
	}
	store, err := runstate.NewExchangeStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, "", err
	}
	return store, string(resolved.Config.Product.ID), nil
}

// exchangeVoice is the answering half of the channel: one toolless provider
// invocation per round, under the answering role's identity and the harness's
// own boundary.
//
// It answers under the agent configured for the role, which is the same
// resolution `yoyo agent chat` makes, so the architect that answers an exchange
// is the architect an operator would have addressed. A role nobody configured is
// not an empty answer: it is refused, and the round records that there was
// nobody to ask.
type exchangeVoice struct {
	config     config.Config
	provider   chat.Backend
	repository string
	// usageLimits is where a provider refusing this round for want of capacity is
	// written down. An answering round has no run to park and no conversation of
	// its own to fail at somebody's terminal, so without this an exhausted limit
	// met here would stop an exchange and leave no trace anywhere — and an
	// exhausted limit is hours in which nothing happens anywhere, which is exactly
	// what somebody not watching this conversation needs to be told.
	usageLimits  *runstate.UsageLimitStore
	productID    domain.ProductID
	redactValues []string
}

func (v exchangeVoice) Answer(ctx context.Context, question exchange.Question) (exchange.Spoken, error) {
	name := agentNameForRole(v.config, question.Role)
	if name == "" {
		return exchange.Spoken{}, fmt.Errorf("no %s agent is configured, so there is nobody to ask", question.Role)
	}
	agent := v.config.Agents[name]
	if agent.Backend != domain.BackendClaudeCode {
		return exchange.Spoken{}, fmt.Errorf("an exchange requires a claude-code agent, and the %s agent %s is configured for %q",
			question.Role, name, agent.Backend)
	}
	prompt := execution.NewRedactor(v.redactValues...).Redact(renderQuestion(question))
	result, err := v.provider.Run(ctx, backend.RunRequest{
		// The exchange is the record this invocation belongs to, so it is what the
		// provider is told the invocation is: an answering round has no run and no
		// conversation of its own.
		RunID:            question.ExchangeID,
		Role:             question.Role,
		WorkingDirectory: v.repository,
		Prompt:           prompt,
		SystemPrompt:     chat.AnsweringPrompt(question.Role, agent.Persona.Text),
		SessionID:        question.SessionID,
		Model:            agent.Model,
		// No tools at all, exactly as a conversation gets none. What separates this
		// from a conversation is only that there is no authority behind it either.
		PermissionMode: "plan",
		AllowedTools:   []string{},
		Timeout:        exchangeAnswerTimeout,
		RedactValues:   v.redactValues,
	})
	spoken := exchange.Spoken{Agent: name, SessionID: result.SessionID, CostUSD: result.CostUSD}
	// The refusal is recorded before the round is failed, because it is a fact
	// about the whole product rather than about this exchange. Failing to record
	// it never replaces the refusal itself in what the round reports: the round is
	// spent either way, and the exchange says so.
	refusal := v.noteUsageLimit(question, result, err)
	switch {
	case err != nil:
		return spoken, errors.Join(fmt.Errorf("the %s could not be reached: %w", chat.RoleTitle(question.Role), err), refusal)
	case result.IsError:
		return spoken, errors.Join(
			fmt.Errorf("the %s reported failure: %s", chat.RoleTitle(question.Role), result.DescribeFailure()),
			refusal)
	}
	spoken.Answer = result.FinalText
	return spoken, nil
}

// noteUsageLimit records a provider refusal this round met, exactly as a
// conversation turn records one, and reports only what went wrong recording it.
// A round that was not refused, and a voice with nowhere to record one, both
// record nothing and say nothing.
func (v exchangeVoice) noteUsageLimit(question exchange.Question, result backend.RunResult, err error) error {
	if result.UsageLimit == nil || (err == nil && !result.IsError) || v.usageLimits == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     v.productID,
		At:            time.Now().UTC(),
		Waiting: fmt.Sprintf("the %s answering exchange %s, asked by the %s",
			chat.RoleTitle(question.Role), question.ExchangeID, chat.RoleTitle(question.Asker)),
		Kind: result.UsageLimit.Kind,
	}
	if !result.UsageLimit.ResetsAt.IsZero() {
		resetsAt := result.UsageLimit.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := v.usageLimits.Record(exhaustion); err != nil {
		return fmt.Errorf("record the provider's refusal: %w", err)
	}
	return nil
}

// renderQuestion is what the answering role is sent. The thread before this
// round is included on the first invocation of a session and left out afterwards
// only in the sense that the provider already holds it: it is sent every time,
// because a session the provider dropped would otherwise answer round four with
// no idea what rounds one to three said.
func renderQuestion(question exchange.Question) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "# The %s is asking you something\n\n", chat.RoleTitle(question.Asker))
	fmt.Fprintf(&rendered, "This is exchange %s, round %d of the %d it is allowed.\n\n",
		question.ExchangeID, question.Round, question.MaxRounds)
	for _, earlier := range question.Earlier {
		fmt.Fprintf(&rendered, "## Round %d\n\n%s asked: %s\n\n",
			earlier.Number, chat.RoleTitle(question.Asker), strings.TrimSpace(earlier.Question))
		if context := strings.TrimSpace(earlier.Context); context != "" {
			fmt.Fprintf(&rendered, "Their framing: %s\n\n", context)
		}
		switch {
		case strings.TrimSpace(earlier.Answer) != "":
			fmt.Fprintf(&rendered, "You answered: %s\n\n", strings.TrimSpace(earlier.Answer))
		default:
			rendered.WriteString("That round produced no answer.\n\n")
		}
	}
	rendered.WriteString("## The question\n\n")
	rendered.WriteString(strings.TrimSpace(question.Question) + "\n")
	if context := strings.TrimSpace(question.Context); context != "" {
		fmt.Fprintf(&rendered, "\nTheir framing, which is what they think rather than evidence: %s\n", context)
	}
	rendered.WriteString("\nAnswer it in prose. Nothing else you write will be carried out.\n")
	return rendered.String()
}

// conversationExchanges is the channel as a conversation reaches it, or nothing
// where the role holding that conversation is not on the channel. Wiring it only
// for the roles that may ask is the same decision the triage budgets are wired
// by: a capability a role has no authority for is not one its conversation
// should be able to reach at all.
func conversationExchanges(parts components, role domain.AgentRole, provider chat.Backend) chat.Exchanges {
	authority, known := chat.AuthorityFor(role)
	if !known || !authority.Asks {
		return nil
	}
	store, err := runstate.NewExchangeStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		// A store that cannot be built is a conversation with no channel, which
		// refuses an ask plainly. It is not a conversation that fails to open: the
		// operator came here to talk about the product.
		return nil
	}
	return exchange.Conductor{
		Store: store,
		Voice: exchangeVoice{
			config:       parts.config,
			provider:     provider,
			repository:   parts.repository,
			usageLimits:  parts.usageLimits,
			productID:    parts.config.Product.ID,
			redactValues: parts.redactValues,
		},
		Reports:      parts.reports,
		MaxRounds:    parts.config.Exchange.MaxRounds,
		ProductID:    parts.config.Product.ID,
		RepositoryID: string(parts.config.Product.RepositoryID),
	}
}

func printExchangeUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo exchange <list|show> [options] [<id>]

  list             every exchange the roles have had, open ones first
  show <id>        one exchange in full, every question and every answer

Options:
  --config <path>  configuration file (default: the nearest .yoyodyne/config.yaml)
  --json           emit machine-readable JSON

An exchange is one role asking another something through the harness: the
product manager asking the architect what a goal costs, the architect asking the
product manager whether a trade-off is one a user would accept. It moves opinion
and never evidence -- both sides are toolless -- and it carries no authority, so
nothing in one admits work, orders a backlog, or edits a document. It is
recorded so that two roles can never say anything to each other that you cannot
read afterwards, with what each one cost beside the rounds it took.

An exchange that reaches its round limit closes as unresolved and is reported to
you, because two roles deferring to each other for ever is the one way this can
fail and a silent limit would hide it.`)
}
