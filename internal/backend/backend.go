package backend

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

type Capabilities struct {
	StructuredEvents  bool `json:"structured_events"`
	SessionResumption bool `json:"session_resumption"`
	StructuredOutput  bool `json:"structured_output"`
	ToolControl       bool `json:"tool_control"`
	LocalAuth         bool `json:"local_auth"`
}

type Availability struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
	APIProvider   string `json:"api_provider,omitempty"`
}

type RunRequest struct {
	RunID            string
	Role             domain.AgentRole
	WorkingDirectory string
	Prompt           string
	SystemPrompt     string
	SessionID        string
	Model            string
	PermissionMode   string
	AllowedTools     []string
	// Timeout is the total budget for the invocation and IdleTimeout is how long
	// it may go without emitting an event. They answer different questions -- is
	// this run worth continuing, and is it doing anything at all -- so a backend
	// applies both rather than letting either stand in for the other. Zero means
	// the backend's default for each.
	Timeout      time.Duration
	IdleTimeout  time.Duration
	LastSequence uint64
	RedactValues []string
	EventSink    func(execution.Event) error
	// ReplySink receives the agent's prose as the provider produces it, so a
	// caller with somebody watching can show a reply forming rather than holding
	// it until the invocation is over. It is optional and it decides nothing:
	// the same text reaches the event stream and the result whether or not
	// anybody is listening, so a run watched and a run unwatched record and
	// return byte for byte the same thing.
	//
	// A backend calls it only with text it has already redacted and already
	// recorded, so what a watcher can be shown is bounded by what the durable
	// record holds. Fragments are whatever size the provider reports; a caller
	// that shows them has to cope with a reply that stops part way, because an
	// invocation that fails is one whose fragments were the start of an answer
	// nobody finished.
	ReplySink func(fragment string)
}

// UsageLimit is a provider's report that a usage limit is exhausted. It is
// deliberately not a failure: the work was never judged, only declined for want
// of capacity, so what it calls for is a wait rather than a failure record. A
// transient throttle never produces one of these — the provider CLI retries
// those itself and reports them as its own api_retry event.
type UsageLimit struct {
	// Kind is the provider's own name for the exhausted limit, carried as
	// evidence rather than interpreted by the harness.
	Kind string
	// ResetsAt is when the provider said the limit resets. It is zero when the
	// provider named no usable reset time, which is not a wait a caller may
	// guess at.
	ResetsAt time.Time
}

// ServerOverload is a provider's report that its own servers could not serve the
// attempt. Like UsageLimit it is deliberately not a failure: the work was never
// judged, only refused, and the provider's own message says the condition is
// temporary. It names no reset time, because a server under load never quotes
// one, so what it calls for is a short wait and another attempt rather than a
// deadline to sleep on.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A transient server refusal is a fourth answer that contract has to name,
// beside the ordinary retry a provider takes itself, the limit that will lift,
// and the refusal that will not. It is not a variant of any of them: nothing
// about the account is exhausted, so it is not a usage limit; the provider has
// already stopped retrying by the time it says this, so it is not an ordinary
// retry; and it is explicitly temporary, so it is not a refusal that stands.
// What makes it a distinct answer operationally is that it carries no reset
// time and never will, so the contract cannot fold it into the unknown-reset
// case: that case polls on the interval an exhausted limit deserves, and an
// overload wants one two orders of magnitude shorter.
//
// The evidence, so a plugin author has the shape rather than the assertion. Two
// runs on 2026-08-18, run-ff3c59bff086d6ac16dbf5101778843d and
// run-19dc9dff153e1eb89a2470f78f02f240, each failed a whole run instantly on a
// terminal result identical apart from the session: terminal_reason "api_error"
// and the text
//
//	API Error: 529 Overloaded. This is a server-side issue, usually temporary —
//	try again in a moment. If it persists, check https://status.claude.com.
//
// Both had already exhausted the provider CLI's own ten api_retry attempts on
// error "overloaded" before emitting it. That is Claude Code's spelling of the
// answer and belongs behind the contract rather than in it; how the Claude Code
// adapter reads it is in that adapter's parser. What generalizes is the answer
// itself, and that the harness — never the plugin — decides how long to wait
// for it and against which budget.
type ServerOverload struct {
	// Detail is the provider's own message, carried as evidence rather than
	// interpreted by the harness.
	Detail string
}

