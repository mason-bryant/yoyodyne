package claudecode

// Claude Code's operational dialect: how this provider says it is throttled, how
// it says it is exhausted, how to tell those apart, and when to come back.
//
// All of it used to be spread through the parser, which is what made it look
// general when it never was. It is one implementation of the provider contract
// in internal/backend now, and nothing above it special-cases this provider: the
// parser hands the dialect an event, the dialect answers with one of the
// contract's answers, and the harness decides what waiting -- if any -- that
// answer earns. The dialect states no duration anywhere, which is the property
// that keeps a provider from being able to spend an account.

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// The provider's own names for the events this dialect reads. Everything else in
// the stream is prose, tool calls, and accounting, and the dialect says nothing
// about any of it.
const (
	rateLimitEventType = "rate_limit_event"
	systemEventType    = "system"
	// apiRetrySubtype is the provider's own transient retry, which it is
	// handling itself. The attempt has not ended and nothing about the account
	// is exhausted, so it is evidence rather than a reason for the harness to
	// wait.
	apiRetrySubtype = "api_retry"
)

// terminalAPIError is the provider's own terminal reason for an invocation that
// ended on an error from the API rather than on anything the agent did or the
// harness decided.
const terminalAPIError = "api_error"

// overloadedStatus is the HTTP status the provider's API answers with when it is
// transiently unable to serve a request at all.
const overloadedStatus = "529"

// clientErrorPrefix marks the API statuses that describe the request rather than
// the server's ability to serve it. A relaunch would put the identical request in
// front of the provider again and earn the identical refusal, so nothing in this
// class is transient — an exhausted key and a malformed request both stay what
// they are however many times they are asked.
const clientErrorPrefix = "4"

// apiErrorStatus reads the status out of the message the provider CLI writes on
// a terminal API error, which it puts at the front, before its own prose.
//
// Provenance. Two runs on 2026-08-18 — run-ff3c59bff086d6ac16dbf5101778843d and
// run-19dc9dff153e1eb89a2470f78f02f240 — recorded a terminal result byte for
// byte identical apart from the session, with terminal_reason "api_error" and
// this result text:
//
//	API Error: 529 Overloaded. This is a server-side issue, usually temporary —
//	try again in a moment. If it persists, check https://status.claude.com.
//
// Both had already exhausted the CLI's own ten api_retry attempts on the same
// condition, so a terminal result in this shape is what the provider says after
// it has finished retrying rather than instead of retrying.
//
// The match is deliberately narrow: only a terminal API error whose status is
// the overloaded one becomes a waitable refusal, so a message this version does
// not recognize fails the run exactly as it does today rather than becoming a
// wait nobody can justify.
var apiErrorStatus = regexp.MustCompile(`(?i)\bapi error\b\D{0,4}(\d{3})\b`)

// rateLimitRejected is the provider's name for a limit that is refusing work.
// Its other statuses describe a limit that is still serving, and the transient
// throttles the provider CLI retries by itself arrive separately as the system
// api_retry subtype, so neither is a reason for the harness to wait.
const rateLimitRejected = "rejected"

// rateLimitInfo names the fields of a rate_limit_event this dialect acts on. The
// provider sends more than this — utilization, overage accounting, the reason
// overage is unavailable — and all of it is preserved in the event stream; only
// these decide whether a run waits.
//
// Provenance of these names. No recorded rate_limit_event was available to read
// them off: this parser used to route the event to its default branch, so all
// twenty entries in the local run history had already had their payload
// discarded, and a hard limit could not be provoked on demand to record a fresh
// one. They were therefore read out of the shipped provider CLI itself —
// Claude Code 2.1.224, the emitted-message schema `$1v` and the payload schema
// it references, `L1v` — which declares:
//
//	type:           "rate_limit_event"
//	rate_limit_info: status ∈ {allowed, allowed_warning, rejected}   (required)
//	                 resetsAt?: int
//	                 rateLimitType? ∈ {five_hour, seven_day, seven_day_opus,
//	                                   seven_day_sonnet,
//	                                   seven_day_overage_included, overage}
//	                 utilization?, isUsingOverage?, overageStatus?,
//	                 overageResetsAt?, overageDisabledReason?, …
//
// resetTime reads resetsAt as whole Unix seconds because that CLI compares it as
// `resetsAt * 1000 <= Date.now()` and renders it as `resetsAt - Date.now()/1000`.
// exhausted excludes isUsingOverage because that CLI shows a hard limit only
// when overage is not already serving the request.
//
// That is a description of the provider, not a promise from it. Everything here
// degrades safely if a future version disagrees: an unreadable payload is
// recorded whole and never read as exhaustion, so the harness stops waiting
// rather than starts waiting wrongly. The first real exhausted limit this
// records is the evidence that should replace this comment.
type rateLimitInfo struct {
	Status        string `json:"status"`
	RateLimitType string `json:"rateLimitType"`
	// ResetsAt stays raw so that a reset time in a shape this version does not
	// expect cannot sink the decode of the whole payload. An exhausted limit
	// whose reset time is unreadable is still an exhausted limit, and it has to
	// reach the caller as one: what the harness refuses is guessing the wait, not
	// noticing the refusal.
	ResetsAt       json.RawMessage `json:"resetsAt"`
	IsUsingOverage bool            `json:"isUsingOverage"`
}

