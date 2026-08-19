package chat

// The operator's hold on the work the harness chooses for itself, as a
// conversation places, lifts, and reads it.
//
// It is the middle of the three things an operator can do about work in
// progress, and the one with no equivalent before now. Stopping one item is
// aimed at a run; pausing everything stops the runs too. This stops neither: it
// stops the choosing, and lets what is already running finish. That is the case
// somebody reaches for when the queue looks wrong but nothing is on fire, and it
// is the hardest of the three to add afterwards, because by the time it is
// wanted the only available verb is the blunt one.
//
// It never stops the operator. An item they name runs while intake is held —
// they placed the hold, so naming something is them deciding it is the
// exception, and a hold that also stopped its owner would just be the other
// switch under a different name.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// IntakeHolds is the operator's switch over what the harness starts by itself,
// as a conversation reads and writes it. It is satisfied by
// runstate.IntakeHoldStore.
type IntakeHolds interface {
	Hold(reason string, at time.Time) (runstate.IntakeHold, error)
	Held() (runstate.IntakeHold, bool, error)
	Release() (runstate.IntakeHold, bool, error)
}

// errNoIntake reports a conversation with no intake switch wired to it. Such a
// conversation can still see and stop work; it just cannot hold what the harness
// chooses, and says so rather than appearing to have done it.
var errNoIntake = errors.New("no intake switch is wired to this conversation, so it cannot hold or release what the harness starts on its own")

// IntakeReport is what the operator is told about intake: what is in force now,
// and whether this command is what put it there. The two are separate because
// holding what is already held and lifting what was never placed both leave
// intake in the state the operator asked for while having changed nothing, and a
// report that could not tell those apart would claim an act that never happened.
type IntakeReport struct {
	Held bool                `json:"held"`
	Hold runstate.IntakeHold `json:"hold,omitzero"`
	// Placed and Lifted report a change this command made. Both false on a
	// command that asked for a state intake was already in.
	Placed bool `json:"placed,omitempty"`
	Lifted bool `json:"lifted,omitempty"`
}

// ReadIntake reports what the harness may start on its own. It is read-only:
// asking whether intake is held never changes whether it is.
func (s *Session) ReadIntake() (IntakeReport, error) {
	if s.options.Intake == nil {
		return IntakeReport{}, errNoIntake
	}
	hold, held, err := s.options.Intake.Held()
	if err != nil {
		return IntakeReport{}, fmt.Errorf("read whether intake is held: %w", err)
	}
	return IntakeReport{Held: held, Hold: hold}, nil
}

