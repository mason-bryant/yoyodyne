package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func deliveredEntry() triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, "run-0123456789abcdef0123456789abcdef"),
		Class:         triage.ClassStoppedRun,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.209.16",
		WorkItemTitle: "A 2-of-2 review stoppage escalates itself",
		Blocker:       "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

// What the harness says when it puts a stoppage in front of her names the entry
// and says nobody has decided anything. A delivery that read as a recommendation
// would be the harness having an opinion about work it is only carrying.
func TestTheEscalationMessageNamesTheStoppageAndDecidesNothing(t *testing.T) {
	t.Parallel()

	message := escalationMessage(deliveredEntry())
	for _, wanted := range []string{
		"run-0123456789abcdef0123456789abcdef",
		"yoyodyne-ifd.209.16 — A 2-of-2 review stoppage escalates itself",
		"nothing has been decided about it",
		"yours to judge",
	} {
		if !strings.Contains(message, wanted) {
			t.Fatalf("the message reads:\n%s\nwant it to carry %q", message, wanted)
		}
	}
	// The evidence is in the docket the conversation is already built on, so a
	// message that repeated it would be a second copy that can disagree with the
	// first.
	if strings.Contains(message, "Yoyodyne stopped this item") {
		t.Fatalf("the message reads:\n%s\nwant the blocker left to the docket entry", message)
	}
	// And it recommends nothing: the vocabulary of decisions is hers.
	for _, decided := range []string{"repair it", "re-run", "escalate to the operator"} {
		if strings.Contains(message, decided) {
			t.Fatalf("the message reads:\n%s\nwant no decision recommended in it", message)
		}
	}
}

// A conversation that could not be opened asks her nothing, and says so in the
// one way the caller acts on: the attempt is given back rather than spent.
func TestAConversationThatCannotBeOpenedReportsItselfUnreachable(t *testing.T) {
	t.Parallel()

	manager := developmentManagerConversation{configPath: t.TempDir() + "/nothing-here.yaml"}
	_, err := manager.Judge(context.Background(), deliveredEntry())
	if !errors.Is(err, orchestrator.ErrConversationUnreachable) {
		t.Fatalf("Judge() error = %v, want the conversation reported as unreachable", err)
	}
}

// The delivery is wired over the same parts everything else reads, and it
// validates: an escalator missing any of them would refuse every stoppage at the
// first pass, in the real binary and nowhere else.
func TestTheDeliveryIsWiredOverTheHarnesssOwnRecords(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}

	escalator := escalatorFrom(parts, configPath, nil)
	if escalator.Docket == nil || escalator.Runs == nil || escalator.Records == nil || escalator.Manager == nil {
		t.Fatalf("escalator = %#v, want every record a delivery needs wired", escalator)
	}
	// The pause is wired too: a delivery is a provider invocation, so `yoyo
	// pause` has to cover it exactly as it covers a run.
	if escalator.Holds == nil {
		t.Fatal("the delivery was wired no pause, so the operator could watch it spend through one")
	}
	// A pass over a product where nothing has stopped delivers nothing and fails
	// at nothing, which is what says the wiring is usable rather than only
	// present.
	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 {
		t.Fatalf("escalated = %#v, want nothing delivered where nothing has stopped", sweep.Escalated)
	}
}

// A turn that produced no answer of hers keeps the stoppage's delivery. Three
// failures leave that behind: a provider that declined the turn for want of
// capacity, a pause the operator placed after the escalator read it, and a
// cancellation that killed the turn before her reply existed.
func TestATurnSheWasNeverAskedKeepsTheDelivery(t *testing.T) {
	t.Parallel()

	declined := fmt.Errorf("development manager reported failure: %w", chat.ErrProviderCapacity)
	if !errors.Is(notReached(declined), orchestrator.ErrConversationUnreachable) {
		t.Fatalf("a turn the provider declined = %v, want the delivery kept", notReached(declined))
	}
	paused := fmt.Errorf("the turn was refused: %w", &chat.OperatorHoldError{
		Hold: runstate.OperatorHold{HeldAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)},
	})
	if !errors.Is(notReached(paused), orchestrator.ErrConversationUnreachable) {
		t.Fatalf("a turn the pause refused = %v, want the delivery kept", notReached(paused))
	}

	// The sentence the twelve abandoned records of yoyodyne-ifd.250 all carried.
	// Every one of them was a turn a process-group teardown killed, and every one
	// of them spent an attempt it should have given back.
	teardown := fmt.Errorf("development manager reported failure: cancelled: %w", chat.ErrTurnAbandoned)
	if !errors.Is(notReached(teardown), orchestrator.ErrDeliveryCancelled) {
		t.Fatalf("a turn a teardown killed = %v, want the delivery kept", notReached(teardown))
	}
	// And it says which of the two it was, because both are written into a record
	// somebody reads: her conversation opened perfectly well.
	if errors.Is(notReached(teardown), orchestrator.ErrConversationUnreachable) {
		t.Fatalf("a turn a teardown killed = %v, want it reported as a cancellation rather than an unopened conversation", notReached(teardown))
	}

	// And a turn that may have reached her is left as it is: nothing here can
	// prove she did not read it, and asking again would spend a turn on an
	// answer she may already have given.
	answered := errors.New("development manager reported failure: the reply could not be read")
	if notReached(answered) != answered {
		t.Fatalf("a turn that may have reached her = %v, want it left as a delivery that happened", notReached(answered))
	}
}
