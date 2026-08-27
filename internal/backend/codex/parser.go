package codex

// Reading one `codex exec --json` invocation: which line is the invocation's
// terminal, what goes into the event log, and what the invocation's result is.
// What each thing the provider said *means* is the dialect's, and what to do
// about it is the harness's.
//
// The vocabulary below is Codex's own non-interactive event protocol: each line
// is an envelope carrying an event under `msg`, and the event names itself with
// a `type`. No recorded Codex stream existed in this repository when this was
// written, so the names are read from the provider's documented protocol rather
// than off a run, and everything here degrades in the safe direction if a future
// version disagrees: an event this parser does not recognize is recorded whole
// and read as nothing, and an invocation whose terminal never arrives fails with
// exactly that reason rather than with an outcome nobody produced. The first
// real Codex stream this repository records is the evidence that should replace
// this paragraph.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const maxEventTextBytes = 16 << 10

// sourceName is what this adapter's normalized events are recorded as, and what
// its dialect calls itself.
const sourceName = "codex"

// The rest of the provider's event names this parser reads. The three the
// dialect also reads are named beside it, because what they mean is its answer
// rather than this parser's.
const (
	eventSessionConfigured = "session_configured"
	eventAgentMessage      = "agent_message"
	eventExecCommandBegin  = "exec_command_begin"
	eventExecCommandEnd    = "exec_command_end"
	eventMCPToolCallBegin  = "mcp_tool_call_begin"
	eventMCPToolCallEnd    = "mcp_tool_call_end"
	eventPatchApplyBegin   = "patch_apply_begin"
	eventPatchApplyEnd     = "patch_apply_end"
	eventTokenCount        = "token_count"
)

// streamedFragment names the events that carry a piece of something the provider
// also sends whole. They are dropped rather than recorded: a delta stream is the
// same text again in hundreds of pieces, and an event log holding both says
// nothing extra while being far harder to read.
func streamedFragment(eventType string) bool {
	return strings.HasSuffix(eventType, "_delta") ||
		eventType == "agent_reasoning_raw_content" ||
		eventType == "agent_reasoning_section_break"
}

// streamEnvelope is one line of the provider's stream. Only the event itself is
// read: the submission identifier beside it correlates a line with a request the
// harness never makes more than one of.
type streamEnvelope struct {
	Msg json.RawMessage `json:"msg"`
}

// providerMessage is one event, reduced to the fields this adapter reads. The
// provider sends more than this on several of them, and what is not read here is
// still recorded: the raw line reaches the event log whenever this parser has
// nothing better to say about it.
type providerMessage struct {
	Type string `json:"type"`
	// session_configured names the session a later invocation resumes and the
	// model the provider resolved the requested selector to.
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	// agent_message, error, and stream_error each carry their prose here.
	Message string `json:"message"`
	// task_complete carries the agent's last message, which is the invocation's
	// answer.
	LastAgentMessage string `json:"last_agent_message"`
	// The shell, MCP, and patch calls each identify themselves with a call id and
	// report how they ended.
	CallID   string          `json:"call_id"`
	Command  []string        `json:"command"`
	Server   string          `json:"server"`
	Tool     string          `json:"tool"`
	ExitCode *int            `json:"exit_code"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr"`
	Success  *bool           `json:"success"`
	Changes  json.RawMessage `json:"changes"`
	// token_count reports what the invocation has read and written so far, in
	// either of the two shapes the provider has used for it.
	Info              json.RawMessage `json:"info"`
	InputTokens       *int64          `json:"input_tokens"`
	CachedInputTokens *int64          `json:"cached_input_tokens"`
	OutputTokens      *int64          `json:"output_tokens"`
}

// tokenCountInfo is the newer shape of a token_count, which nests the running
// totals under an info object.
type tokenCountInfo struct {
	TotalTokenUsage *struct {
		InputTokens       *int64 `json:"input_tokens"`
		CachedInputTokens *int64 `json:"cached_input_tokens"`
		OutputTokens      *int64 `json:"output_tokens"`
	} `json:"total_token_usage"`
}

