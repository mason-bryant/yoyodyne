package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// recurringAmendments is the amendment log as a test hands it to the trigger.
type recurringAmendments struct {
	records []amendment.Record
	fail    error
}

func (a recurringAmendments) List() ([]amendment.Record, error) { return a.records, a.fail }

// proposedToTheArchitect is one undecided change against a design, raised at
// the given offset before the trigger's clock, with a distinct id per index.
func proposedToTheArchitect(index int, age time.Duration) amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            fmt.Sprintf("amendment-%032x", index),
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         fmt.Sprintf("run-%032x", index),
		WorkItemID:    fmt.Sprintf("yoyodyne-ifd.%d", 400+index),
		ProductID:     "example",
		RepositoryID:  "example",
		Artifact:      "v1-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        fmt.Sprintf("change %d", index),
		Why:           fmt.Sprintf("reason %d", index),
		RaisedAt:      recurringNow.Add(-age),
	}
}

func architectTask() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"architect-amendments": {
			Role:     domain.RoleArchitect,
			Every:    config.Duration(6 * time.Hour),
			Enabled:  true,
			Prompt:   "argue the proposed changes",
			MaxTurns: 2,
		},
	}
}

func recommendation(proposal amendment.Proposal, verdict sweep.Recommended, reason string) sweep.Recommendation {
	return sweep.Recommendation{Proposal: proposal.ID, Verdict: verdict, Reason: reason}
}

// The architect is woken with the undecided changes proposed to her documents,
// oldest first, told the recommendation contract, and what she recommends on
// each is on the firing's durable record as one batch.
func TestAnOwningRoleIsPutTheUndecidedProposalsAndItsRecommendationsAreRecorded(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	newer := proposedToTheArchitect(2, 2*24*time.Hour)
	older := proposedToTheArchitect(1, 20*24*time.Hour)
	role := &wokenRole{answers: []scriptedTurn{{result: &sweep.Result{
		Status:  sweep.StatusComplete,
		Summary: "two argued",
		Recommendations: []sweep.Recommendation{
			recommendation(older, sweep.RecommendApprove, "the design does not say which ordering holds"),
			recommendation(newer, sweep.RecommendDecline, "the goal it cites was retired"),
		},
	}}}}
	trigger := Trigger{
		Tasks: architectTask(), Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
		// Raised newest first in the log's order, so oldest-first has to be a
		// choice rather than an accident of the listing.
		Amendments: recurringAmendments{records: []amendment.Record{{Proposal: &newer}, {Proposal: &older}}},
	}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Recommendations != 2 || fired.Fired[0].Problem != "" {
		t.Fatalf("fired = %+v, want two recommendations and no problem", fired.Fired)
	}
	message := role.messages[0]
	for _, want := range []string{
		"# Changes proposed to documents you own",
		"puts 2 of them to you here, oldest first",
		older.ID, newer.ID,
		"yoyo amendment",
		"under the architect's authority",
		"argue the proposed changes",
		`"recommendations"`,
		"not decisions",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the wake does not carry %q:\n%s", want, message)
		}
	}
	if strings.Index(message, older.ID) > strings.Index(message, newer.ID) {
		t.Errorf("the wake puts the newer proposal ahead of the older one:\n%s", message)
	}
	// The proposals go ahead of the project's own prompt, so what she has to
	// look at arrives before what she is told to do about it.
	if strings.Index(message, older.ID) > strings.Index(message, "argue the proposed changes") {
		t.Errorf("the proposals come after the prompt:\n%s", message)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil || len(recorded[0].Result.Recommendations) != 2 {
		t.Fatalf("recorded = %+v, want the batch on the durable report", recorded)
	}
	if !strings.Contains(fired.Render(), "recommended on 2 proposed change(s)") {
		t.Errorf("Render() = %q, want the batch said", fired.Render())
	}
}

// A proposal the role already argued on an earlier pass is not put to it again
// while the operator has not decided it: the pass moves on to what has not
// been argued, and says how many await the operator.
func TestAProposalAlreadyArguedIsNotPutAgain(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	argued := proposedToTheArchitect(1, 20*24*time.Hour)
	fresh := proposedToTheArchitect(2, 2*24*time.Hour)
	amendments := recurringAmendments{records: []amendment.Record{{Proposal: &argued}, {Proposal: &fresh}}}
	clock := &movingRecurringClock{now: recurringNow}
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusComplete, Summary: "one argued",
			Recommendations: []sweep.Recommendation{recommendation(argued, sweep.RecommendApprove, "yes")}}},
		{result: &sweep.Result{Status: sweep.StatusComplete, Summary: "one more argued",
			Recommendations: []sweep.Recommendation{recommendation(fresh, sweep.RecommendDecline, "no")}}},
	}}
	trigger := Trigger{Tasks: architectTask(), Claims: store, Reports: store, Roles: role, Clock: clock, Amendments: amendments}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("first Fire() error = %v", err)
	}
	clock.now = recurringNow.Add(7 * time.Hour)
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(role.messages) != 2 {
		t.Fatalf("messages = %d, want two firings", len(role.messages))
	}
	second := role.messages[1]
	if strings.Contains(second, argued.ID) {
		t.Errorf("the second wake puts the argued proposal to her again:\n%s", second)
	}
	if !strings.Contains(second, fresh.ID) || !strings.Contains(second, "1 already carry your recommendation from an earlier pass and await the operator's decision") {
		t.Errorf("the second wake does not carry the fresh proposal and the count awaiting the operator:\n%s", second)
	}

	// Once everything undecided has been argued, the wake says so rather than
	// putting an empty list and a contract for filling it.
	clock.now = recurringNow.Add(14 * time.Hour)
	role.answers = []scriptedTurn{{result: complete("nothing to argue")}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("third Fire() error = %v", err)
	}
	third := role.messages[2]
	if !strings.Contains(third, "already carries your recommendation from an earlier pass — 2 of them await the operator's decision") {
		t.Errorf("the third wake does not say the queue is all argued:\n%s", third)
	}
	if strings.Contains(third, fresh.ID) {
		t.Errorf("the third wake puts an argued proposal to her again:\n%s", third)
	}
}

