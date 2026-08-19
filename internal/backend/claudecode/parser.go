package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const maxEventTextBytes = 16 << 10

type streamParser struct {
	runID    string
	sequence *execution.Sequence
	clock    execution.Clock
	redactor execution.Redactor
	sink     func(execution.Event) error
	// reply is where the agent's prose goes for whoever is watching it arrive.
	// It is nil when nobody is, which is every invocation the harness makes on
	// its own behalf.
	reply     func(string)
	result    backend.RunResult
	sawResult bool
}

type streamEnvelope struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	SessionID      string          `json:"session_id"`
	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	StopReason     string          `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	Usage          json.RawMessage `json:"usage"`
	Message        json.RawMessage `json:"message"`
	Model          string          `json:"model"`
	PermissionMode string          `json:"permissionMode"`
	Tools          []string        `json:"tools"`
	Capabilities   []string        `json:"capabilities"`
	Attempt        int             `json:"attempt"`
	MaxRetries     int             `json:"max_retries"`
	Error          string          `json:"error"`
	Output         string          `json:"output"`
	// RateLimitInfo is the payload of a rate_limit_event, kept whole rather than
	// reduced to the few fields the harness reads from it. It is the only
	// evidence anybody has of what the provider says when capacity runs out, and
	// discarding the parts this version does not act on is what made the
	// question unanswerable the last time it was asked.
	RateLimitInfo json.RawMessage `json:"rate_limit_info"`
}

// rateLimitInfo names the fields of a rate_limit_event the harness acts on. The
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

// terminalAPIError is the provider's own terminal reason for an invocation that
// ended on an error from the API rather than on anything the agent did or the
// harness decided.
const terminalAPIError = "api_error"

// overloadedStatus is the HTTP status the provider's API answers with when it is
// transiently unable to serve a request at all.
const overloadedStatus = "529"

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
//
// All of this is Claude Code's dialect, and it is one more piece of it living
// here rather than behind a contract. What generalizes out of it — that a
// transient server refusal is an answer a provider contract has to name, and
// why it cannot be folded into the unknown-reset case — is stated where the
// contract lives, on backend.ServerOverload, for yoyodyne-ifd.32 to build on.
var apiErrorStatus = regexp.MustCompile(`(?i)\bapi error\b\D{0,4}(\d{3})\b`)

// transientServerOverload reports an invocation the provider ended because its
// own servers could not serve it. It is not a usage limit — nothing about the
// account is exhausted, and no reset time is quoted — but it asks for the same
// answer: wait briefly and ask again.
func transientServerOverload(result backend.RunResult) *backend.ServerOverload {
	if !result.IsError || result.StopReason != terminalAPIError {
		return nil
	}
	status := apiErrorStatus.FindStringSubmatch(result.FinalText)
	if status == nil || status[1] != overloadedStatus {
		return nil
	}
	return &backend.ServerOverload{Detail: result.FinalText}
}

// clientErrorPrefix marks the API statuses that describe the request rather than
// the server's ability to serve it. A relaunch would put the identical request in
// front of the provider again and earn the identical refusal, so nothing in this
// class is transient — an exhausted key and a malformed request both stay what
// they are however many times they are asked.
const clientErrorPrefix = "4"

// transientProviderFailure reports an invocation the provider ended on something
// that judged nothing about the work and may well not happen again. It is what is
// left of the terminal API errors once the narrower readings have taken theirs:
// an overload is already a waitable refusal and never reaches here, and a status
// describing the request is a refusal that stands. What remains is a server-side
// status that named no wait, or a terminal API error carrying no status at all —
// the shape of a connection that went away, since "Connection closed
// mid-response" quotes no status because nothing answered.
//
// Reading the leftovers as transient rather than the recognized ones as such is
// deliberate, and it is the opposite of how the overload above is matched. That
// one turns a failure into a wait, so a message it does not recognize has to keep
// failing the run. This one turns a failure into another attempt against a
// budget, so the cost of being wrong is one more invocation and a blocker that
// arrives three attempts later than it might have — while the cost of missing a
// case is the whole run, which is what a person spent this week reconciling by
// hand. Being narrow is the safe direction there only if the harness is content
// to keep failing on weather it has not seen yet, and it is not.
//
// Claude Code's dialect again: what generalizes is on backend.TransientFailure.
func transientProviderFailure(result backend.RunResult) *backend.TransientFailure {
	if !result.IsError || result.StopReason != terminalAPIError {
		return nil
	}
	if status := apiErrorStatus.FindStringSubmatch(result.FinalText); status != nil && strings.HasPrefix(status[1], clientErrorPrefix) {
		return nil
	}
	// The provider's own account of the death, bounded: the category alone does
	// not say what happened, and the message beside it is the difference between
	// a record somebody can act on and three runs that all read "api_error".
	return &backend.TransientFailure{Detail: result.DescribeFailure()}
}

// rateLimitRejected is the provider's name for a limit that is refusing work.
// Its other statuses describe a limit that is still serving, and the transient
// throttles the provider CLI retries by itself arrive separately as the system
// api_retry subtype, so neither is a reason for the harness to wait.
const rateLimitRejected = "rejected"

// exhausted reports a limit that actually stops work. A rejected primary limit
// with overage already in use is still being served, which is the provider's own
// rule for whether to show the user a hard limit or leave them working.
func (r rateLimitInfo) exhausted() bool {
	return r.Status == rateLimitRejected && !r.IsUsingOverage
}

// resetTime reads the instant the limit resets. The provider sends whole
// seconds since the Unix epoch; anything else is no reset time at all, and the
// caller refuses to wait rather than inventing one.
func (r rateLimitInfo) resetTime() time.Time {
	var seconds int64
	if len(r.ResetsAt) == 0 || json.Unmarshal(r.ResetsAt, &seconds) != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

type message struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func newStreamParser(runID string, lastSequence uint64, clock execution.Clock, redactor execution.Redactor, sink func(execution.Event) error, reply func(string)) *streamParser {
	return &streamParser{
		runID:    runID,
		sequence: execution.NewSequence(lastSequence),
		clock:    clock,
		redactor: redactor,
		sink:     sink,
		reply:    reply,
	}
}

func (p *streamParser) ParseLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	var envelope streamEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return fmt.Errorf("decode stream event: %w", err)
	}
	if envelope.Type == "" {
		return errors.New("decode stream event: type is required")
	}
	if err := p.redactEnvelope(&envelope); err != nil {
		return err
	}
	// Not every result envelope is the invocation's result. A nested agent ends
	// in one too, so the guard below is about the terminal rather than about the
	// envelope type: a nested result is recorded as the stream noise it is and
	// never counts as a terminal at all.
	if envelope.Type == "result" && nestedAgentResult(envelope) {
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"is_error":         envelope.IsError,
			"total_cost_usd":   envelope.TotalCostUSD,
			"usage":            json.RawMessage(envelope.Usage),
		})
	}
	// A terminal result decides the run's outcome, but the provider keeps
	// writing after it. Trailing events are recorded so their payload stays
	// diagnosable; they must not disturb the decided result, and the guarded
	// invariant is that a second terminal result cannot replace the first.
	if p.sawResult && envelope.Type == "result" {
		return errors.New("second provider terminal result received after terminal result")
	}
	if !p.sawResult && envelope.SessionID != "" {
		p.result.SessionID = envelope.SessionID
	}

	switch envelope.Type {
	case "system":
		return p.parseSystem(envelope)
	case "assistant", "user":
		return p.parseMessage(envelope.Type, envelope.Message)
	case "result":
		return p.parseResult(envelope)
	case "rate_limit_event":
		return p.parseRateLimit(envelope)
	default:
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
		})
	}
}

func (p *streamParser) EmitProcessOutput(output execution.Output) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream": output.Stream,
		"text":   truncate(p.redactor.Redact(output.Text)),
	})
}

func (p *streamParser) parseSystem(envelope streamEnvelope) error {
	switch envelope.Subtype {
	case "init":
		// The init event is where the provider names the model it resolved the
		// requested selector to. It is recorded as first-class result evidence
		// rather than left buried in the event payload.
		if !p.sawResult && envelope.Model != "" {
			p.result.ResolvedModel = envelope.Model
		}
		return p.emit(execution.EventRunStarted, map[string]any{
			"session_id":      envelope.SessionID,
			"model":           envelope.Model,
			"permission_mode": envelope.PermissionMode,
			"tools":           envelope.Tools,
			"capabilities":    envelope.Capabilities,
		})
	case "api_retry":
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"attempt":          envelope.Attempt,
			"max_retries":      envelope.MaxRetries,
			"error":            envelope.Error,
		})
	default:
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"error":            truncate(envelope.Error),
			"output":           truncate(envelope.Output),
		})
	}
}

// parseRateLimit records what the provider said about capacity. The whole
// payload goes into the event stream, not the handful of fields this version
// reads from it: a limit nobody has seen the shape of cannot be diagnosed from
// an event that already threw the shape away. Only an exhausted limit becomes a
// result the run acts on — the same event reports healthy utilization far more
// often, and reading that as exhaustion would stop runs that have capacity.
func (p *streamParser) parseRateLimit(envelope streamEnvelope) error {
	if err := p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type":   envelope.Type,
		"rate_limit_info": json.RawMessage(envelope.RateLimitInfo),
	}); err != nil {
		return err
	}
	var info rateLimitInfo
	if len(envelope.RateLimitInfo) == 0 || json.Unmarshal(envelope.RateLimitInfo, &info) != nil {
		// A payload the harness cannot read is already recorded above. It says
		// nothing about capacity, so it must not be read as exhaustion, and a
		// malformed report is not a reason to fail the stream either.
		return nil
	}
	if !info.exhausted() {
		// The provider re-reports as the limit changes, so a later report that
		// the limit is serving again supersedes an earlier exhausted one rather
		// than accumulating beside it.
		p.result.UsageLimit = nil
		return nil
	}
	p.result.UsageLimit = &backend.UsageLimit{Kind: info.RateLimitType, ResetsAt: info.resetTime()}
	return nil
}

func (p *streamParser) parseMessage(messageType string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s event is missing message", messageType)
	}
	var value message
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s message: %w", messageType, err)
	}
	for _, block := range value.Content {
		block.Text = p.redactor.Redact(block.Text)
		block.ID = p.redactor.Redact(block.ID)
		block.Name = p.redactor.Redact(block.Name)
		block.ToolUseID = p.redactor.Redact(block.ToolUseID)
		switch block.Type {
		case "text":
			if messageType == "assistant" {
				if !p.sawResult {
					p.result.FinalText = block.Text
				}
				if err := p.emit(execution.EventAgentMessage, map[string]any{"text": truncate(block.Text)}); err != nil {
					return err
				}
				// The prose reaches a watcher after the event that records it and
				// after the redaction above, in that order and never the other:
				// nothing may be shown that the record does not hold, and nothing
				// may be shown before it has been redacted. It is handed over whole
				// rather than truncated the way the event is — a bound that exists
				// to keep the event log readable is not a reason to show the
				// operator half of what they were told.
				if p.reply != nil {
					p.reply(block.Text)
				}
			}
		case "tool_use":
			if err := p.emit(execution.EventCommandStarted, map[string]any{
				"tool_use_id": block.ID,
				"tool":        block.Name,
				"input_bytes": len(block.Input),
			}); err != nil {
				return err
			}
		case "tool_result":
			if err := p.emit(execution.EventCommandCompleted, map[string]any{
				"tool_use_id":   block.ToolUseID,
				"is_error":      block.IsError,
				"content_bytes": len(block.Content),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *streamParser) redactEnvelope(envelope *streamEnvelope) error {
	// Type and subtype are provider control enums used for dispatch, not
	// provider-authored text. Redacting them could corrupt parsing if a poorly
	// chosen credential happened to equal an enum such as "result".
	envelope.SessionID = p.redactor.Redact(envelope.SessionID)
	envelope.Result = p.redactor.Redact(envelope.Result)
	envelope.StopReason = p.redactor.Redact(envelope.StopReason)
	envelope.TerminalReason = p.redactor.Redact(envelope.TerminalReason)
	envelope.Model = p.redactor.Redact(envelope.Model)
	envelope.PermissionMode = p.redactor.Redact(envelope.PermissionMode)
	envelope.Error = p.redactor.Redact(envelope.Error)
	envelope.Output = p.redactor.Redact(envelope.Output)
	for index := range envelope.Tools {
		envelope.Tools[index] = p.redactor.Redact(envelope.Tools[index])
	}
	for index := range envelope.Capabilities {
		envelope.Capabilities[index] = p.redactor.Redact(envelope.Capabilities[index])
	}
	usage, err := p.redactJSONStrings(envelope.Usage)
	if err != nil {
		return fmt.Errorf("redact provider usage: %w", err)
	}
	envelope.Usage = usage
	rateLimit, err := p.redactJSONStrings(envelope.RateLimitInfo)
	if err != nil {
		return fmt.Errorf("redact provider rate limit: %w", err)
	}
	envelope.RateLimitInfo = rateLimit
	return nil
}

func (p *streamParser) redactJSONStrings(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value = redactJSONValue(value, p.redactor)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func redactJSONValue(value any, redactor execution.Redactor) any {
	switch typed := value.(type) {
	case string:
		return redactor.Redact(typed)
	case []any:
		for index := range typed {
			typed[index] = redactJSONValue(typed[index], redactor)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = redactJSONValue(item, redactor)
		}
		return typed
	default:
		return value
	}
}

// nestedAgentResult reports a result envelope that ends something inside the
// invocation rather than the invocation itself. An agent may spawn subagents,
// and a subagent's completion arrives in the parent's stream as another
// type:result envelope; nothing in it says whose result it is, so the two are
// told apart by shape.
//
// Provenance. Run run-841f5ee1866addb533c02a30e67f001a, developing
// yoyodyne-ifd.60 on 2026-08-18, recorded one at sequence 1186 while its
// developer had subagents outstanding: is_error false, empty result, empty
// terminal_reason, no cost, and an all-zero usage object, arriving sixteen
// milliseconds after a system init and sixteen before another. It is the only
// envelope of that shape in the local run history; the 218 genuine terminals
// beside it all carry a terminal_reason and result text. The parser of the day
// read it as this invocation's terminal, so the real terminal three minutes
// later tripped the one-terminal guard and killed a run whose work was already
// complete in the worktree.
//
// The test is deliberately narrow, because both mistakes cost the same thing —
// a nested result read as terminal loses the real outcome, and so does a
// terminal read as nested — and only one of them has ever been observed. An
// envelope carrying either mark is therefore still the terminal it has always
// been, and only one carrying neither is noise.
//
// This is Claude Code's dialect like the rest of this file, and it is one more
// thing yoyodyne-ifd.32 has to name: what generalizes is that a provider which
// can nest agents emits results that are not the run's, and that a contract
// which identifies the terminal by envelope type alone cannot survive one.
func nestedAgentResult(envelope streamEnvelope) bool {
	return envelope.TerminalReason == "" && envelope.Result == ""
}

func (p *streamParser) parseResult(envelope streamEnvelope) error {
	p.sawResult = true
	p.result.SessionID = envelope.SessionID
	// A terminal result names the model only when no init event did; the model
	// the run started on stays authoritative.
	if p.result.ResolvedModel == "" && envelope.Model != "" {
		p.result.ResolvedModel = envelope.Model
	}
	p.result.FinalText = envelope.Result
	p.result.IsError = envelope.IsError
	p.result.CostUSD = envelope.TotalCostUSD
	p.result.Usage = append([]byte(nil), envelope.Usage...)
	p.result.StopReason = envelope.TerminalReason
	if p.result.StopReason == "" {
		p.result.StopReason = envelope.StopReason
	}
	p.result.ServerOverload = transientServerOverload(p.result)
	// An overload is the transient death the harness already has a wait for, so
	// it is never also reported as one to relaunch on. Deciding it here rather
	// than inside the classifier keeps the two answers mutually exclusive by
	// construction instead of by each one remembering the other.
	if p.result.ServerOverload == nil {
		p.result.TransientFailure = transientProviderFailure(p.result)
	}
	eventType := execution.EventRunCompleted
	if envelope.IsError {
		eventType = execution.EventRunFailed
	}
	return p.emit(eventType, map[string]any{
		"session_id":      envelope.SessionID,
		"is_error":        envelope.IsError,
		"result":          truncate(envelope.Result),
		"total_cost_usd":  envelope.TotalCostUSD,
		"usage":           json.RawMessage(envelope.Usage),
		"terminal_reason": envelope.TerminalReason,
	})
}

func (p *streamParser) emit(eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(p.runID, p.sequence.Next(), p.clock.Now(), eventType, string(domainBackend), payload)
	if err != nil {
		return err
	}
	p.result.LastEvent = event.Sequence
	if p.sink != nil {
		if err := p.sink(event); err != nil {
			return fmt.Errorf("persist normalized event: %w", err)
		}
	}
	return nil
}

func (p *streamParser) Result() backend.RunResult {
	return p.result
}

func (p *streamParser) SawResult() bool {
	return p.sawResult
}

const domainBackend = "claude-code"

func truncate(value string) string {
	if len(value) <= maxEventTextBytes {
		return value
	}
	return value[:maxEventTextBytes] + "…[truncated]"
}
