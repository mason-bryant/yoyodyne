package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/execution"
)

const maxEventTextBytes = 16 << 10

type streamParser struct {
	runID     string
	sequence  *execution.Sequence
	clock     execution.Clock
	redactor  execution.Redactor
	sink      func(execution.Event) error
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

func newStreamParser(runID string, lastSequence uint64, clock execution.Clock, redactor execution.Redactor, sink func(execution.Event) error) *streamParser {
	return &streamParser{runID: runID, sequence: execution.NewSequence(lastSequence), clock: clock, redactor: redactor, sink: sink}
}

func (p *streamParser) ParseLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if p.sawResult {
		return errors.New("provider event received after terminal result")
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
	if envelope.SessionID != "" {
		p.result.SessionID = envelope.SessionID
	}

	switch envelope.Type {
	case "system":
		return p.parseSystem(envelope)
	case "assistant", "user":
		return p.parseMessage(envelope.Type, envelope.Message)
	case "result":
		return p.parseResult(envelope)
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
				p.result.FinalText = block.Text
				if err := p.emit(execution.EventAgentMessage, map[string]any{"text": truncate(block.Text)}); err != nil {
					return err
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

func (p *streamParser) parseResult(envelope streamEnvelope) error {
	p.sawResult = true
	p.result.SessionID = envelope.SessionID
	p.result.FinalText = envelope.Result
	p.result.IsError = envelope.IsError
	p.result.CostUSD = envelope.TotalCostUSD
	p.result.Usage = append([]byte(nil), envelope.Usage...)
	p.result.StopReason = envelope.TerminalReason
	if p.result.StopReason == "" {
		p.result.StopReason = envelope.StopReason
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
