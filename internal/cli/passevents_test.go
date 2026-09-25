package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var passWindowStart = time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

// Admissions are read from the tracker's export by when each item was
// created: the thirty created inside the window, and nothing created before it,
// with an unreadable line and a record that is not an item set aside.
func TestAdmissionsAreTheItemsTheExportRecordsAsCreatedInTheWindow(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf(`{"_type":"issue","id":"yoyodyne-ifd.1","title":"before","created_at":%q}`, passWindowStart.Add(-time.Minute).Format(time.RFC3339Nano)))
	for index := 1; index <= 30; index++ {
		lines = append(lines, fmt.Sprintf(`{"_type":"issue","id":"yoyodyne-ifd.1%02d","title":"item %d","created_at":%q}`, index, index, passWindowStart.Add(time.Minute).Format(time.RFC3339Nano)))
	}
	lines = append(lines, "{not json", fmt.Sprintf(`{"_type":"memory","id":"m-1","created_at":%q}`, passWindowStart.Add(time.Minute).Format(time.RFC3339Nano)))
	if err := os.WriteFile(filepath.Join(repository, issuesExport), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	events, err := passEvents{repository: repository}.Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(events) != 30 {
		t.Fatalf("events = %d, want the thirty created inside the window", len(events))
	}
	for _, event := range events {
		if event.Class != config.TriggerAdmissions || event.Stream != runstate.PassStreamTracker || !strings.HasPrefix(event.Detail, "item ") || event.Key != event.Subject {
			t.Fatalf("event = %+v, want an admission read from the tracker", event)
		}
	}
}

// A repository with a tracker and no export is a stream that could not be
// read, not a quiet one; a repository with no tracker has admitted nothing.
func TestAMissingExportIsUnreadableOnlyWhereThereIsATracker(t *testing.T) {
	t.Parallel()

	bare := t.TempDir()
	if events, err := (passEvents{repository: bare}).Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour)); err != nil || len(events) != 0 {
		t.Fatalf("Events() = %v, %v; want nothing admitted where there is no tracker", events, err)
	}
	tracked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tracked, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (passEvents{repository: tracked}).Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour)); err == nil {
		t.Fatal("Events() error = nil, want a tracker with no export reported")
	}
}

// Landings and stoppages are read from the run records by when each run
// ended: a run that landed its change, and one that stopped with a blocker;
// a run that succeeded without landing, one still running, and one that ended
// outside the window are none of them.
func TestRunEventsAreTheRunsThatLandedOrStoppedInTheWindow(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	inside := passWindowStart.Add(10 * time.Minute)

	landed := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.landed", inside)
	landed.WorkItemTitle = "the change that landed"
	landed.WorktreePath = filepath.Join(t.TempDir(), "worktree")
	landed.Branch = "yoyodyne/landed"
	landed.BaseCommit = substrateBase
	promoted(&landed)
	if err := store.Save(landed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stopped := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.stopped", inside)
	stopped.Blocker = "a configured check failed"
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.unlanded", inside)
	recordedRun(t, store, runstate.StatusRunning, "yoyodyne-ifd.running", inside)
	recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.before", passWindowStart.Add(-time.Minute))

	events, err := passEvents{runs: store}.Events(context.Background(), runstate.PassStreamRuns, passWindowStart, passWindowStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	classes := map[string]config.TriggerEvent{}
	for _, event := range events {
		classes[event.Subject] = event.Class
		if !strings.HasSuffix(event.Key, "/"+string(event.Class)) || !strings.HasPrefix(event.Key, "run-") {
			t.Errorf("key = %q, want the run and the class, so a record read again is known as carried", event.Key)
		}
	}
	if len(events) != 2 || classes["yoyodyne-ifd.landed"] != config.TriggerLandings || classes["yoyodyne-ifd.stopped"] != config.TriggerStoppages {
		t.Fatalf("events = %+v, want the landing and the stoppage alone", events)
	}
}

// `yoyo sweeps` says what a program manager's pass carried.
func TestASweepListingSaysWhatAPassCarried(t *testing.T) {
	t.Parallel()

	rendered := renderSweep(runstate.Sweep{
		Task:      "reliability-pm",
		Role:      "program-manager",
		StartedAt: passWindowStart,
		Turns:     1,
		Model:     "opus",
		Events:    map[string]int{"admissions": 30, "landings": 1},
		Problem:   "the role answered in prose",
	})
	if !strings.Contains(rendered, "reliability-pm (program-manager), 1 turn(s), on opus") || !strings.Contains(rendered, "carried 1 landing, 30 admissions since its last pass") {
		t.Fatalf("rendered = %q, want the pass's model and what it carried", rendered)
	}
}

// A program manager's pass opens the instance's own conversation, by the
// agent's name, rather than whichever agent fills the role first.
func TestAnInstancesPassOpensTheInstancesOwnConversation(t *testing.T) {
	t.Parallel()

	var opened []string
	open := func(_ context.Context, _ domain.AgentRole, agent, _ string) (*chat.Session, *runstate.ConversationHold, error) {
		opened = append(opened, agent)
		return nil, nil, errors.New("the provider is not signed in")
	}
	_, err := roleConversation{open: open}.Wake(context.Background(), domain.RoleProgramManager, "reliability-pm", "reliability-pm#1", "", "a pass")
	if !errors.Is(err, orchestrator.ErrRoleUnreachable) {
		t.Fatalf("Wake() error = %v, want the conversation reported unreachable", err)
	}
	if len(opened) != 1 || opened[0] != "reliability-pm" {
		t.Fatalf("opened = %v, want the instance's own conversation", opened)
	}
}
