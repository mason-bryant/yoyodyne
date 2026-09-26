package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// wakeCapture is a role's conversation as these tests reach it: every message a
// firing put into it.
type wakeCapture struct {
	messages []string
}

func (w *wakeCapture) Wake(_ context.Context, _ domain.AgentRole, _, _, message string) (orchestrator.Turn, error) {
	w.messages = append(w.messages, message)
	return orchestrator.Turn{ConversationID: "chat-1", Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "looked"}}, nil
}

// threeStoppages records three runs of three items, each ended on a durable
// blocker an hour apart, and nothing else: no delivery of any of them to the
// development manager and no decision about any of them.
func threeStoppages(t *testing.T) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	for index, item := range []string{"yoyodyne-ifd.430.13.4", "yoyodyne-ifd.428.29", "yoyodyne-ifd.429.21"} {
		state := stoppedRunOf(item)
		state.RunID = fmt.Sprintf("run-%032x", index+1)
		completed := state.CompletedAt.Add(time.Duration(index) * time.Hour)
		state.CompletedAt = &completed
		state.UpdatedAt = completed
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	return store
}

func developmentManagerSweep() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"development-manager-sweep": {
			Role:     domain.RoleDevelopmentManager,
			Every:    config.Duration(time.Hour),
			Enabled:  true,
			Prompt:   "sweep for unresolved issues",
			MaxTurns: 1,
		},
	}
}

// A scheduled pass of the development manager's is handed the docket as it
// stands, in the message that wakes her, whether or not anything was delivered
// to her since her last turn. On 2026-09-25 three passes ran after the first of
// three approved changes stopped waiting on her and decided none of them: her
// conversation's docket was the one rendered when it opened, a pass resumes it,
// and stoppages otherwise reached her one per delivery.
func TestAScheduledSweepCarriesEveryUndecidedStoppageWithNoDeliveryMade(t *testing.T) {
	t.Parallel()

	runs := threeStoppages(t)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewSweepStore() error = %v", err)
	}
	role := &wakeCapture{}
	trigger := orchestrator.Trigger{
		Tasks:   developmentManagerSweep(),
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   role,
		Docket:  sweepDocket{docketer: docketerOverDocket(runs, docket)},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %d, want the one turn of the pass", len(role.messages))
	}
	message := role.messages[0]
	positions := make([]int, 0, 3)
	for index, item := range []string{"yoyodyne-ifd.430.13.4", "yoyodyne-ifd.428.29", "yoyodyne-ifd.429.21"} {
		run := fmt.Sprintf("run-%032x", index+1)
		at := strings.Index(message, "on "+item+" ("+run+")")
		if at < 0 {
			t.Fatalf("the pass was not handed the stoppage of %s (%s):\n%s", item, run, message)
		}
		positions = append(positions, at)
	}
	// Oldest first, as the conversation lists it.
	if positions[0] > positions[1] || positions[1] > positions[2] {
		t.Errorf("the stoppages are not listed oldest first: at %v", positions)
	}
	for _, want := range []string{
		"read for this pass",
		"## Triage docket",
		"sweep for unresolved issues",
		sweep.Fence,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the wake message does not carry %q:\n%s", want, message)
		}
	}
	// The docket is ahead of the task, so what the task asks her to look over is
	// what she has just been shown.
	if strings.Index(message, "## Triage docket") > strings.Index(message, "sweep for unresolved issues") {
		t.Errorf("the docket follows the task rather than preceding it:\n%s", message)
	}
}

// An empty docket is said in words rather than left out, so a pass can tell
// nothing waiting from nothing read; and a docket that could not be built is
// said as unread, never as empty.
func TestASweepsDocketSaysWhenItIsEmptyAndWhenItCouldNotBeRead(t *testing.T) {
	t.Parallel()

	runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	empty := sweepDocket{docketer: docketerOverDocket(runs, docket)}.Window()
	if !strings.Contains(empty, "Nothing is on the docket") {
		t.Errorf("an empty docket rendered as %q, want it said in words", empty)
	}

	unread := sweepDocket{docketer: failingDocketer{}}.Window()
	for _, want := range []string{"could not be read", "the docket log is locked", "Do not assume nothing has stopped"} {
		if !strings.Contains(unread, want) {
			t.Errorf("an unreadable docket rendered as %q, want %q", unread, want)
		}
	}
}

type failingDocketer struct{}

func (failingDocketer) Build() (orchestrator.DocketBuild, error) {
	return orchestrator.DocketBuild{}, errors.New("the docket log is locked")
}

// A product with a docket wires it into the trigger, and one without wires
// nothing rather than a docket that can only fail.
func TestTheTriggerCarriesTheProductsDocketWhereThereIsOne(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	parts := components{config: config.Config{RecurringTasks: developmentManagerSweep()}, store: store}
	if trigger := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger); trigger.Docket != nil {
		t.Errorf("docket = %#v, want none wired for a product with no docket", trigger.Docket)
	}
	parts.docket, err = runstate.NewDocketStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	if trigger := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger); trigger.Docket == nil {
		t.Error("the trigger carries no docket, so a scheduled pass of the development manager's would see none")
	}
}
