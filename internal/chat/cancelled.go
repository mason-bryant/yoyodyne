package chat

// A turn something killed on its way to the role, told apart from a turn the
// role answered badly.
//
// A cancelled invocation is the harness's own context ending under the process:
// a signal, a shutdown, a process-group teardown taken out from under a
// long-running command. Nothing about it is a judgement of the work, and nothing
// about it is the provider failing — it is the harness withdrawing its own
// question before an answer existed.
//
// That matters to exactly one kind of caller: the one taking turns nobody is
// sitting in front of. A person at a terminal sees the failure and types the
// message again. A delivery has a bounded number of attempts and no person, so a
// cancellation counted as an attempt is the harness's own shutdown spending the
// operator's attention budget.

import (
	"errors"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ErrTurnAbandoned marks the failure of a turn a cancellation killed before any
// answer of the role's existed. It is a sentinel joined into the error the turn
// fails with, exactly as ErrProviderCapacity is, so what a person reads is
// unchanged and a caller that is not a person can tell the two endings apart.
//
// Where the line falls is what the sentinel is worth, so it is stated rather
// than implied. A cancelled invocation that produced no answer produced nothing
// the harness could act on: the turn fails before a reply is parsed, so no
// action of hers was applied and no durable record moved. A cancelled invocation
// that did produce one is not this: her answer arrived, whatever became of the
// process afterwards, and a caller must treat that turn as one she took.
//
// It does not claim the role never saw the message. The provider had the prompt
// the moment the invocation started, and a transcript it had already written is
// hers to read on the next turn. What it claims is narrower and is the whole of
// what a caller may rely on: this turn produced no answer, so nothing was
// decided and nothing was carried out.
var ErrTurnAbandoned = errors.New("the turn was cancelled before the role answered")

// turnAbandoned is that sentinel for a cancelled turn that produced no answer,
// and nothing for every other ending.
//
// The two pieces of evidence are the ones a provider writes only at the terminal
// of an invocation: the answer text, and the cost it priced the invocation at. A
// stream cut off mid-turn reaches no terminal and carries neither, and one that
// reached its own carries both whether or not the process survived long enough
// to exit — which is what makes a cancellation landing after her answer arrived
// distinguishable from one that landed before it.
//
// The session identifier is deliberately not among them, and the reason is worth
// stating because it is the obvious third: it is recorded from the first
// envelope carrying one, which is the event that opens the session and precedes
// anything the role does. A killed turn has one. Reading it as evidence the
// invocation ended would make every abandoned turn look answered, which is this
// whole judgement inverted.
func turnAbandoned(result backend.RunResult) error {
	if result.Process.Status != execution.ProcessCancelled {
		return nil
	}
	if result.CostReported || strings.TrimSpace(result.FinalText) != "" {
		return nil
	}
	return ErrTurnAbandoned
}
