package readmodel

// What the harness got done over a day and a week, and what it cost.
//
// The standing status answers "where does the harness stand right now"; this
// answers the other question an operator glancing at a page asks, which is
// whether anything is getting done and what it is costing. It derives nothing
// of its own about either. The money is (*runstate.StreamStore).Spend's — the
// one call internal/cli/statusstream.go's reportSpend makes to price
// `yoyo status --spend` — asked once over the widest window and split here by
// the local day each row already carries; the endings are runstate.State.Outcome,
// the word `yoyo status` prints for each run, with a `succeeded` run counted as
// landed exactly where it carries a promotion; and the days are
// runstate.LocalDay, the spend report's own. So a day's cost here is the day's
// total the spend report prints, and a run counts as landed here exactly when
// the terminal says its work landed, because two surfaces disagreeing about what
// today cost is a disagreement only the operator can settle.
// TestThroughputPricesTheSameRecordsTheSpendReportPrices holds the first of
// those over a real state directory.
//
// Every figure names its window, and every total says what it does not cover.
// A window is local calendar days, today counting as the first of them, because
// that is the day an operator's own clock is keeping and the day the spend
// report already groups by; the two windows here are today, and today with the
// six days before it. A cost is a floor wherever a record that should be in it
// could not be read, and the count of what could not be read rides beside the
// figure rather than being dropped from it — the rule every cost surface in the
// harness holds, held here too.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Ledger is the recorded spend as `yoyo status --spend` prices it: every run,
// conversation, branch review, and exchange, by the local day the money was
// spent on. It is satisfied by *runstate.StreamStore.
type Ledger interface {
	Spend(runstate.SpendQuery) (runstate.SpendReport, error)
}

// ThroughputSources are the records one throughput reading is assembled from.
// Both are interfaces so the derivation can be exercised without a state
// directory, which is the only way a figure nobody may recompute per surface
// gets a fixture that holds it.
type ThroughputSources struct {
	// Runs is the durable run state, read for what each recorded run became and
	// when. A reading without one says so rather than reporting nothing landed,
	// and says RunsProblem where the caller carries the reason it could not open
	// the store: "permission denied" is what somebody acts on, and "nothing was
	// wired" is not it.
	Runs        Runs
	RunsProblem string
	// Ledger is the spend, and LedgerProblem the reason it could not be opened
	// where the caller has one. A reading without one says so rather than
	// reporting nothing spent.
	Ledger        Ledger
	LedgerProblem string
	// Now stamps the reading and anchors the windows. It defaults to the wall
	// clock and is injected so a test can pin a day.
	Now func() time.Time
}

func (s ThroughputSources) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// windows are the two windows every reading covers, in days: today, and today
// with the six days before it. They are the design's daily and weekly windows,
// counted the way the spend report counts a window so that the two agree.
var windows = []struct {
	label string
	days  int
}{
	{"today", 1},
	{"last 7 days", 7},
}

// Window is what happened in one window of local days, every figure labeled
// with what it covers.
type Window struct {
	// Label is the window in words, and Days how many local days it covers,
	// today counting as the first. Since is the first local day covered.
	Label string `json:"label"`
	Days  int    `json:"days"`
	Since string `json:"since"`

	// Started counts the runs that started inside the window, whatever became of
	// them. It is the attempt volume the endings below are read against.
	Started int `json:"started"`
	// The endings, by the outcome the run history prints for each run, counted
	// by when the run completed. They are five fields rather than a map so that
	// the vocabulary here is the run history's whole vocabulary and a reader can
	// see that it is: a run that ended is in exactly one of them.
	//
	// Landed is the run whose work reached the target branch — `succeeded` with a
	// promotion recorded — which is what the design's integrated-work total
	// counts. A run that succeeded without promoting anything, which is what an
	// escalation or a bootstrap run looks like in the record, is counted under
	// Succeeded and not here, because nothing reached the branch. An evidence
	// landing is not one of those: its change integrates exactly as a discharge
	// does, so it is counted here, and that the item stayed open is the tracker's
	// to say rather than this figure's.
	Landed    int `json:"landed"`
	Succeeded int `json:"succeeded"`
	Stopped   int `json:"stopped"`
	Cancelled int `json:"cancelled"`
	TimedOut  int `json:"timed_out"`
	Failed    int `json:"failed"`
	// LandedItems names each run Landed counts — the work item it landed, and
	// when — newest first, so a surface that opens the landed grouping lists the
	// runs the count was taken over rather than a second reading of the records.
	// It is nil where the runs could not be read, as the endings are nothing
	// then, and empty rather than absent otherwise.
	LandedItems []LandedRun `json:"landed_items"`

	// CostUSD is what every priced invocation in the window cost, across runs,
	// conversations, branch reviews, and exchanges alike, and Invocations how
	// many there were. Kinds splits both by what was invoked, in the order the
	// spend report prices them.
	CostUSD     float64     `json:"cost_usd"`
	Invocations int         `json:"invocations"`
	Kinds       []KindSpend `json:"kinds"`
	// Unpriced counts the records the window should cover and could not read,
	// which is the exchange records the spend report names as unreadable. While
	// it is non-zero the cost is a floor, and Floor says so in one word so a page
	// does not have to know why.
	Unpriced int  `json:"unpriced"`
	Floor    bool `json:"floor"`
}