// usage is what this invocation read and wrote, written under the names the
// harness's own price reader looks for rather than under the provider's. Codex
// calls its cached reads `cached_input_tokens`; renaming it here is what stops a
// run priced from this log counting every cache read as a fresh one. A count the
// provider did not state is left out rather than written as zero, because an
// invocation nobody has a measurement for and one measured at nothing are
// opposite facts to anything adding tokens up.
func (m providerMessage) usage() (json.RawMessage, bool) {
	input, cached, output := m.InputTokens, m.CachedInputTokens, m.OutputTokens
	if len(m.Info) > 0 {
		var info tokenCountInfo
		if json.Unmarshal(m.Info, &info) == nil && info.TotalTokenUsage != nil {
			input, cached, output = info.TotalTokenUsage.InputTokens, info.TotalTokenUsage.CachedInputTokens, info.TotalTokenUsage.OutputTokens
		}
	}
	if input == nil && cached == nil && output == nil {
		return nil, false
	}
	counts := map[string]int64{}
	if input != nil {
		counts["input_tokens"] = *input
	}
	if output != nil {
		counts["output_tokens"] = *output
	}
	if cached != nil {
		counts["cache_read_input_tokens"] = *cached
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

type streamParser struct {
	runID string
	// role is who this invocation was made as, and it is written onto the
	// terminal event so the record says whose invocation it priced rather than
	// leaving that to be inferred from where the terminal sits in the log.
	role     domain.AgentRole
	sequence *execution.Sequence
	clock    execution.Clock
	redactor execution.Redactor
	sink     func(execution.Event) error
	// dialect is how what this provider says is read. It is a field rather than a
	// package-level call so the parser depends on the contract rather than on
	// this provider: a project that declared a provider running on this adapter
	// supplies its own, and a test drives the parser with a provider's answers
	// and nothing else about the provider.
	dialect backend.Dialect
	// reply is where the agent's prose goes for whoever is watching it arrive,
	// and nil when nobody is.
	reply       func(string)
	result      backend.RunResult
	sawTerminal bool
}

func newStreamParser(runID string, role domain.AgentRole, lastSequence uint64, clock execution.Clock, redactor execution.Redactor, sink func(execution.Event) error, reply func(string), dialect backend.Dialect) *streamParser {
	if dialect == nil {
		dialect = Dialect{}
	}
	return &streamParser{
		runID:    runID,
		role:     role,
		sequence: execution.NewSequence(lastSequence),
		clock:    clock,
		redactor: redactor,
		sink:     sink,
		dialect:  dialect,
		reply:    reply,
	}
}

// ParseLine reads one line of the provider's stream.
//
// A line this parser cannot read is recorded rather than fatal, which is where
// it differs from the Claude Code adapter beside it. Codex writes its events to
// standard output and nothing guarantees that every line there is one of them: a
// banner or a warning would otherwise fail a run whose work was fine. Nothing is
// lost by being lenient here, because Run still requires a terminal it can read
// — an invocation whose whole stream is unreadable fails with exactly that.
func (p *streamParser) ParseLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	message, readable := decodeMessage(line)
	if !readable {
		return p.emit(execution.EventProcessOutput, map[string]any{
			"text": truncate(p.redactor.Redact(line)),
		})
	}
	p.redactMessage(&message)
	if streamedFragment(message.Type) {
		return nil
	}
	// The provider keeps writing after the terminal — a shutdown notice, a last
	// accounting line. Those are recorded so their payload stays diagnosable, and
	// none of them may disturb the decided result: the guarded invariant is that
	// a second terminal cannot replace the first.
	if p.sawTerminal {
		return p.recordAfterTerminal(message)
	}

	switch message.Type {
	case eventSessionConfigured:
		return p.parseSessionConfigured(message)
	case eventAgentMessage:
		return p.parseAgentMessage(message)
	case eventExecCommandBegin, eventMCPToolCallBegin, eventPatchApplyBegin:
		return p.emit(execution.EventCommandStarted, map[string]any{
			"tool_use_id": message.CallID,
			"tool":        toolName(message),
			"input_bytes": inputBytes(message),
		})
	case eventExecCommandEnd, eventMCPToolCallEnd, eventPatchApplyEnd:
		return p.emit(execution.EventCommandCompleted, map[string]any{
			"tool_use_id":   message.CallID,
			"is_error":      callFailed(message),
			"content_bytes": len(message.Stdout) + len(message.Stderr),
		})
	case eventTokenCount:
		return p.parseTokenCount(message)
	case eventStreamError:
		// The provider is retrying by itself. The dialect is asked anyway, so the
		// answer for a retry in progress is the contract's rather than this
		// parser's silence.
		p.observe(backend.ProviderEvent{Type: message.Type, Text: message.Message})
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type": message.Type,
			"error":         truncate(message.Message),
		})
	case eventTaskComplete:
		return p.parseTerminal(message, message.LastAgentMessage, false)
	case eventError:
		return p.parseTerminal(message, message.Message, true)
	default:
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type": message.Type,
		})
	}
}

