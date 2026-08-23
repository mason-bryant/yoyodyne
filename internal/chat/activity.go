package chat

// A turn that prints nothing between the operator's message and the reply makes
// a slow turn and a hung one look exactly alike, and an operator who reasonably
// concludes the command is broken kills a conversation that was working. The
// harness already knows what is happening: every phase below is read off the
// same normalized event stream the turn records. This is only where it is said
// out loud, and it changes nothing about what is recorded.

import (
	"encoding/json"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The phases a turn passes through, in the operator's language rather than the
// provider's. They are deliberately coarse: what the operator needs is whether
// the turn is alive and roughly what it is doing, not an event log.
const (
	phaseSending   = "sending your message"
	phaseThinking  = "thinking about it"
	phaseWriting   = "writing the reply"
	phaseTracker   = "carrying out the tracker actions it asked for"
	phaseExchange  = "putting its question to the other role"
	phaseRecording = "recording the turn"
)

// turnActivity is the display fed by a turn's own events. It keeps the last
// phase it was able to name, so an event that says nothing new about what is
// happening still refreshes the fact that something arrived: that is what
// separates a turn that is progressing from one that has stalled.
type turnActivity struct {
	display console.Activity
	phase   string
}

// doing names a phase directly, for the work the harness does between provider
// invocations, which emits no provider events to read.
func (a *turnActivity) doing(phase string) {
	if a == nil || a.display == nil {
		return
	}
	a.phase = phase
	a.display.Doing(phase)
}

// observe reports one recorded event to the operator's display. It reads the
// event rather than the provider's own stream, so the display can never say
// anything the durable record does not.
func (a *turnActivity) observe(event execution.Event) {
	if a == nil || a.display == nil {
		return
	}
	if phase := describeEvent(event); phase != "" {
		a.phase = phase
	}
	a.display.Doing(a.phase)
}

// eventDetail is the part of an event payload the display reads. Everything
// else in the payload is evidence for the record rather than something to put
// on screen.
type eventDetail struct {
	ProviderSubtype string `json:"provider_subtype"`
	Attempt         int    `json:"attempt"`
	MaxRetries      int    `json:"max_retries"`
	Tool            string `json:"tool"`
}

// describeEvent names what an event says the turn is doing, or nothing where it
// says only that the turn is still alive.
func describeEvent(event execution.Event) string {
	switch event.Type {
	case execution.EventRunStarted:
		return phaseThinking
	case execution.EventAgentMessage:
		return phaseWriting
	case execution.EventRunCompleted, execution.EventRunFailed:
		return phaseRecording
	case execution.EventProcessOutput:
		detail := readEventDetail(event)
		if detail.ProviderSubtype == providerRetrySubtype {
			return describeRetry(detail)
		}
		return ""
	case execution.EventCommandStarted:
		if tool := readEventDetail(event).Tool; tool != "" {
			return fmt.Sprintf("running %s", tool)
		}
		return "running a tool"
	default:
		return ""
	}
}

// providerRetrySubtype is what the provider CLI calls its own retries. A turn
// that is slow because the provider is refusing requests is telling the
// operator something about their account or the service, and it is the case
// most likely to look like a hang, so it is named rather than folded into
// generic waiting.
const providerRetrySubtype = "api_retry"

func describeRetry(detail eventDetail) string {
	if detail.Attempt <= 0 || detail.MaxRetries <= 0 {
		return "the provider is refusing requests; it is being retried"
	}
	return fmt.Sprintf("the provider is refusing requests; retrying (attempt %d of %d)", detail.Attempt, detail.MaxRetries)
}

// readEventDetail reads what the display needs from an event payload. A payload
// it cannot read describes an event that still happened, so it is reported as
// activity with nothing said about it rather than dropped.
func readEventDetail(event execution.Event) eventDetail {
	var detail eventDetail
	if len(event.Payload) == 0 {
		return detail
	}
	if err := json.Unmarshal(event.Payload, &detail); err != nil {
		return eventDetail{}
	}
	return detail
}
