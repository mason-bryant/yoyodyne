package chat

// What a role may ask for on a side thread, which is less than it may ask for on
// its main one.
//
// A side conversation runs beside the main thread and never in place of it: it
// judges, answers, and tentatively plans, and every intent it forms is a draft
// the main thread ratifies through its own single-threaded path. So its authority
// is the role's own, narrowed — and narrowed here, in Go, for the same reason the
// table it narrows is written here. The per-agent knob chooses whether an agent
// holds side threads at all; no value of it reaches what one may do, which is
// what `configuration-never-grants-authority` requires.
//
// The narrowing is read off `internal/sidestream` rather than written out a
// second time. What a side thread may ask for is stated once, in the capability
// vocabulary, exactly as what a role may ask for is.

import (
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// OnSideStream is this role's authority as a side thread holds it: judgment and
// reading, and no action at all.
//
// Every answer is the role's own answer and the side thread's, so this can only
// ever take authority away. A role that may not survey the tracker on its main
// thread may not survey it here either, and a role that may admit work on its
// main thread may not admit any here — which is the whole point, because the
// second is the case a side thread would otherwise widen.
//
// What it leaves alone is what is not authority: the title, the contract, and
// what the role owns are what a role is called and what it is sent, and a side
// thread is still that role.
func (a Authority) OnSideStream() Authority {
	narrowed := a
	narrowed.TrackerActions = nil
	for _, action := range a.TrackerActions {
		if sidestream.Permits(trackerCapabilities[action]) {
			narrowed.TrackerActions = append(narrowed.TrackerActions, action)
		}
	}
	narrowed.Proposals = a.Proposals && sidestream.Permits(capability.ProposalRaise)
	narrowed.Concerns = a.Concerns && sidestream.Permits(capability.ConcernRaise)
	narrowed.Research = a.Research && sidestream.Permits(capability.ResearchCommission)
	narrowed.Evaluations = a.Evaluations && sidestream.Permits(capability.EvaluationRecord)
	narrowed.Asks = a.Asks && sidestream.Permits(capability.ExchangeAsk)
	return narrowed
}