// A batch is bounded to what one pass can argue, and the wake says how many
// wait behind it; the next firing takes the next slice.
func TestABatchIsBoundedPerFiring(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	var records []amendment.Record
	for i := 1; i <= sweep.MaxRecommendations+2; i++ {
		proposal := proposedToTheArchitect(i, time.Duration(30-i)*24*time.Hour)
		records = append(records, amendment.Record{Proposal: &proposal})
	}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}}}
	trigger := Trigger{Tasks: architectTask(), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}, Amendments: recurringAmendments{records: records}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	message := role.messages[0]
	if !strings.Contains(message, fmt.Sprintf("puts %d of them to you here", sweep.MaxRecommendations)) || !strings.Contains(message, "2 more wait behind these") {
		t.Errorf("the wake does not say the bound and what waits behind it:\n%s", message)
	}
	// The oldest are the first ten; the two newest wait.
	for i := 1; i <= sweep.MaxRecommendations; i++ {
		if !strings.Contains(message, proposedToTheArchitect(i, 0).ID) {
			t.Errorf("proposal %d, among the oldest, is not put to her", i)
		}
	}
	for i := sweep.MaxRecommendations + 1; i <= sweep.MaxRecommendations+2; i++ {
		if strings.Contains(message, proposedToTheArchitect(i, 0).ID) {
			t.Errorf("proposal %d, among the newest, is put to her past the bound", i)
		}
	}
}

// A recommendation on a proposal that was not undecided against the role's
// documents — decided already, or never raised — stays on the account and is
// named on the record beside it as one the operator is not put.
func TestARecommendationOnNothingUndecidedIsNamedOnTheRecord(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	pending := proposedToTheArchitect(1, 5*24*time.Hour)
	decided := proposedToTheArchitect(2, 9*24*time.Hour)
	decision, err := decided.Decide(amendment.VerdictDeclined, amendment.DeciderOperator, "already settled", recurringNow.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	role := &wokenRole{answers: []scriptedTurn{{result: &sweep.Result{Status: sweep.StatusComplete, Summary: "argued",
		Recommendations: []sweep.Recommendation{
			recommendation(pending, sweep.RecommendApprove, "yes"),
			recommendation(decided, sweep.RecommendApprove, "also yes"),
		}}}}}
	trigger := Trigger{Tasks: architectTask(), Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
		Amendments: recurringAmendments{records: []amendment.Record{{Proposal: &pending}, {Proposal: &decided}, {Decision: &decision}}}}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if strings.Contains(role.messages[0], decided.ID) {
		t.Errorf("a decided proposal was put to her:\n%s", role.messages[0])
	}
	problem := fired.Fired[0].Problem
	if !strings.Contains(problem, "1 recommendation(s) name proposed changes that were not undecided") || !strings.Contains(problem, decided.ID) {
		t.Errorf("problem = %q, want the stray recommendation named", problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded[0].Result.Recommendations) != 2 {
		t.Errorf("recorded = %+v, want the account kept as the role gave it", recorded[0].Result)
	}
}

// A role that owns no documents is told nothing about proposals, whatever is
// pending against somebody else's; and a log that cannot be read puts none and
// says so on the record rather than reading as an empty queue.
func TestOnlyAnOwnerIsPutProposalsAndAnUnreadableLogSaysSo(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	pending := proposedToTheArchitect(1, 5*24*time.Hour)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
		Amendments: recurringAmendments{records: []amendment.Record{{Proposal: &pending}}}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if strings.Contains(role.messages[0], "Changes proposed to documents you own") || strings.Contains(role.messages[0], `"recommendations"`) {
		t.Errorf("the development manager was told about the architect's queue:\n%s", role.messages[0])
	}

	unreadable := sweepStore(t)
	woken := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	failing := Trigger{Tasks: architectTask(), Claims: unreadable, Reports: unreadable, Roles: woken, Clock: recurringClock{},
		Amendments: recurringAmendments{fail: errors.New("the log is torn")}}
	fired, err := failing.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if !strings.Contains(fired.Fired[0].Problem, "the proposed changes could not be read, so none were put to the architect") {
		t.Errorf("problem = %q, want the unreadable log named", fired.Fired[0].Problem)
	}
	if strings.Contains(woken.messages[0], "Changes proposed to documents you own") {
		t.Errorf("an unreadable log put a section to her:\n%s", woken.messages[0])
	}
}