func (p *streamParser) EmitProcessOutput(output execution.Output) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream": output.Stream,
		"text":   truncate(p.redactor.Redact(output.Text)),
	})
}

func (p *streamParser) parseSessionConfigured(message providerMessage) error {
	p.result.SessionID = message.SessionID
	// This is where the provider names the model it resolved the requested
	// selector to. It is recorded as first-class result evidence rather than left
	// buried in the event payload, because a floating family alias makes the
	// resolved identifier the only durable evidence of what really ran.
	p.result.ResolvedModel = message.Model
	return p.emit(execution.EventRunStarted, map[string]any{
		"session_id": message.SessionID,
		"model":      message.Model,
	})
}

func (p *streamParser) parseAgentMessage(message providerMessage) error {
	p.result.FinalText = message.Message
	if err := p.emit(execution.EventAgentMessage, map[string]any{"text": truncate(message.Message)}); err != nil {
		return err
	}
	// The prose reaches a watcher after the event that records it and after the
	// redaction above, in that order and never the other: nothing may be shown
	// that the record does not hold, and nothing may be shown before it has been
	// redacted.
	if p.reply != nil {
		p.reply(message.Message)
	}
	return nil
}

func (p *streamParser) parseTokenCount(message providerMessage) error {
	usage, measured := message.usage()
	if measured {
		p.result.Usage = usage
	}
	return p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type": message.Type,
		"usage":         rawOrNil(usage),
	})
}

// parseTerminal records the invocation's own ending, whichever way it ended.
//
// Codex prices nothing: it reports what an invocation read and wrote and never
// what it cost, so CostReported stays false and the terminal event carries no
// cost at all rather than a zero that would read as an invocation that spent
// nothing. What it does carry is the role, because a run's log holds several
// invocations and where a terminal sits in it is a fact about the order the
// harness happened to do things in.
func (p *streamParser) parseTerminal(message providerMessage, text string, failed bool) error {
	p.sawTerminal = true
	if strings.TrimSpace(text) != "" || failed {
		p.result.FinalText = text
	}
	p.result.IsError = failed
	p.result.StopReason = message.Type
	// The terminal is where the provider says how the invocation ended, so it is
	// the event whose answer decides whether this is a wait, another attempt, or
	// a refusal that stands. Which of those it is comes back from the dialect;
	// that they cannot stand together is held by the contract.
	p.observe(backend.ProviderEvent{
		Type:     message.Type,
		Subtype:  message.Type,
		Text:     p.result.FinalText,
		Terminal: true,
		Failed:   failed,
	})
	eventType := execution.EventRunCompleted
	if failed {
		eventType = execution.EventRunFailed
	}
	payload := map[string]any{
		"role":            string(p.role),
		"session_id":      p.result.SessionID,
		"is_error":        failed,
		"result":          truncate(p.result.FinalText),
		"terminal_reason": message.Type,
	}
	if len(p.result.Usage) > 0 {
		payload["usage"] = json.RawMessage(p.result.Usage)
	}
	return p.emit(eventType, payload)
}

// recordAfterTerminal keeps what the provider said after it had already ended
// the invocation. Nothing here is written to the result: a second terminal is
// recorded as the anomaly it is and never replaces the first.
func (p *streamParser) recordAfterTerminal(message providerMessage) error {
	payload := map[string]any{"provider_type": message.Type}
	if message.Type == eventTaskComplete || message.Type == eventError {
		payload["anomaly"] = "terminal_after_terminal"
	}
	return p.emit(execution.EventProcessOutput, payload)
}

