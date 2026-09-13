package cli

// The provider side of a side conversation: one toolless invocation that puts a
// question to a role on its own side thread and brings back what it said.
//
// It is here rather than in `internal/sidestream` for the reason the exchange's
// answering voice is here: starting a provider needs the project's
// configuration — which agent fills the role, which model it is served under,
// which account pays for it — and the package that holds a side conversation
// must not depend on any of that. What that package holds is the record, the
// lease, the turn cap, and the boundary the reply is read against; what this
// holds is how one invocation is made.
//
// It is the harness's own hand, like every other voice here. No role reaches it,
// nothing configures its way into it, and the authority it carries is none:
// `harness-is-the-only-role-invoker` is satisfied because the only thing that
// calls this is the runner, and the only thing that calls the runner is the
// harness.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// sideTurnTimeout bounds one side turn. It is the exchange round's bound rather
// than a conversation turn's: a side thread answers a question in prose with
// nothing to look up, and a turn still running past this is one whoever asked is
// waiting on for no reason they can see.
const sideTurnTimeout = 5 * time.Minute

// sideVoice answers a side thread's turns under the agent configured for the
// role, which is the same resolution `yoyo agent chat` makes — so the architect
// that answers beside its main thread is the architect an operator would have
// addressed. A role nobody configured is not an empty answer: it is refused, and
// the turn records that there was nobody to ask.
type sideVoice struct {
	config     config.Config
	provider   chat.Backend
	repository string
	// usageLimits is where a provider refusing this turn for want of capacity is
	// written down. A side turn has no run to park and no conversation of its own
	// to fail at somebody's terminal, so without this an exhausted limit met here
	// would stop a side thread and leave no trace anywhere.
	usageLimits *runstate.UsageLimitStore
	// spend is where what a turn costs is written down. A side turn is a provider
	// invocation like any other and the design prices it beside the
	// conversations, so it is charged to the side stream — its own subject on the
	// cost line, because a line naming the main thread would put a side turn's
	// cost on a conversation that never took it.
	spend     spend.Log
	productID domain.ProductID
	// stateRoot is where the answering account's provider home is found. A voice
	// built without one answers where the machine is already signed in, which is
	// the single-account arrangement and is what a test wiring the voice directly
	// gets.
	stateRoot    string
	redactValues []string
	// clock is what the turn reads the time from, so the failover seam is
	// testable on a fixed clock. A voice built without one reads the wall clock.
	clock func() time.Time
}

func (v sideVoice) now() time.Time {
	if v.clock == nil {
		return time.Now().UTC()
	}
	return v.clock().UTC()
}