// LandedRun is one run whose work reached the target branch inside a window:
// the item it landed, by id and by the title the run recorded at its claim, and
// when it ended.
type LandedRun struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Title      string    `json:"title,omitempty"`
	LandedAt   time.Time `json:"landed_at"`
}

// KindSpend is one kind's share of a window's cost.
type KindSpend struct {
	Kind        runstate.StreamKind `json:"kind"`
	Invocations int                 `json:"invocations"`
	CostUSD     float64             `json:"cost_usd"`
}

// Throughput is the reading: the two windows, and what could not be read. It is
// carried whole for the surfaces that project the model — the dashboard's
// throughput section and the tiles above it — and it never fails as a whole: a
// source that cannot be read costs its own figures and leaves the other's,
// saying so in its problem rather than reporting zero.
type Throughput struct {
	ObservedAt time.Time `json:"observed_at"`
	Windows    []Window  `json:"windows"`
	// RunsProblem is set where the run records could not be read; the endings
	// in every window are then nothing rather than zero, and this says why.
	RunsProblem string `json:"runs_problem,omitempty"`
	// SpendProblem is set where the spend could not be read; the costs in every
	// window are then nothing rather than zero, and this says why.
	SpendProblem string `json:"spend_problem,omitempty"`
}

// ReadThroughput assembles the two windows from the durable records.
func ReadThroughput(ctx context.Context, sources ThroughputSources) Throughput {
	now := sources.now()
	reading := Throughput{ObservedAt: now, Windows: make([]Window, 0, len(windows))}
	for _, window := range windows {
		reading.Windows = append(reading.Windows, Window{
			Label: window.label,
			Days:  window.days,
			Since: firstLocalDay(now, window.days),
			Kinds: []KindSpend{},
		})
	}

	var recorded []runstate.State
	switch {
	case sources.Runs == nil:
		reading.RunsProblem = absent("the recorded runs", sources.RunsProblem)
	default:
		states, err := sources.Runs.Recorded()
		if err != nil {
			reading.RunsProblem = fmt.Sprintf("the recorded runs could not be read: %v", err)
		} else {
			recorded = states
		}
	}

	// The spend is read once, over the widest window, and each window takes the
	// rows that fall inside it: every row carries the local day it was spent on,
	// and pricing the streams costs a read of every event log, which is not a
	// thing to do once per window on a page that asks every minute.
	var report runstate.SpendReport
	switch {
	case ctx.Err() != nil:
		reading.SpendProblem = fmt.Sprintf("the spend was not read: %v", ctx.Err())
	case sources.Ledger == nil:
		reading.SpendProblem = absent("the spend", sources.LedgerProblem)
	default:
		widest := 0
		for _, window := range windows {
			widest = max(widest, window.days)
		}
		priced, err := sources.Ledger.Spend(runstate.SpendQuery{Days: widest, Now: now})
		if err != nil {
			reading.SpendProblem = fmt.Sprintf("the spend could not be read: %v", err)
		} else {
			report = priced
		}
	}

	for index := range reading.Windows {
		window := &reading.Windows[index]
		if reading.RunsProblem == "" {
			countEndings(window, recorded, now)
		}
		if reading.SpendProblem == "" {
			sumSpend(window, report)
		}
	}
	return reading
}