// observe hands one provider event to this provider's dialect and records
// whatever answer comes back on the invocation's result. It is the only way an
// answer reaches the result, so nothing in this parser decides what a provider
// said and nothing above it special-cases this provider.
func (p *streamParser) observe(event backend.ProviderEvent) {
	if p.dialect == nil {
		return
	}
	observation, said := p.dialect.Observe(event)
	if !said {
		return
	}
	observation.Record(&p.result)
}

// redactMessage removes the values that must not become a durable record from
// everything the provider authored. The event type is left alone: it is a
// control enum used for dispatch rather than provider prose, and redacting it
// could corrupt parsing if a poorly chosen credential happened to equal one.
func (p *streamParser) redactMessage(message *providerMessage) {
	message.SessionID = p.redactor.Redact(message.SessionID)
	message.Model = p.redactor.Redact(message.Model)
	message.Message = p.redactor.Redact(message.Message)
	message.LastAgentMessage = p.redactor.Redact(message.LastAgentMessage)
	message.CallID = p.redactor.Redact(message.CallID)
	message.Server = p.redactor.Redact(message.Server)
	message.Tool = p.redactor.Redact(message.Tool)
	message.Stdout = p.redactor.Redact(message.Stdout)
	message.Stderr = p.redactor.Redact(message.Stderr)
	for index := range message.Command {
		message.Command[index] = p.redactor.Redact(message.Command[index])
	}
}

func (p *streamParser) emit(eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(p.runID, p.sequence.Next(), p.clock.Now(), eventType, sourceName, payload)
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

// Result is the invocation's own answer. Everything in it is settled as the
// stream arrives, so there is nothing left to decide here.
func (p *streamParser) Result() backend.RunResult {
	return p.result
}

func (p *streamParser) SawTerminal() bool {
	return p.sawTerminal
}

// decodeMessage reads one line into the event it carries. The event normally
// sits under `msg`; a line that is a bare event with no envelope around it is
// read as one too, because which of the two shapes a version writes is not
// something this adapter should fail a run over.
func decodeMessage(line string) (providerMessage, bool) {
	var envelope streamEnvelope
	if json.Unmarshal([]byte(line), &envelope) != nil {
		return providerMessage{}, false
	}
	body := envelope.Msg
	if len(body) == 0 {
		body = json.RawMessage(line)
	}
	var message providerMessage
	if json.Unmarshal(body, &message) != nil || strings.TrimSpace(message.Type) == "" {
		return providerMessage{}, false
	}
	return message, true
}

// toolName is what the harness's command event calls the thing that ran: the
// shell for a command, the MCP tool for a tool call, and the patch applier for
// an edit. The command itself is not recorded, only how big it was — a shell
// line is provider-authored text that may quote anything in the worktree.
func toolName(message providerMessage) string {
	switch message.Type {
	case eventMCPToolCallBegin, eventMCPToolCallEnd:
		if message.Server != "" {
			return message.Server + "." + message.Tool
		}
		return message.Tool
	case eventPatchApplyBegin, eventPatchApplyEnd:
		return "apply_patch"
	default:
		return "shell"
	}
}

func inputBytes(message providerMessage) int {
	if len(message.Changes) > 0 {
		return len(message.Changes)
	}
	total := 0
	for _, word := range message.Command {
		total += len(word)
	}
	return total
}

// callFailed reports a shell, tool, or patch call that ended badly, from
// whichever of the two ways the provider says so. A call that reported neither
// is not called a failure, because absence is not the same as a non-zero exit.
func callFailed(message providerMessage) bool {
	switch {
	case message.ExitCode != nil:
		return *message.ExitCode != 0
	case message.Success != nil:
		return !*message.Success
	default:
		return false
	}
}

// rawOrNil keeps an absent usage object absent in the event payload rather than
// writing an empty one, which would be a measurement of nothing.
func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func truncate(value string) string {
	if len(value) <= maxEventTextBytes {
		return value
	}
	return value[:maxEventTextBytes] + "…[truncated]"
}
