package chat

// A program manager's request that the supervisor restart a part of the product.
//
// `docs/designs/program-manager.md` gives the role one request to the
// supervisor, `service.request-restart`, under "Restarts": a block naming a part
// the services section declares writes one durable request, at most one open
// per part per instance, and the supervisor's periodic pass is the only thing
// that ever acts on one. That pass is yoyodyne-ifd.413 and has not landed, so a
// request is recorded, carried by the read model, and acted on by nothing — and
// the role is told exactly that in the result, so it never reports a part as
// restarted that nobody restarted.
//
// Nothing here starts, stops, or signals a process. The request path writes a
// record through the store and renders what became of it; the harness is the
// only invoker, and noticing surfaces do not restart processes.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const restartFence = "```yoyodyne-restart"

// maxRestartBlockBytes bounds the block itself: one part and a reason.
const maxRestartBlockBytes = runstate.MaxRestartReasonBytes + 1<<10

// restartExecutor is the work that builds what acts on a request, named in the
// result and on every surface that shows one.
const restartExecutor = "yoyodyne-ifd.413"

// RestartRequests is where a program manager's restart requests are recorded.
// It is satisfied by *runstate.RestartRequestStore.
type RestartRequests interface {
	Request(request runstate.RestartRequest) (runstate.RestartRequest, error)
}

// RestartAsk is what one reply asked to be restarted, and why.
type RestartAsk struct {
	Part   string `json:"part"`
	Reason string `json:"reason"`
}

// RestartOutcome is what became of one ask: the request as recorded, or why
// nothing was.
type RestartOutcome struct {
	Ask      RestartAsk               `json:"ask"`
	Recorded bool                     `json:"recorded"`
	Request  *runstate.RestartRequest `json:"request,omitempty"`
	Failure  string                   `json:"failure,omitempty"`
}

// RestartError reports a restart block the harness could not read. Nothing in it
// was recorded, and nothing else about the turn is changed by it.
type RestartError struct {
	Err error
}

func (e *RestartError) Error() string {
	return "the reply carried a restart block the harness cannot read: " + e.Err.Error()
}

func (e *RestartError) Unwrap() error { return e.Err }

// extractRestart takes the restart block out of a reply, leaving its prose.
func extractRestart(reply string) (string, *RestartAsk, error) {
	prose, payload, found, err := splitFencedBlock(reply, restartFence, "restart")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	ask, err := decodeRestart(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, &ask, nil
}

// decodeRestart strictly decodes the block. Which parts exist and whether one is
// already asked for are the store's to decide, and a refusal there is an outcome
// the role is told rather than an unreadable block.
func decodeRestart(payload string) (RestartAsk, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return RestartAsk{}, errors.New("decode restart request: the restart block is empty")
	}
	if len(trimmed) > maxRestartBlockBytes {
		return RestartAsk{}, fmt.Errorf("decode restart request: block is %d bytes, limit is %d", len(trimmed), maxRestartBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var ask RestartAsk
	if err := decoder.Decode(&ask); err != nil {
		return RestartAsk{}, fmt.Errorf("decode restart request: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return RestartAsk{}, errors.New("decode restart request: unexpected trailing content after the request")
	}
	return ask, nil
}

// performRestartRequest records the one request a reply asked for. A refusal —
// a part the services section does not declare, a request already open for the
// part, a store that would not take it — is an outcome the role is told, and
// never a failed turn.
func (s *Session) performRestartRequest(ask RestartAsk) RestartOutcome {
	outcome := RestartOutcome{Ask: ask}
	if err := runstate.ValidateRestartPart(strings.TrimSpace(ask.Part)); err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	if s.options.RestartRequests == nil {
		outcome.Failure = "no restart request store is wired to this conversation, so nothing was recorded"
		return outcome
	}
	id, err := runstate.NewRestartRequestID()
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	recorded, err := s.options.RestartRequests.Request(runstate.RestartRequest{
		SchemaVersion:  runstate.RestartRequestSchemaVersion,
		ProductID:      s.options.ProductID,
		ID:             id,
		Agent:          s.options.Agent,
		Part:           config.ServiceName(strings.TrimSpace(ask.Part)),
		Reason:         strings.TrimSpace(ask.Reason),
		RequestedAt:    s.options.clock().Now(),
		ConversationID: s.state.ConversationID,
		Turn:           s.state.Turns,
	})
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	outcome.Recorded = true
	outcome.Request = &recorded
	return outcome
}

// renderRestartResult is what the role is told became of its request. A
// recorded request says, in as many words, that nothing acts on it yet, so the
// role never reports a part as restarted that nobody restarted.
func renderRestartResult(outcome RestartOutcome) string {
	var rendered strings.Builder
	rendered.WriteString("# Restart request\n\n")
	if outcome.Recorded {
		fmt.Fprintf(&rendered, "Recorded as %s: a request that the supervisor restart the %s. It is shown on your report and in the standing. Nothing acts on it yet: the supervisor's periodic pass, which is the only thing that executes a restart request, is %s and has not landed, so no part has been or will be restarted until it does. Do not say the %s was restarted.\n\n",
			outcome.Request.ID, outcome.Request.Part, restartExecutor, outcome.Request.Part)
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "Not recorded: %s\n\n", outcome.Failure)
	return rendered.String()
}

// reportRestart tells the operator what the role asked of the supervisor.
func (s *Session) reportRestart(out io.Writer, reply Reply) {
	if reply.Restart == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	outcome := *reply.Restart
	if outcome.Recorded {
		fmt.Fprintf(out, "the %s asked for the %s to be restarted (%s); nothing acts on the request until %s lands\n\n", title, outcome.Request.Part, outcome.Request.ID, restartExecutor)
		return
	}
	fmt.Fprintf(out, "the %s's request to restart %q was not recorded: %s\n\n", title, outcome.Ask.Part, outcome.Failure)
}

// restartContract is what a role holding service.request-restart is told about
// it.
const restartContract = `# Asking for a part of the product to be restarted

Where you believe a part of the product should be restarted — the Slack sink, the dashboard, the scheduler, or the maintenance pass — you may ask the supervisor to restart it, by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-restart
{"part":"scheduler","reason":"why, in a sentence or two"}
` + "```" + `

The part is one of "slack", "dashboard", "scheduler", and "maintenance". The harness records the request durably and shows it on your report and in the standing; at most one request per part may be open at a time, and a second while the first is unanswered is refused naming the first. You restart, stop, and signal nothing yourself. The supervisor's periodic pass is the only thing that acts on a request, and it has not been built yet (` + restartExecutor + `), so today a request is recorded and acted on by nothing: say that it was asked for, never that it happened.`
