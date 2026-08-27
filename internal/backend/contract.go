package backend

// The provider contract: what the harness needs a provider to say, in terms no
// one provider owns.
//
// Everything the harness knew about rate limits, retries, and when to try again
// used to be Claude Code's dialect, sitting in the adapter that speaks it. The
// envelope names, the field spellings, the unit a reset time arrives in, which
// terminal error is weather and which is a refusal that stands -- none of that
// is general, and all of it decides whether a run waits and for how long. A
// second provider inherits none of it, and a user who wants to reach a harness
// nobody here has seen inherits less.
//
// So the contract is the answers rather than a way to reach in and parse.
// Whatever a provider said, in whatever shape it said it, what the harness needs
// from a dialect is which of six things happened, and -- for the one answer where
// it makes sense -- when the provider said the condition lifts. A dialect
// describes; it never decides. There is deliberately no duration anywhere in
// what a dialect returns: how long to wait, whether to wait at all, and against
// which budget are the harness's, because that is what the operator's
// usage_limit settings and a run's safety properties rest on. A plugin that
// could decide to keep waiting could spend an account.
//
// The two cases already learned the hard way are named here rather than left for
// each dialect to rediscover: a limit that names no reset time is unknown rather
// than fatal, and a reset time that is not in the future is malformed and must
// not be trusted. ReadReset is the single place either is decided.
//
// See docs/provider-plugins.md for how a dialect is delivered and what a
// user-supplied one may and may not do.

import (
	"encoding/json"
	"strings"
	"time"
)

// Answer is what a provider said about one attempt, in the only terms the
// harness acts on. The set is closed on purpose: a dialect that cannot say which
// of these happened is describing something the harness has no response to, and
// saying nothing is the honest answer for that rather than inventing a seventh.
type Answer string

const (
	// AnswerServed is the provider reporting capacity: this attempt was served,
	// or a limit that was refusing is refusing no longer. It is what supersedes
	// an earlier limit, because providers re-report their limits as they change
	// and the last word wins.
	AnswerServed Answer = "served"
	// AnswerRetrying is the provider handling a transient condition itself. It
	// is evidence and nothing more -- the attempt has not ended, nothing about
	// the account is exhausted, and the harness neither waits nor counts it.
	AnswerRetrying Answer = "retrying"
	// AnswerLimitReached is a usage limit that is refusing work and will lift.
	// It is the only answer that carries a reset time, and the only one that may
	// carry the provider's own name for the limit.
	AnswerLimitReached Answer = "limit-reached"
	// AnswerUnavailable is the provider's own servers transiently unable to
	// serve the attempt. Nothing about the account is exhausted and no reset
	// time is ever quoted, which is why it is not folded into an unknown reset:
	// that case polls on the interval an exhausted account deserves, and this
	// one lifts two orders of magnitude sooner.
	AnswerUnavailable Answer = "unavailable"
	// AnswerInterrupted is the attempt dying of something that judged nothing
	// about the work and may not happen again -- an API error the provider's own
	// retries did not outlast, or a response cut off mid-flight. It names no
	// condition that will lift, so what it asks for is another attempt against a
	// budget the harness keeps rather than a wait on a clock nobody has.
	AnswerInterrupted Answer = "interrupted"
	// AnswerRefused is a refusal that stands. The same request put in front of
	// the same provider earns the same answer, so there is nothing to wait for
	// and nothing to relaunch into.
	AnswerRefused Answer = "refused"
)

// Answers is every answer the contract names, in the order they are documented.
var Answers = []Answer{
	AnswerServed,
	AnswerRetrying,
	AnswerLimitReached,
	AnswerUnavailable,
	AnswerInterrupted,
	AnswerRefused,
}

// Valid reports an answer the contract names. Anything else is refused where a
// dialect is built rather than met as a run that does nothing recognizable.
func (a Answer) Valid() bool {
	for _, known := range Answers {
		if a == known {
			return true
		}
	}
	return false
}

// DescribeAnswers names every answer, for a refusal that shows the choices
// rather than describing them.
func DescribeAnswers() string {
	named := make([]string, 0, len(Answers))
	for _, answer := range Answers {
		named = append(named, string(answer))
	}
	return strings.Join(named, ", ")
}

// ProviderEvent is one thing a provider said, reduced to what any dialect can
// match on. It is deliberately thin: a dialect that needs more than this has the
// provider's own payload verbatim in Payload, and a dialect delivered as data
// has only these to work with.
//
// Terminal and Failed are separate because they answer different questions. An
// invocation reaches a terminal whether it succeeded or not, and a dialect that
// only matched on failure could not report the successful terminal that
// supersedes a limit reported mid-stream.
type ProviderEvent struct {
	// Type and Subtype are the provider's own names for the event, whatever
	// vocabulary it uses.
	Type    string
	Subtype string
	// Text is the provider's prose for this event: the result text of a
	// terminal, the message of an error. It is what a message-shaped dialect
	// matches on, because a provider that reports a limit in a sentence has said
	// it nowhere else.
	Text string
	// Terminal says this event ends the invocation, and Failed says it ended
	// badly. A provider that can nest agents emits results that are not the
	// invocation's, so which envelope is the terminal is the adapter's to decide
	// before it builds one of these -- the contract cannot identify a terminal
	// by type or by arrival order.
	Terminal bool
	Failed   bool
	// Payload is the provider's own structured payload for the event, exactly as
	// it arrived. It is carried whole rather than reduced, because a limit
	// nobody has seen the shape of cannot be diagnosed from a record that
	// already threw the shape away.
	Payload json.RawMessage
}