// TransientFailure is a provider invocation that died of something that judged
// nothing about the work: the API answered with an error the provider's own
// retries did not outlast, or the connection carrying the response went away
// before the invocation reached a terminal at all.
//
// It is not a refusal the way UsageLimit and ServerOverload are. Those two say
// in advance that the attempt was never served and name the condition; this one
// is the provider ending an invocation badly with nothing to say about why it
// would go better next time. What makes it the same kind of answer operationally
// is what it leaves behind: a change part-made in a worktree and a session that
// can still be continued, and no verdict on any of it. So it asks the harness to
// ask again against a budget it keeps, rather than to wait on a clock it does
// not have.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A transient death is a fifth answer that contract has to name. It is not the
// ordinary retry a provider takes itself, because the provider has already
// stopped by the time it says this; not a usage limit, because nothing about the
// account is exhausted; not an overload, because no condition is named that will
// lift; and not a refusal that stands, because the same request may well be
// served on the next attempt. A contract that folded it into the last of those
// would fail a whole run on weather, which is what this harness did until
// yoyodyne-ifd.101.
//
// The evidence, so a plugin author has the shape rather than the assertion. Run
// run-80f99e14210681e07bb07722f1b91483, developing yoyodyne-ifd.68.2 on
// 2026-08-19, ended on terminal_reason "api_error" with the text
//
//	API Error: Connection closed mid-response. The response above may be
//	incomplete.
//
// which names no HTTP status because nothing answered — the transport went away
// mid-reply. An earlier run, run-77cd11f0f96309286ddf3d2d2449ca26, recorded the
// bare category "api_error" and nothing else, because its record predates the
// harness keeping the provider's message beside it. That it can no longer be told
// apart from any other member of the category is part of the point: the category
// alone does not say what happened, so a harness that reads every member of it as
// a judgement of the work is guessing.
type TransientFailure struct {
	// Detail is the provider's own account of the death, carried as evidence
	// rather than interpreted by the harness. It is bounded, because it is what a
	// durable failure record and a blocker on a work item both keep.
	Detail string
}

// RunResult is what one provider invocation is worth: the invocation's own
// terminal, and nothing nested inside it.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A provider whose agents can spawn agents emits results that are not the run's.
// Claude Code spells this as a second envelope of the terminal type carrying
// neither a terminal reason nor result text — run
// run-841f5ee1866addb533c02a30e67f001a, sequence 1186, is the recorded specimen,
// and how that adapter tells the two apart is in its parser. What generalizes is
// that the terminal cannot be identified by envelope type or by arrival order,
// because a nested agent may finish before the parent and one that finishes
// after is not a duplicate; a plugin has to say which result is the
// invocation's, and the contract has to make that answerable rather than assume
// exactly one result arrives.
type RunResult struct {
	Backend   domain.Backend
	SessionID string
	// ResolvedModel is the model the provider reported actually serving the
	// invocation. A requested selector may be a floating family alias, so the
	// resolved identifier is the only durable evidence of what really ran.
	ResolvedModel string
	FinalText     string
	IsError       bool
	CostUSD       float64
	Usage         []byte
	Process       execution.ProcessResult
	LastEvent     uint64
	StopReason    string
	// UsageLimit is set when the provider reported an exhausted usage limit
	// during this invocation. It is separate from IsError because the two call
	// for opposite responses: a failure ends the run, an exhausted limit asks
	// the caller to wait and ask again.
	UsageLimit *UsageLimit
	// ServerOverload is set when the invocation ended because the provider's
	// servers were transiently unable to serve it. It travels beside IsError
	// rather than instead of it — the provider does report this as a terminal
	// error — and it asks the same thing of the caller an exhausted limit does:
	// wait and ask again rather than end the run.
	ServerOverload *ServerOverload
	// TransientFailure is set when the invocation died of something that judged
	// nothing about the work and may not happen again. It travels beside IsError
	// like an overload does, and it is never set alongside one: an overload is
	// the transient death the caller already has a wait for, so a backend that
	// reported both would leave which answer a run took depending on the order
	// the caller read them.
	TransientFailure *TransientFailure
}

// maxFailureDetailBytes bounds the provider's own words in a described failure.
// What the bound cuts is the tail of a message; nothing that identifies the
// failure is at the end of one, and the whole of it is in the invocation's event
// log either way.
const maxFailureDetailBytes = 512

// DescribeFailure says why a provider ended an invocation badly, in its own name
// for the ending and its own words about it. It lives on the result rather than
// at any one caller because every role's invocation dies the same way and the
// description of it outlives them all: a developer's death becomes the run's
// durable failure, a reviewer's ends the run that asked for it, and a
// conversation turn's is what an operator is shown.
//
// The name alone is a category -- `api_error` covers a transient 529 and a
// refused request alike -- and it is what a run's durable failure keeps, so a
// record carrying nothing else leaves whoever reads it afterwards with no idea
// which of them happened. The message is added when it says something the
// category does not, so a provider that only names the category still reads as
// one reason rather than as the same words twice.
func (r RunResult) DescribeFailure() string {
	reason := strings.TrimSpace(r.StopReason)
	detail := boundFailureDetail(r.FinalText)
	switch {
	case reason == "" && detail == "":
		return "unknown provider failure"
	case reason == "" || reason == detail:
		return firstOf(reason, detail)
	case detail == "" || strings.Contains(detail, reason):
		return firstOf(detail, reason)
	default:
		return reason + ": " + detail
	}
}

// boundFailureDetail folds the provider's message into one bounded line, so a
// final reply that runs to pages becomes a reason somebody can read rather than
// the body of a work item note.
// boundFailureDetail and internal/cli's singleLine share the same
// walk-back-to-a-rune-start fold with different bounds (512 bytes here, 160
// there); a fix to either almost certainly belongs in both.
func boundFailureDetail(detail string) string {
	folded := strings.Join(strings.Fields(detail), " ")
	if len(folded) <= maxFailureDetailBytes {
		return folded
	}
	cut := maxFailureDetailBytes
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type Backend interface {
	CheckAvailability(ctx context.Context) (Availability, error)
	Capabilities() Capabilities
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}