// exhausted reports a limit that actually stops work. A rejected primary limit
// with overage already in use is still being served, which is the provider's own
// rule for whether to show the user a hard limit or leave them working.
func (r rateLimitInfo) exhausted() bool {
	return r.Status == rateLimitRejected && !r.IsUsingOverage
}

// resetTime reads the instant the limit resets. The provider sends whole
// seconds since the Unix epoch; anything else is no reset time at all, and what
// the harness makes of that is the contract's to say rather than this dialect's.
func (r rateLimitInfo) resetTime() time.Time {
	var seconds int64
	if len(r.ResetsAt) == 0 || json.Unmarshal(r.ResetsAt, &seconds) != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// Dialect reads Claude Code. It is stateless and carries no clock, because
// nothing it answers depends on either: it says what the provider said, and
// every judgement about time belongs to the caller.
type Dialect struct{}

var _ backend.Dialect = Dialect{}

func (Dialect) Name() string { return domainBackend }

// Observe reports what one event from this provider says about the attempt.
//
// A terminal that succeeded is deliberately not an answer. It says nothing about
// capacity — the provider reports its limits on their own event, and re-reports
// them as they change — so reading a completed invocation as evidence that a
// limit has lifted would be this dialect inventing a fact the provider never
// stated.
func (Dialect) Observe(event backend.ProviderEvent) (backend.Observation, bool) {
	switch {
	case event.Type == rateLimitEventType:
		return observeRateLimit(event.Payload)
	case event.Type == systemEventType && event.Subtype == apiRetrySubtype:
		return backend.Observation{Answer: backend.AnswerRetrying}, true
	case event.Terminal && event.Failed:
		return observeFailedTerminal(event)
	default:
		return backend.Observation{}, false
	}
}

// observeRateLimit reads what the provider said about capacity. A payload this
// dialect cannot read says nothing: it must not be read as exhaustion, because
// the same event reports healthy utilization far more often and reading that as
// exhaustion would stop runs that have capacity.
func observeRateLimit(payload json.RawMessage) (backend.Observation, bool) {
	var info rateLimitInfo
	if len(payload) == 0 || json.Unmarshal(payload, &info) != nil {
		return backend.Observation{}, false
	}
	if !info.exhausted() {
		return backend.Observation{Answer: backend.AnswerServed}, true
	}
	return backend.Observation{
		Answer:   backend.AnswerLimitReached,
		Kind:     info.RateLimitType,
		ResetsAt: info.resetTime(),
	}, true
}

// observeFailedTerminal tells the three ways this provider can end an invocation
// badly apart. A server overload is a wait; a status describing the request is a
// refusal that stands; everything else left in the API-error category is a death
// that judged nothing about the work.
//
// The two are matched from opposite directions on purpose. The overload turns a
// failure into a wait, so a message this version does not recognize has to keep
// failing the run. The transient death turns a failure into another attempt
// against a budget, so the cost of being wrong is one more invocation and a
// blocker that arrives later than it might have — while the cost of missing a
// case is the whole run, which is what a person spent a week reconciling by
// hand. It is therefore the leftovers that are read as transient, and being
// narrow there is the safe direction only if the harness is content to keep
// failing on weather it has not seen yet, and it is not.
func observeFailedTerminal(event backend.ProviderEvent) (backend.Observation, bool) {
	described := backend.DescribeFailure(event.Subtype, event.Text)
	if event.Subtype != terminalAPIError {
		return backend.Observation{Answer: backend.AnswerRefused, Detail: described}, true
	}
	status := apiErrorStatus.FindStringSubmatch(event.Text)
	switch {
	case status != nil && status[1] == overloadedStatus:
		// The provider's own message is carried whole here rather than folded:
		// it is the only evidence of an overload, and it is short by
		// construction.
		return backend.Observation{Answer: backend.AnswerUnavailable, Detail: event.Text}, true
	case status != nil && strings.HasPrefix(status[1], clientErrorPrefix):
		return backend.Observation{Answer: backend.AnswerRefused, Detail: described}, true
	default:
		// The shape of a connection that went away, among others: "Connection
		// closed mid-response" quotes no status because nothing answered.
		return backend.Observation{Answer: backend.AnswerInterrupted, Detail: described}, true
	}
}