// Answer takes one side turn. It satisfies sidestream.Voice.
func (v sideVoice) Answer(ctx context.Context, question sidestream.Question) (sidestream.Spoken, error) {
	name := strings.TrimSpace(question.Agent)
	if name == "" {
		name = agentNameForRole(v.config, question.Role)
	}
	if name == "" {
		return sidestream.Spoken{}, fmt.Errorf("no %s agent is configured, so there is nobody to hold a side thread", question.Role)
	}
	agent, configured := v.config.Agents[name]
	if !configured {
		return sidestream.Spoken{}, fmt.Errorf("no agent named %s is configured, so %s names nobody", name, question.StreamID)
	}
	// Whether this agent holds side threads at all is the per-agent knob, and it
	// is read here because this is where a side turn becomes a provider
	// invocation. An agent left at the default queues, which is what every agent
	// did before the knob existed, so a project acquires side threads by writing
	// the key rather than by upgrading. What the knob never decides is what the
	// answer may do: that is `sidestream.Permitted` and the role's own authority
	// narrowed by it, neither of which reads configuration.
	if !v.config.AgentHoldsSideThreads(name) {
		return sidestream.Spoken{}, fmt.Errorf("the %s agent %s is configured for %q conversations, so it holds no side threads; %s has nobody to answer it",
			question.Role, name, v.config.AgentConversationMode(name), question.StreamID)
	}
	if agent.Backend != domain.BackendClaudeCode {
		return sidestream.Spoken{}, fmt.Errorf("a side thread requires a claude-code agent, and the %s agent %s is configured for %q",
			question.Role, name, agent.Backend)
	}
	prompt := execution.NewRedactor(v.redactValues...).Redact(renderSideQuestion(question))
	// The turn is answered on the endpoint the agent is configured for, under the
	// account its main conversation is held under: a side thread is that role
	// speaking, and what it costs belongs on that role's subscription. The
	// endpoint names the provider, the adapter that reaches it, and the model
	// beside that account, which is what a substitution is checked against below.
	providers, err := v.config.ProviderRegistry()
	if err != nil {
		return sidestream.Spoken{}, fmt.Errorf("resolve the providers the %s agent %s may be served by: %w", question.Role, name, err)
	}
	choice, err := v.config.AgentEndpoint(providers, v.stateRoot, name)
	if err != nil {
		return sidestream.Spoken{}, fmt.Errorf("resolve the endpoint the %s agent %s answers on: %w", question.Role, name, err)
	}
	account := choice.Account
	// The turn goes through the meter, so what it spends is one line in the cost
	// log beside every other provider invocation the harness makes, charged to the
	// side stream because that is the record it belongs to. The phase is the
	// conversation's, which is where the design says a side thread is listed.
	provider := spend.Metered{
		Provider: v.provider,
		Log:      v.spend,
		Attribution: spend.Attribution{
			ProductID:      v.productID,
			Agent:          name,
			Phase:          runstate.SpendPhaseConversation,
			AccountAlias:   account.Alias,
			ConfigRevision: v.config.Revision(),
			Backend:        agent.Backend,
			SideStreamID:   question.StreamID,
		},
	}
	// The turn is served by the agent's permitted alternate where its own model
	// has no capacity, exactly as its main conversation's turn is: a role that can
	// still speak on its main thread and not beside it would be the same stall
	// moved one seam along. The failover sits outside the meter so each attempt is
	// priced against the model that attempt asked for.
	result, served, err := modelfailover.Serve(ctx, provider, backend.RunRequest{
		// The side stream is the record this invocation belongs to, so it is what
		// the provider is told the invocation is. Its events are named for it and
		// land in its own log, which is what keeps the two transcripts apart.
		RunID:            question.StreamID,
		Role:             question.Role,
		WorkingDirectory: v.repository,
		Prompt:           prompt,
		SystemPrompt:     chat.SidePrompt(question.Role, agent.Persona.Text),
		SessionID:        question.SessionID,
		Model:            agent.Model,
		// No tools at all, exactly as a conversation gets none. Whatever a side
		// thread may read, it reads through the harness and never through a
		// filesystem, a shell, or a network of its own.
		AllowedTools:     []string{},
		Timeout:          sideTurnTimeout,
		LastSequence:     question.LastSequence,
		RedactValues:     v.redactValues,
		EventSink:        question.Events,
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	}, v.failoverPolicy(question, name, choice.Endpoint, providers))
	// What served the turn travels back whether or not there was an answer,
	// because it is a fact about the invocation rather than about what came back,
	// and the stream is pinned to it either way. The build is this process's own:
	// a resident holding side threads goes on running the binary it was started
	// with.
	spoken := sidestream.Spoken{
		SessionID: result.SessionID,
		CostUSD:   result.CostUSD,
		Backend:   agent.Backend,
		// The model that actually asked, which is the configured one unless the
		// permitted alternate served the turn. Recording the configured selector
		// would leave the stream naming a model that refused it.
		Model:          served.Model,
		ResolvedModel:  result.ResolvedModel,
		AccountAlias:   account.Alias,
		ConfigRevision: v.config.Revision(),
		Build:          buildinfo.Commit(),
		LastEvent:      result.LastEvent,
	}
	// The refusal is recorded before the turn is failed, because it is a fact
	// about the whole product rather than about this side thread. Failing to
	// record it never replaces the refusal in what the turn reports: the turn is
	// spent either way, and the stream says so.
	refusal := v.noteSideUsageLimit(question, result, err)
	switch {
	case err != nil:
		return spoken, errors.Join(fmt.Errorf("the %s could not be reached on %s: %w",
			chat.RoleTitle(question.Role), question.StreamID, err), refusal)
	case result.IsError:
		return spoken, errors.Join(fmt.Errorf("the %s reported failure on %s: %s",
			chat.RoleTitle(question.Role), question.StreamID, result.DescribeFailure()), refusal)
	}
	spoken.Answer = result.FinalText
	return spoken, nil
}

