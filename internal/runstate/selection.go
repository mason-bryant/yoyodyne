package runstate

// Why the harness is running this work item.
//
// While the operator started every run themselves, the answer was never worth
// recording: they typed the identifier, so they knew. Once a development manager
// pulls from the queue and work starts without anybody asking for it item by
// item, the answer stops being obvious and starts being the thing that separates
// autonomy from work happening behind somebody's back. A run nobody can account
// for looks the same whether it was chosen well or chosen by a bug.
//
// So it is recorded with the run rather than derived afterwards. It is written
// once, when the run is reserved, by the process that holds the run's lease, and
// nothing changes it: what made the harness pick this item is a fact about a
// moment, and a later reading of the queue is a different fact.
//
// It is absent from a run recorded before selections existed, and absence is
// reported as "no reason recorded" rather than rendered as a blank — an
// unaccounted run is exactly what this exists to make visible, so it must not be
// possible to mistake one for a run whose reason happened to be empty.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The two things that choose work, named here rather than imported from the
// role vocabulary because this is durable evidence: a record written today has
// to keep meaning what it meant if the roles are ever renamed.
const (
	SelectedByOperator           = "operator"
	SelectedByDevelopmentManager = "development manager"
)

// MaxSelectionReasonBytes bounds the recorded reason. It is generous enough for
// a development manager to say what it weighed and small enough that no
// selection can crowd out the run state it sits in.
const MaxSelectionReasonBytes = 4 << 10

// Selection is why this run exists: who chose the work, when, and on what
// grounds. Reason is prose meant for the operator rather than a code, because
// what makes a choice defensible is the argument for it and not a category.
type Selection struct {
	By     string    `json:"by"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// Validate rejects a selection that could not account for a run. Every field is
// required: a selection naming nobody, or giving no reason, records the absence
// this exists to prevent while looking like a record of something.
func (s Selection) Validate() error {
	var problems []error
	if strings.TrimSpace(s.By) == "" {
		problems = append(problems, errors.New("selection by is required"))
	}
	if strings.TrimSpace(s.Reason) == "" {
		problems = append(problems, errors.New("selection reason is required"))
	}
	if len(s.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("selection reason is %d bytes, which exceeds the %d byte bound", len(s.Reason), MaxSelectionReasonBytes))
	}
	if s.At.IsZero() {
		problems = append(problems, errors.New("selection at is required"))
	}
	return errors.Join(problems...)
}

// Stated reports a selection that actually accounts for a choice. It is the
// question every caller asks before recording one, because a half-filled
// selection is the absence this exists to make visible rather than a weaker
// version of a record.
func (s Selection) Stated() bool {
	return strings.TrimSpace(s.By) != "" && strings.TrimSpace(s.Reason) != ""
}

// Stamped dates a stated selection and reports whether there was one to date. A
// caller that supplied its own moment keeps it; one that did not gets the moment
// the run was reserved, which is when the choice took effect. An unstated
// selection, or one that could not be recorded as it stands, is reported as
// nothing rather than stored half-formed.
func (s Selection) Stamped(at time.Time) (Selection, bool) {
	if !s.Stated() {
		return Selection{}, false
	}
	stamped := Selection{By: strings.TrimSpace(s.By), Reason: strings.TrimSpace(s.Reason), At: s.At}
	if stamped.At.IsZero() {
		stamped.At = at.UTC()
	}
	if stamped.Validate() != nil {
		return Selection{}, false
	}
	return stamped, true
}

// SelectedByHarness reports a selection that is not the operator naming the
// work, which is what an intake hold applies to. An unstated selection counts as
// the harness choosing: unaccounted work is exactly what the hold exists to
// catch, so the uncertain case belongs on the side that is held rather than the
// side that runs.
func (s Selection) SelectedByHarness() bool {
	return strings.TrimSpace(s.By) != SelectedByOperator
}

// OperatorSelection is the selection every run an operator asked for by name
// carries. The reason is supplied by whoever took the instruction, because
// "from a conversation, after turn 12" and "from a command line" are different
// accounts of the same authority and the operator can tell them apart.
func OperatorSelection(reason string, at time.Time) Selection {
	return Selection{By: SelectedByOperator, Reason: strings.TrimSpace(reason), At: at.UTC()}
}
