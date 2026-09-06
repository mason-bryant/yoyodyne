package cli

import (
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

// A project that has scheduled nothing gets no trigger at all, and the nil has to
// be one the pull can actually test.
//
// This is the typed-nil trap, and it is worth a test of its own because the
// failure is not a schedule that does nothing — it is a panic on every pull. A
// (*orchestrator.Trigger)(nil) returned into an interface is not equal to nil, so
// the pass would find a trigger, call Fire through a nil receiver, and take down
// the session for every project that never asked for a schedule.
func TestAProjectThatSchedulesNothingGetsNoTrigger(t *testing.T) {
	t.Parallel()

	if trigger := recurringTrigger(components{}, "", io.Discard); trigger != nil {
		t.Errorf("trigger = %#v, want an untyped nil a pull can test against", trigger)
	}
}

// A configured task gets a trigger wired to the product's own sweep store, which
// is the only path by which a task ever fires.
//
// Every field checked here is silent if it is missing: a trigger with no claims
// fires every pull instead of on its cadence, one with no reports produces
// nothing anybody can read afterwards, and one with no roles cannot wake anybody.
// None of those looks different from a schedule that has found nothing yet.
func TestAConfiguredTaskGetsATriggerOverTheProductsSweepStore(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	parts := components{
		config: config.Config{RecurringTasks: map[string]config.RecurringTask{
			"development-manager-sweep": {
				Role:    domain.RoleDevelopmentManager,
				Every:   config.Duration(time.Hour),
				Enabled: true,
				Prompt:  "sweep for unresolved issues",
			},
		}},
		store: store,
	}

	wired := recurringTrigger(parts, "", io.Discard)
	if wired == nil {
		t.Fatal("recurringTrigger() = nil, want a trigger for a project that configured a task")
	}
	trigger, ok := wired.(*orchestrator.Trigger)
	if !ok {
		t.Fatalf("recurringTrigger() = %T, want the orchestrator's trigger", wired)
	}
	if len(trigger.Tasks) != 1 {
		t.Errorf("tasks = %#v, want the configured schedule as this pull read it", trigger.Tasks)
	}
	if trigger.Claims == nil {
		t.Error("the trigger has nowhere to claim a cadence, so it would fire on every pull rather than every hour")
	}
	if trigger.Reports == nil {
		t.Error("the trigger has nowhere to record a pass, so what it found would reach nobody")
	}
	if trigger.Roles == nil {
		t.Error("the trigger has no way to reach a role's conversation, so it could never wake anybody")
	}
}

func recordedSweep(task string, at time.Time, result *sweep.Result, problem string) runstate.Sweep {
	return runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     "example",
		Task:          task,
		Role:          "development-manager",
		StartedAt:     at,
		EndedAt:       at.Add(time.Minute),
		Turns:         1,
		Result:        result,
		Problem:       problem,
	}
}

// A schedule that has produced nothing says so plainly. An empty listing that
// looked like a failure to read would send an operator looking for a fault in
// the schedule on the morning of the day they turned it on.
func TestSweepListingOfAnUnsweptProjectSaysSo(t *testing.T) {
	t.Parallel()

	if rendered := renderSweeps(nil, nil, defaultRenderedSweeps); !strings.Contains(rendered, "no recurring task has recorded a sweep") {
		t.Errorf("rendered = %q, want it to say nothing has swept yet", rendered)
	}
}

// Questions come first, because a report with none asks for nothing — which is
// what makes reading these at leisure possible at all.
func TestSweepListingPutsQuestionsAboveTheFindings(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	rendered := renderSweepsAtDefault([]runstate.Sweep{recordedSweep("a-sweep", at, &sweep.Result{
		Status:    sweep.StatusComplete,
		Summary:   "one thing fixed, one waiting on a ruling",
		Questions: []string{"should the release wait?"},
		Findings: []sweep.Finding{
			{Issue: "a dead claim", Disposition: sweep.DispositionFixed, Filed: []string{"yoyodyne-ifd.300"}},
		},
	}, "")})
	question := strings.Index(rendered, "should the release wait?")
	finding := strings.Index(rendered, "a dead claim")
	if question < 0 || finding < 0 {
		t.Fatalf("rendered = %q, want both the question and the finding", rendered)
	}
	if question > finding {
		t.Errorf("the question is shown below the findings:\n%s", rendered)
	}
	if !strings.Contains(rendered, "filed: yoyodyne-ifd.300") {
		t.Errorf("rendered = %q, want the work filed for the root cause named", rendered)
	}
}

// A fix that filed nothing is called what it is. It is the whole thing a week of
// these reports is read for, and a reader must not have to compare two lines to
// see it.
func TestSweepListingNamesASilentRepair(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	rendered := renderSweepsAtDefault([]runstate.Sweep{recordedSweep("a-sweep", at, &sweep.Result{
		Status:   sweep.StatusComplete,
		Summary:  "fixed it",
		Findings: []sweep.Finding{{Issue: "a stuck delivery", Disposition: sweep.DispositionFixed}},
	}, "")})
	if !strings.Contains(rendered, "fixed with nothing filed for the root cause") {
		t.Errorf("rendered = %q, want the silent repair named", rendered)
	}
}