// Observation is what a dialect reports about one event: the answer, and the
// evidence for it. Every field but Answer is evidence carried for the record --
// none of it is a decision, and there is nowhere in it to state a duration, a
// retry count, or a budget.
type Observation struct {
	// Answer is which of the contract's answers this event is.
	Answer Answer
	// Kind is the provider's own name for the limit that was reached, carried as
	// evidence rather than interpreted. It is meaningful only on
	// AnswerLimitReached.
	Kind string
	// ResetsAt is when the provider said the condition lifts, meaningful only on
	// AnswerLimitReached. Zero means the provider named no usable reset time,
	// which is a fact about the provider rather than a wait the dialect may
	// guess at: see ReadReset for what the harness makes of it.
	ResetsAt time.Time
	// Detail is the provider's own words about the answer, bounded by whoever
	// records it. It is what turns a category shared by a transient overload and
	// a refused request into a record somebody can act on.
	Detail string
}

// Dialect turns one provider's operational vocabulary into the contract's
// answers. It is the whole of what a provider plugin is: a built-in delivers one
// as code beside its adapter, and a user-supplied one delivers it as data.
//
// Observe reports what the provider said, and reports false when the dialect has
// nothing to say about the event. Saying nothing is the safe answer and the
// common one -- most of a provider's stream is prose and tool calls -- and it
// leaves the invocation's outcome exactly where it was.
type Dialect interface {
	// Name is the dialect's own name, for the record.
	Name() string
	Observe(event ProviderEvent) (Observation, bool)
}

// ResetKind is what the harness makes of a reset time a provider named. It is
// decided here rather than in any dialect, because both of the cases that are
// not simply a time in the future were learned at the cost of a run and neither
// is a thing a plugin author should have to rediscover.
type ResetKind string

const (
	// ResetKnown is a reset time in the future, which is a deadline the harness
	// can wait on.
	ResetKnown ResetKind = "known"
	// ResetUnknown is no reset time at all. It is not fatal: the overage
	// allowance reports this way while the ordinary rolling window keeps
	// resetting on its usual schedule, so the work resumes and the harness has
	// to ask again rather than be told when. What it asks for is the caller's
	// configured recheck interval, which is the caller's to state.
	ResetUnknown ResetKind = "unknown"
	// ResetMalformed is a reset time that is not in the future while the limit
	// is still refusing work. It is not describing a wait: honoring it would
	// mean reissuing immediately into the same refusal with nothing bounding the
	// attempts. A clock skew, or a window the provider has not rolled yet, is a
	// fact for a person rather than something to spin on.
	ResetMalformed ResetKind = "malformed"
)

// ReadReset says what the harness makes of a reset time. It is the contract's
// answer to the two hard cases and the single place either is decided, so a
// dialect nobody here wrote inherits both rather than getting them wrong
// privately.
func ReadReset(resetsAt time.Time, now time.Time) ResetKind {
	switch {
	case resetsAt.IsZero():
		return ResetUnknown
	case !resetsAt.After(now):
		return ResetMalformed
	default:
		return ResetKnown
	}
}

// Record writes one observation onto the invocation's result. It is where the
// contract's answers become the refusals the rest of the harness already reads,
// and it lives here rather than in any adapter so that the rules about which
// refusals may stand together are held once.
//
// Two of those rules are worth naming. A later report that a limit is serving
// again supersedes an earlier exhausted one rather than accumulating beside it,
// because a provider re-reports its limits as they change and only the last word
// describes the account now. And an overload is never also reported as a death
// to relaunch on: the caller already has a wait for it, and a result carrying
// both would leave which answer a run took depending on the order the caller
// read them.
func (o Observation) Record(result *RunResult) {
	if result == nil {
		return
	}
	switch o.Answer {
	case AnswerServed:
		result.UsageLimit = nil
	case AnswerRetrying:
		// The provider is still working on the attempt. It says nothing about
		// capacity and nothing about the outcome, so nothing here changes.
	case AnswerLimitReached:
		result.UsageLimit = &UsageLimit{Kind: o.Kind, ResetsAt: o.ResetsAt}
	case AnswerUnavailable:
		result.ServerOverload = &ServerOverload{Detail: o.Detail}
		result.TransientFailure = nil
	case AnswerInterrupted:
		if result.ServerOverload == nil {
			result.TransientFailure = &TransientFailure{Detail: o.Detail}
		}
	case AnswerRefused:
		// A refusal that stands is the ordinary failure the result already
		// carries. It clears the two transient readings so that a dialect
		// reporting one and then the other leaves the last word standing, and it
		// leaves any limit alone: a limit reported beside a failed attempt is
		// exactly the refusal the caller waits on.
		result.ServerOverload = nil
		result.TransientFailure = nil
	}
}
