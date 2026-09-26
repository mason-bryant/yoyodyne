package readmodel

// What says a refusal no longer stands before the reset it quoted.
//
// A refusal stands until the reset the provider named, and until this that
// clock was the only thing that ended one. Two things end one sooner, and both
// are facts the harness already holds. The provider serving the same account
// and model after the refusal was recorded is the provider saying the window is
// open: on 2026-09-24 the operator added capacity a day into a seven-day window,
// every turn after that was served, and the dashboard showed two conversations
// blocked until 09-27. And a conversation its role has since replaced is one in
// which nothing will happen again, so a refusal of it holds nobody: one of those
// two was a retired conversation that nothing would ever have cleared.
//
// This is read once, here, and every reading of what the provider is refusing
// passes its refusals through it — the capacity-blocked list, the hold over
// every role, and the watch session's hold on intake — so no surface can come to
// clear a block another still shows.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CapacityServedRecord is the durable record of what the provider has served,
// per account and model. It is satisfied by *runstate.CapacityServedStore.
type CapacityServedRecord interface {
	List() ([]runstate.CapacityServed, error)
}

// CapacityEvidence is what a reading of the refusals is read against.
type CapacityEvidence struct {
	// Served is the latest moment the provider served each account and model.
	Served []runstate.CapacityServed
	// Current is every conversation that is still its role's, by id, and nil
	// where that could not be read — which clears nothing rather than treating
	// every conversation as retired.
	Current map[string]bool
}

// Lifted reports a refusal the evidence says no longer stands: a served
// invocation after it on its account and model, or a conversation its role has
// since replaced.
func (e CapacityEvidence) Lifted(refusal runstate.UsageLimitExhaustion) bool {
	if id := strings.TrimSpace(refusal.ConversationID); id != "" && e.Current != nil && !e.Current[id] {
		return true
	}
	for _, served := range e.Served {
		if served.Lifts(refusal) {
			return true
		}
	}
	return false
}

// Standing is the refusals the evidence leaves standing, in the order given.
func (e CapacityEvidence) Standing(refusals []runstate.UsageLimitExhaustion) []runstate.UsageLimitExhaustion {
	if len(e.Served) == 0 && e.Current == nil {
		return refusals
	}
	var standing []runstate.UsageLimitExhaustion
	for _, refusal := range refusals {
		if !e.Lifted(refusal) {
			standing = append(standing, refusal)
		}
	}
	return standing
}

// ReadCapacityEvidence reads the evidence from the two records it lives in,
// either of which may be absent. Where either could not be read, nothing is
// cleared early at all — not by the one that could be read either — and the
// failure is said as the problem: a block cleared on part of the evidence is
// cleared on a guess about the rest, and one left standing over it is only
// late. A record that is simply not wired clears nothing of its own kind and is
// no problem: that caller never asked.
func ReadCapacityEvidence(served CapacityServedRecord, conversations Conversations) (CapacityEvidence, string) {
	var evidence CapacityEvidence
	var problems []string
	if served != nil {
		listed, err := served.List()
		if err != nil {
			problems = append(problems, fmt.Sprintf("what the provider has served since it refused could not be read: %v", err))
		}
		evidence.Served = listed
	}
	if conversations != nil {
		recorded, err := conversations.Recorded()
		if err != nil {
			problems = append(problems, fmt.Sprintf("which conversations are still their roles' could not be read: %v", err))
		} else {
			evidence.Current = make(map[string]bool, len(recorded))
			for _, conversation := range recorded {
				evidence.Current[conversation.ConversationID] = true
			}
		}
	}
	if len(problems) > 0 {
		return CapacityEvidence{}, "nothing was read as lifted before its quoted reset, because " + strings.Join(problems, "; and ")
	}
	return evidence, ""
}

// CapacityEvidenceOf reads the evidence from a set of sources.
func CapacityEvidenceOf(sources Sources) (CapacityEvidence, string) {
	return ReadCapacityEvidence(sources.CapacityServed, sources.Conversations)
}