// A firing that produced no account is not a quiet pass, and the listing must not
// let the two look alike.
func TestSweepListingTellsAFailedPassFromAQuietOne(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	failed := renderSweepsAtDefault([]runstate.Sweep{recordedSweep("a-sweep", at, nil, "the conversation could not be opened")})
	if !strings.Contains(failed, "no account of this pass was recorded") {
		t.Errorf("rendered = %q, want it to say the pass produced nothing", failed)
	}
	quiet := renderSweepsAtDefault([]runstate.Sweep{recordedSweep("a-sweep", at, &sweep.Result{
		Status:  sweep.StatusComplete,
		Summary: "nothing unresolved",
	}, "")})
	if strings.Contains(quiet, "no account of this pass") {
		t.Errorf("rendered = %q, want a quiet pass shown as its own summary", quiet)
	}
	if !strings.Contains(quiet, "nothing unresolved") {
		t.Errorf("rendered = %q, want the quiet pass's summary", quiet)
	}
}

// The most recent is what a reader wants from a schedule, and the count says how
// much of the pile they are not looking at.
func TestSweepListingShowsTheMostRecentFirst(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	var recorded []runstate.Sweep
	for i := 0; i < defaultRenderedSweeps+3; i++ {
		recorded = append(recorded, recordedSweep("a-sweep", at.Add(time.Duration(i)*time.Hour), &sweep.Result{
			Status:  sweep.StatusComplete,
			Summary: "pass " + string(rune('a'+i)),
		}, ""))
	}
	rendered := renderSweepsAtDefault(recorded)
	if !strings.Contains(rendered, "sweep(s) recorded; the most recent") {
		t.Errorf("rendered = %q, want it to say how much of the pile is not shown", rendered)
	}
	newest := "pass " + string(rune('a'+defaultRenderedSweeps+2))
	oldest := "pass a"
	if !strings.Contains(rendered, newest) {
		t.Errorf("rendered = %q, want the newest pass shown", rendered)
	}
	if strings.Contains(rendered, oldest+"\n") {
		t.Errorf("rendered = %q, want the oldest passes left out", rendered)
	}
}

// renderSweepsAtDefault is the listing as an operator gets it with no flags, which
// is what these tests are about.
func renderSweepsAtDefault(recorded []runstate.Sweep) string {
	return renderSweeps(recorded, nil, defaultRenderedSweeps)
}

// The default bound is what fits a terminal, not what the log holds. At the
// hourly cadence the recurring task ships with, twenty passes is under a day, and
// the question these reports exist for needs a week — so a reader has to be able
// to get past the default and has to be told the default is one.
func TestSweepListingReadsPastItsDefaultBound(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	week := 24 * 7
	var recorded []runstate.Sweep
	for i := 0; i < week; i++ {
		recorded = append(recorded, recordedSweep("a-sweep", at.Add(time.Duration(i)*time.Hour), &sweep.Result{
			Status:  sweep.StatusComplete,
			Summary: fmt.Sprintf("pass %d", i),
		}, ""))
	}
	oldest := "pass 0\n"

	bounded := renderSweeps(recorded, nil, defaultRenderedSweeps)
	if strings.Contains(bounded, oldest) {
		t.Errorf("the default listing reaches the whole week, so the bound says nothing:\n%s", bounded)
	}
	// A listing showing part of the pile has to say how to see the rest, or the
	// bound reads as the whole log and the schedule looks a day old.
	if !strings.Contains(bounded, "--limit 0 reads all of them") {
		t.Errorf("rendered = %q, want it to name the flag that reads further back", bounded)
	}

	whole := renderSweeps(recorded, nil, 0)
	if !strings.Contains(whole, oldest) {
		t.Errorf("--limit 0 does not reach the oldest pass of the week:\n%s", whole[:200])
	}
	if strings.Contains(whole, "the most recent") {
		t.Errorf("an unbounded listing still says it is showing part of the pile:\n%s", whole[:200])
	}

	if widened := renderSweeps(recorded, nil, week); !strings.Contains(widened, oldest) {
		t.Errorf("--limit %d does not reach the oldest pass of the week", week)
	}
}

// A torn line must not cost the reports around it, and must not vanish quietly
// either: a listing short by a record it never mentioned is a worse answer than
// the failure it replaced.
func TestSweepListingNamesALineItCouldNotReadAndShowsTheRest(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	rendered := renderSweeps(
		[]runstate.Sweep{recordedSweep("a-sweep", at, &sweep.Result{
			Status:  sweep.StatusComplete,
			Summary: "the pass that survived",
		}, "")},
		[]runstate.UnreadableSweep{{Line: 4, Problem: "unexpected end of JSON input"}},
		defaultRenderedSweeps,
	)
	if !strings.Contains(rendered, "line 4 of the sweep log could not be read") {
		t.Errorf("rendered = %q, want the unreadable line named", rendered)
	}
	if !strings.Contains(rendered, "the pass that survived") {
		t.Errorf("rendered = %q, want the readable reports shown beside it", rendered)
	}
}