// noteSideUsageLimit records a provider refusal this turn met, exactly as a
// conversation turn records one, and reports only what went wrong recording it.
func (v sideVoice) noteSideUsageLimit(question sidestream.Question, result backend.RunResult, err error) error {
	if result.UsageLimit == nil || (err == nil && !result.IsError) || v.usageLimits == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     v.productID,
		At:            v.now(),
		Waiting:       v.waitingOn(question),
		Kind:          result.UsageLimit.Kind,
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

// failoverPolicy is what a side turn may be served by when the model the agent
// is configured for will not take it, and where the substitution is written
// down. An agent that has not enabled failover produces the zero policy, which
// is failover off: one invocation, under the configured model.
//
// The endpoint the turn is on and the providers this project names travel with
// it, so a substitution is checked against the role's tool posture before it is
// made rather than after the turn has already moved.
func (v sideVoice) failoverPolicy(question sidestream.Question, name string, endpoint backend.Endpoint, providers *backend.Registry) modelfailover.Policy {
	// The alternate only where it stays on the provider this side thread is held
	// on, for the reason an exchange round reads the same answer: a side turn is
	// made on the agent's own endpoint and has no way to cross, and asking that
	// provider for another provider's model would fail on a selector nobody there
	// has heard of.
	alternate := v.config.AgentFailoverModelWithinProvider(name)
	if alternate == "" {
		return modelfailover.Policy{}
	}
	policy := modelfailover.Policy{
		Alternate: alternate,
		Now:       v.now,
		// How long a refusal that named no reset time stands before the agent's own
		// model is asked again, which is the same interval a run probes one on.
		UnknownResetPause: v.config.Execution.UsageLimitUnknownResetPause.Duration(),
		ProductID:         v.productID,
		Waiting:           v.waitingOn(question),
		Endpoint:          endpoint,
		Role:              question.Role,
		Eligibility:       providers,
	}
	if v.usageLimits != nil {
		policy.Windows = v.usageLimits
	}
	return policy
}

// waitingOn is the one sentence a refusal and a substitution both say about this
// turn. It is written once because the two are halves of the same fact — what
// would have stopped, and that something served it anyway — and a reader of
// either should not have to reconcile two descriptions of one side thread.
func (v sideVoice) waitingOn(question sidestream.Question) string {
	return fmt.Sprintf("the %s on side thread %s, held beside conversation %s",
		chat.RoleTitle(question.Role), question.StreamID, question.Conversation)
}

// renderSideQuestion is what the role holding a side thread is sent.
//
// It says where the thread has got to against its cap out of the stream's own
// durable record, which is why the contract does not state the cap as a number:
// a role being asked to land something and a role with turns to spare are being
// asked different things, and the difference is a fact about this thread rather
// than about the configuration.
func renderSideQuestion(question sidestream.Question) string {
	var rendered strings.Builder
	rendered.WriteString("# A question on your side thread\n\n")
	fmt.Fprintf(&rendered, "This is side thread %s, turn %d of the %d it is allowed, held beside your main conversation %s while that conversation is busy.\n\n",
		question.StreamID, question.Turn, question.MaxTurns, question.Conversation)
	fmt.Fprintf(&rendered, "It was opened about: %s\n\n", strings.TrimSpace(question.Topic))
	rendered.WriteString("## The question\n\n")
	rendered.WriteString(strings.TrimSpace(question.Question) + "\n")
	rendered.WriteString("\nAnswer it in prose. Nothing else you write is carried out here.\n")
	return rendered.String()
}
