package codex

// Codex's operational dialect: how this provider says it is retrying, how it
// says a limit is refusing work, and how to tell a server that could not serve
// the attempt apart from a request that will be refused however often it is
// asked.
//
// It is one implementation of the provider contract in internal/backend, exactly
// as the Claude Code dialect beside it is, and nothing above either of them
// special-cases a provider: the parser hands the dialect an event, the dialect
// answers with one of the contract's six answers, and the harness alone decides
// what waiting that answer earns. The dialect states no duration anywhere, which
// is the property that keeps a provider from being able to spend an account.

import (
	"regexp"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// The provider's own names for the events this dialect reads. Everything else in
// the stream is prose, shell calls, and accounting, and the dialect says nothing
// about any of it.
const (
	// eventStreamError is Codex reporting that the model stream failed and that
	// it is retrying by itself. The attempt has not ended and nothing about the
	// account is exhausted, so it is evidence rather than a reason for the
	// harness to wait.
	eventStreamError = "stream_error"
	// eventError is Codex ending the invocation on something it could not carry
	// on past. It is this provider's whole error channel: an attempt that reached
	// the agent at all ends on a completed task instead.
	eventError = "error"
	// eventTaskComplete is the invocation's own successful terminal, carrying the
	// agent's last message.
	eventTaskComplete = "task_complete"
)

// usageLimitMessage is a limit that is refusing work, matched on the words the
// provider announces one with. It is checked before the status patterns below
// because a limit is reported as an HTTP 429 as often as it is reported in a
// sentence, and 429 is also a client status.
var usageLimitMessage = regexp.MustCompile(`(?i)usage limit|rate limit|quota|\b429\b`)

// serverErrorStatus and overloadMessage are the provider's own servers
// transiently unable to serve the attempt. Nothing about the account is
// exhausted and no reset time is ever quoted, which is why this is not folded
// into an unknown reset: that case polls on the interval an exhausted account
// deserves, and this one lifts two orders of magnitude sooner.
var (
	serverErrorStatus = regexp.MustCompile(`\b5\d{2}\b`)
	overloadMessage   = regexp.MustCompile(`(?i)overloaded|server error|service unavailable|temporarily unavailable`)
)

// clientErrorStatus marks the statuses that describe the request rather than the
// server's ability to serve it. A relaunch would put the identical request in
// front of the provider again and earn the identical refusal, so nothing in this
// class is transient.
var clientErrorStatus = regexp.MustCompile(`\b4\d{2}\b`)

// resetTimestamp reads a reset time the provider stated as a machine timestamp.
// A reset quoted any other way -- "try again in three hours", a wall-clock time
// in somebody's local zone -- is no reset time this dialect can read, and what
// the harness makes of that is the contract's answer rather than this dialect's:
// the limit still arrives as a limit, and the wait becomes the configured
// recheck interval instead of a deadline.
var resetTimestamp = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}[Tt][0-9:.]+(?:[Zz]|[+-]\d{2}:\d{2}))`)

// Dialect reads Codex. It is stateless and carries no clock, because nothing it
// answers depends on either: it says what the provider said, and every judgement
// about time belongs to the caller.
type Dialect struct{}

var _ backend.Dialect = Dialect{}

func (Dialect) Name() string { return sourceName }

// Observe reports what one event from this provider says about the attempt.
//
// A terminal that succeeded is deliberately not an answer. Codex says nothing
// about capacity on a completed task, so reading one as evidence that a limit
// has lifted would be this dialect inventing a fact the provider never stated.
func (Dialect) Observe(event backend.ProviderEvent) (backend.Observation, bool) {
	switch {
	case event.Type == eventStreamError:
		return backend.Observation{Answer: backend.AnswerRetrying}, true
	case event.Terminal && event.Failed:
		return observeFailedTerminal(event)
	default:
		return backend.Observation{}, false
	}
}

// observeFailedTerminal tells the ways this provider can end an invocation badly
// apart. A limit is a wait on a deadline, an overloaded server is a much shorter
// wait, a status describing the request is a refusal that stands, and everything
// else is a death that judged nothing about the work.
//
// The leftovers are read as transient on purpose, which is the same trade the
// Claude Code dialect makes and for the same recorded reason: being wrong about
// a transient death costs one more invocation against a budget the harness
// keeps, and being wrong about a refusal that stands costs the whole run and a
// worktree somebody reconciles by hand. Codex reaches this branch only through
// its own error channel, which carries API and stream failures rather than the
// agent's judgement of the work, so the leftovers here are weather far more
// often than they are a verdict.
//
// The kind of limit is deliberately left unnamed. Codex does not put its own
// name for the exhausted window in the message, and Kind is evidence the
// provider gave rather than a label the harness may invent.
func observeFailedTerminal(event backend.ProviderEvent) (backend.Observation, bool) {
	described := backend.DescribeFailure(event.Subtype, event.Text)
	switch {
	case usageLimitMessage.MatchString(event.Text):
		return backend.Observation{
			Answer:   backend.AnswerLimitReached,
			ResetsAt: readReset(event.Text),
			Detail:   described,
		}, true
	case overloadMessage.MatchString(event.Text) || serverErrorStatus.MatchString(event.Text):
		return backend.Observation{Answer: backend.AnswerUnavailable, Detail: described}, true
	case clientErrorStatus.MatchString(event.Text):
		return backend.Observation{Answer: backend.AnswerRefused, Detail: described}, true
	default:
		return backend.Observation{Answer: backend.AnswerInterrupted, Detail: described}, true
	}
}

// readReset is the instant the provider said the limit lifts, and the zero time
// when it named none this dialect can read. It never guesses: an unreadable
// reset is a fact about the provider, and the contract decides what to do about
// it in one place for every provider.
func readReset(message string) time.Time {
	stated := resetTimestamp.FindStringSubmatch(message)
	if stated == nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, stated[1])
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