// absent is what a reading says about a source it was not given: the reason
// the caller could not open it where the caller carried one, and that nothing
// was wired otherwise. The two are different things to do about.
func absent(what, problem string) string {
	if problem != "" {
		return what + " could not be opened: " + problem
	}
	return "nothing was wired to read " + what
}

// countEndings counts the recorded runs into one window: by start for the
// attempt volume, and by completion for the endings, in the run history's own
// vocabulary. A run still in flight has not ended and is counted nowhere here;
// the standing status is what counts it.
func countEndings(window *Window, recorded []runstate.State, now time.Time) {
	since := startOfLocalDay(now, window.Days)
	window.LandedItems = []LandedRun{}
	for _, state := range recorded {
		if !state.StartedAt.Before(since) && !state.StartedAt.After(now) {
			window.Started++
		}
		if !state.Status.Terminal() {
			continue
		}
		ended := endedAt(state)
		if ended.Before(since) || ended.After(now) {
			continue
		}
		switch state.Outcome() {
		case runstate.OutcomeSucceeded:
			if state.Integration != nil {
				window.Landed++
				window.LandedItems = append(window.LandedItems, LandedRun{RunID: state.RunID, WorkItemID: state.WorkItemID, Title: state.WorkItemTitle, LandedAt: ended})
			} else {
				window.Succeeded++
			}
		case runstate.OutcomeStopped:
			window.Stopped++
		case runstate.OutcomeCancelled:
			window.Cancelled++
		case runstate.OutcomeTimedOut:
			window.TimedOut++
		default:
			window.Failed++
		}
	}
	// Newest first, by identifier where two landed at one instant, so two
	// readings of one directory list the landed work in one order.
	sort.SliceStable(window.LandedItems, func(first, second int) bool {
		if !window.LandedItems[first].LandedAt.Equal(window.LandedItems[second].LandedAt) {
			return window.LandedItems[first].LandedAt.After(window.LandedItems[second].LandedAt)
		}
		return window.LandedItems[first].RunID < window.LandedItems[second].RunID
	})
}

// endedAt is when a terminal run ended: its completion where the record has
// one, and the last time the record moved otherwise, which is what a run that
// died without writing a completion leaves.
func endedAt(state runstate.State) time.Time {
	if state.CompletedAt != nil && !state.CompletedAt.IsZero() {
		return *state.CompletedAt
	}
	if !state.UpdatedAt.IsZero() {
		return state.UpdatedAt
	}
	return state.StartedAt
}

// sumSpend takes the window's figures off the spend report: the rows from the
// window's first day on, by the report's own rule for which rows a window
// holds, added up by the report's own summation — the one `yoyo status --spend`
// prints its total and split from, so the split reads the same here as there.
func sumSpend(window *Window, report runstate.SpendReport) {
	inside := report.Since(window.Since)
	totals := inside.Totals()
	window.CostUSD = totals.CostUSD
	window.Invocations = totals.Calls
	for _, share := range totals.ByKind {
		window.Kinds = append(window.Kinds, KindSpend{Kind: share.Kind, Invocations: share.Calls, CostUSD: share.CostUSD})
	}
	window.Unpriced = len(inside.UnreadableExchanges)
	window.Floor = inside.Floor()
}

// startOfLocalDay is the first instant of the window: local midnight at the
// start of the oldest day it covers, stepping back by calendar days rather than
// by multiples of twenty-four hours so a daylight-saving shift cannot move it.
func startOfLocalDay(now time.Time, days int) time.Time {
	local := now.Local()
	return time.Date(local.Year(), local.Month(), local.Day()-(days-1), 0, 0, 0, 0, local.Location())
}

// firstLocalDay is the same day as the spend report names it.
func firstLocalDay(now time.Time, days int) string {
	return runstate.LocalDay(startOfLocalDay(now, days))
}