// HoldIntake stops the harness choosing new work for this product. Work already
// running carries on: the hold is read where a run would be started, and a run
// under way is past that point.
//
// The hold is durable before anything is recorded about it here, because the
// hold is the thing that matters: a process that dies between the two leaves a
// harness that has genuinely stopped choosing work, which is what the operator
// asked for, rather than a conversation log claiming a hold nothing enforces.
func (s *Session) HoldIntake(reason string) (IntakeReport, error) {
	if s.options.Intake == nil {
		return IntakeReport{}, errNoIntake
	}
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) > MaxOperatorMessageBytes {
		return IntakeReport{}, fmt.Errorf("intake hold reason is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	_, alreadyHeld, err := s.options.Intake.Held()
	if err != nil {
		return IntakeReport{}, fmt.Errorf("read whether intake is held: %w", err)
	}
	hold, err := s.options.Intake.Hold(s.intakeNote(trimmed), s.options.clock().Now())
	if err != nil {
		return IntakeReport{}, fmt.Errorf("hold what the harness starts on its own: %w", err)
	}
	report := IntakeReport{Held: true, Hold: hold, Placed: !alreadyHeld}
	if !report.Placed {
		return report, nil
	}
	s.notice("the operator held intake: the harness starts nothing more on its own until they release it")
	if err := s.emit(execution.EventIntakeHeld, map[string]any{"held_at": hold.HeldAt, "reason": hold.Reason}); err != nil {
		return report, fmt.Errorf("record holding intake: %w", err)
	}
	return report, nil
}

// ReleaseIntake lets the harness choose work again. Releasing what is not held
// is not a failure: the operator means the harness to be picking work up, and it
// is, so the report says nothing changed rather than refusing them.
func (s *Session) ReleaseIntake() (IntakeReport, error) {
	if s.options.Intake == nil {
		return IntakeReport{}, errNoIntake
	}
	lifted, wasHeld, err := s.options.Intake.Release()
	if err != nil {
		return IntakeReport{}, fmt.Errorf("release the hold on what the harness starts: %w", err)
	}
	report := IntakeReport{Held: false, Hold: lifted, Lifted: wasHeld}
	if !wasHeld {
		return report, nil
	}
	s.notice("the operator released intake: the harness may start work on its own again")
	if err := s.emit(execution.EventIntakeReleased, map[string]any{"held_at": lifted.HeldAt}); err != nil {
		return report, fmt.Errorf("record releasing intake: %w", err)
	}
	return report, nil
}

// intakeNote is what a hold records about why. The conversation and the turn are
// named for the same reason a stopped item's note names them: the hold has to
// trace back to the intent that placed it, and an operator coming back to a quiet
// queue in the morning is exactly the person who needs that trail.
func (s *Session) intakeNote(reason string) string {
	note := fmt.Sprintf("held from product-manager conversation %s, after turn %d",
		s.state.ConversationID, s.state.Turns)
	if reason != "" {
		note += ": " + reason
	}
	return note
}

// intakeBanner is what a status report says about intake while it is held. It
// sits beneath the pause banner and is quieter than it, which is the difference
// between the two: a paused harness is doing nothing at all, while a held intake
// is a harness finishing what it has and starting nothing more, and only one of
// those looks like a system that died.
//
// A switch that cannot be read is reported rather than passed over, for the
// reason the pause is: "I could not find out whether the harness is choosing
// work" is a different fact from "it is", and only one of them means the quiet is
// expected.
func (s *Session) intakeBanner() string {
	report, err := s.ReadIntake()
	if errors.Is(err, errNoIntake) {
		return ""
	}
	if err != nil {
		return "COULD NOT READ THE INTAKE HOLD: " + err.Error() + "\n\n"
	}
	if !report.Held {
		return ""
	}
	banner := "INTAKE HELD: the harness starts nothing more on its own, since " + report.Hold.HeldAt.Format(time.RFC3339) + "."
	if reason := strings.TrimSpace(report.Hold.Reason); reason != "" {
		banner += "\n" + singleLine(reason, MaxOperatorMessageBytes)
	}
	return banner + "\nWork already running carries on. /release lifts it, and /work <beads-id> still runs an item you name.\n\n"
}

// Render describes what intake is doing and what this command did about it.
func (r IntakeReport) Render() string {
	var rendered strings.Builder
	switch {
	case r.Placed:
		fmt.Fprintf(&rendered, "held intake at %s: the harness starts nothing more on its own.\n", r.Hold.HeldAt.Format(time.RFC3339))
		rendered.WriteString(indent("work already running carries on and is not disturbed; /stop <beads-id> stops one of those, and /stop-everything stops them all."))
		rendered.WriteString(indent("/work <beads-id> still runs an item you name, and /release lets the harness choose work again."))
	case r.Lifted:
		fmt.Fprintf(&rendered, "released the hold on intake, which you placed at %s. The harness may choose work again.\n", r.Hold.HeldAt.Format(time.RFC3339))
	case r.Held:
		fmt.Fprintf(&rendered, "intake is held, since %s, so the harness starts nothing on its own.\n", r.Hold.HeldAt.Format(time.RFC3339))
		if reason := strings.TrimSpace(r.Hold.Reason); reason != "" {
			rendered.WriteString(indent(singleLine(reason, MaxOperatorMessageBytes)))
		}
		rendered.WriteString(indent("this was already the case, so nothing changed. /release lifts it."))
	default:
		rendered.WriteString("intake is not held: the harness may choose work from the backlog on its own.\n")
	}
	return rendered.String()
}
