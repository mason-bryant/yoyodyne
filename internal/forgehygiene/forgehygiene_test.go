package forgehygiene

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeForge is a forge holding a fixed set of open requests, and answering the
// containment question from a fixed set of commits its base already carries.
type fakeForge struct {
	open      []publish.PullRequest
	contained map[string]bool
	compared  []string
	listErr   error
}

func (f *fakeForge) ListOpen(context.Context) ([]publish.PullRequest, error) {
	return f.open, f.listErr
}

func (f *fakeForge) Contains(_ context.Context, base, commit string) (bool, error) {
	f.compared = append(f.compared, base+"..."+commit)
	return f.contained[commit], nil
}

type fakeTracker struct {
	closed []beads.WorkItem
	err    error
	calls  int
}

func (f *fakeTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	f.calls++
	if status != "closed" {
		return nil, errors.New("only the closed work is read")
	}
	return f.closed, f.err
}

type fakeRuns struct{ states []runstate.State }

func (f fakeRuns) Recorded() ([]runstate.State, error) { return f.states, nil }

const (
	mergedCommit = "1111111111111111111111111111111111111111"
	liveCommit   = "2222222222222222222222222222222222222222"
	staleCommit  = "3333333333333333333333333333333333333333"
)

// threeRequests is the forge the work item describes: one request whose item is
// closed, one whose branch main already carries, and one live one.
func threeRequests() *fakeForge {
	return &fakeForge{
		open: []publish.PullRequest{
			{Number: 445, URL: "https://forge.invalid/pull/445", HeadBranch: "yoyodyne/yoyodyne-ifd-283/aaaaaaaa", BaseBranch: "main", HeadCommit: staleCommit},
			{Number: 460, URL: "https://forge.invalid/pull/460", HeadBranch: "yoyodyne/yoyodyne-ifd-300/bbbbbbbb", BaseBranch: "main", HeadCommit: mergedCommit},
			{Number: 470, URL: "https://forge.invalid/pull/470", HeadBranch: "yoyodyne/yoyodyne-ifd-310/cccccccc", BaseBranch: "main", HeadCommit: liveCommit},
		},
		contained: map[string]bool{mergedCommit: true},
	}
}

func TestAPassReportsAClosedItemAndAContainedBranchAndNotALiveRequest(t *testing.T) {
	t.Parallel()

	forge := threeRequests()
	tracker := &fakeTracker{closed: []beads.WorkItem{{ID: "yoyodyne-ifd.283", Status: "closed"}}}
	sweeper := Sweeper{Forge: forge, Tracker: tracker}

	notices, err := sweeper.Notice(context.Background(), nil)
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %+v, want the two requests held open for nothing", notices)
	}
	closedItem, contained := notices[0], notices[1]
	if closedItem.Number != 445 || !closedItem.ItemClosed || closedItem.Contained {
		t.Errorf("notice = %+v, want #445 reported for its closed item and nothing else", closedItem)
	}
	// The branch names the item in the form a branch carries it; the notice
	// names it in the tracker's own.
	if closedItem.WorkItemID != "yoyodyne-ifd.283" {
		t.Errorf("work item = %q, want the tracker's identifier", closedItem.WorkItemID)
	}
	if contained.Number != 460 || contained.ItemClosed || !contained.Contained {
		t.Errorf("notice = %+v, want #460 reported for its contained branch and nothing else", contained)
	}
	if contained.WorkItemID != "yoyodyne-ifd-300" {
		t.Errorf("work item = %q, want the item as the branch names it, there being no run record and no closed item", contained.WorkItemID)
	}
	for _, notice := range notices {
		if notice.Number == 470 {
			t.Errorf("the live request #470 was reported: %+v", notice)
		}
	}
	if tracker.calls != 1 {
		t.Errorf("the tracker was read %d times, want once per pass", tracker.calls)
	}
	// The findings name the request, the item, and the condition.
	if issue := closedItem.Finding().Issue; !strings.Contains(issue, "#445") || !strings.Contains(issue, "yoyodyne-ifd.283") || !strings.Contains(issue, "is closed") {
		t.Errorf("finding = %q, want the request, the item, and the closed condition", issue)
	}
	if issue := contained.Finding().Issue; !strings.Contains(issue, "#460") || !strings.Contains(issue, "already contained in main") {
		t.Errorf("finding = %q, want the request and the contained condition", issue)
	}
}

// A request already reported is not read again: it is left out before the
// tracker or the comparison is consulted, so a second pass over the same forge
// reports nothing and costs nothing.
func TestARequestAlreadyReportedIsNotReportedAgain(t *testing.T) {
	t.Parallel()

	forge := threeRequests()
	tracker := &fakeTracker{closed: []beads.WorkItem{{ID: "yoyodyne-ifd.283", Status: "closed"}}}
	sweeper := Sweeper{Forge: forge, Tracker: tracker}

	notices, err := sweeper.Notice(context.Background(), map[int]bool{445: true, 460: true})
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 0 {
		t.Fatalf("notices = %+v, want nothing on a pass over requests already reported", notices)
	}
	for _, compared := range forge.compared {
		if strings.HasSuffix(compared, mergedCommit) || strings.HasSuffix(compared, staleCommit) {
			t.Errorf("a reported request was compared again: %s", compared)
		}
	}
}

// The run record names the item exactly, and is preferred to the branch, which
// carries the identifier in a form that cannot always be turned back.
func TestTheRunRecordNamesTheItemAheadOfTheBranch(t *testing.T) {
	t.Parallel()

	forge := &fakeForge{
		open: []publish.PullRequest{
			{Number: 500, HeadBranch: "yoyodyne/yoyodyne-ifd-68-9/dddddddd", BaseBranch: "main", HeadCommit: mergedCommit},
		},
		contained: map[string]bool{mergedCommit: true},
	}
	runs := fakeRuns{states: []runstate.State{{
		RunID:       "run-dddddddd",
		WorkItemID:  "yoyodyne-ifd.68.9",
		Branch:      "yoyodyne/yoyodyne-ifd-68-9/dddddddd",
		PullRequest: &runstate.PullRequest{Number: 500},
	}}}
	sweeper := Sweeper{Forge: forge, Tracker: &fakeTracker{}, Runs: runs}

	notices, err := sweeper.Notice(context.Background(), nil)
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 1 || notices[0].WorkItemID != "yoyodyne-ifd.68.9" {
		t.Fatalf("notices = %+v, want the item named as the run record has it", notices)
	}
}

// A request meeting both conditions is one notice carrying both, because it is
// reported once, keyed on the request.
func TestARequestMeetingBothConditionsIsOneNotice(t *testing.T) {
	t.Parallel()

	forge := &fakeForge{
		open:      []publish.PullRequest{{Number: 7, HeadBranch: "yoyodyne/yoyodyne-ifd-1/eeeeeeee", BaseBranch: "main", HeadCommit: mergedCommit}},
		contained: map[string]bool{mergedCommit: true},
	}
	tracker := &fakeTracker{closed: []beads.WorkItem{{ID: "yoyodyne-ifd.1", Status: "closed"}}}
	notices, err := Sweeper{Forge: forge, Tracker: tracker}.Notice(context.Background(), nil)
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 1 || !notices[0].ItemClosed || !notices[0].Contained {
		t.Fatalf("notices = %+v, want one notice carrying both conditions", notices)
	}
	issue := notices[0].Finding().Issue
	if !strings.Contains(issue, "is closed") || !strings.Contains(issue, "already contained") {
		t.Errorf("finding = %q, want both conditions stated", issue)
	}
}

// A forge that cannot be listed, or a tracker that cannot be read, is an error
// rather than a quiet pass: a pass that found nothing and a pass that could not
// look must not be the same record.
func TestAReadingThatCannotBeMadeIsAnErrorNotSilence(t *testing.T) {
	t.Parallel()

	unlisted := Sweeper{Forge: &fakeForge{listErr: errors.New("gh: not logged in")}, Tracker: &fakeTracker{}}
	if _, err := unlisted.Notice(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("Notice() over an unlistable forge error = %v, want the forge's refusal", err)
	}
	unread := Sweeper{Forge: threeRequests(), Tracker: &fakeTracker{err: errors.New("bd: database locked")}}
	if _, err := unread.Notice(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "database locked") {
		t.Errorf("Notice() over an unreadable tracker error = %v, want the tracker's refusal", err)
	}
}

// pagingBD stands in for the bd binary as `bd list` actually behaves: it holds
// every closed item, newest first, and hands back the first fifty of them unless
// the command line says otherwise — `--limit=0` being its word for the whole set.
// It is the tracker the real beads.Client is put over here, so what this pins
// is the client's command line and not a fake's generosity.
type pagingBD struct {
	closed []bdRow
	args   [][]string
}

// bdRow is a work item as `bd list --json` emits it, in the fields the pass reads.
type bdRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// bdDefaultListLimit is the page `bd list` returns when nothing lifts it.
const bdDefaultListLimit = 50

func (p *pagingBD) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	p.args = append(p.args, append([]string(nil), command.Args...))
	if len(command.Args) == 0 || command.Args[0] != "list" {
		return execution.ProcessResult{}, fmt.Errorf("unexpected bd command %v", command.Args)
	}
	limit := bdDefaultListLimit
	status := ""
	for _, arg := range command.Args[1:] {
		switch {
		case strings.HasPrefix(arg, "--limit="):
			parsed, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil {
				return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "invalid --limit"}, nil
			}
			limit = parsed
		case strings.HasPrefix(arg, "--status="):
			status = strings.TrimPrefix(arg, "--status=")
		}
	}
	if status != "closed" {
		return execution.ProcessResult{}, fmt.Errorf("only the closed work is read, not %q", status)
	}
	page := p.closed
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

// The tracker is read whole rather than by its first page. The forge holds a
// request superseded long ago — its item is closed, and it is the oldest of more
// closed items than bd lists by default — and the pass names it as held open for
// nothing rather than passing over it as live. A pass that took bd's default page
// would miss it, and say so again every hour: the request is never reported,
// so nothing records it as reported, so every pass looks at it afresh.
func TestTheWholeClosedSetIsReadSoAnOldSupersededRequestIsNotTakenForLive(t *testing.T) {
	t.Parallel()

	const closedItems = 3 * bdDefaultListLimit
	closed := make([]bdRow, 0, closedItems)
	// Newest first, as a listing sorts them, so the request's item is the last
	// one a page would reach.
	for i := closedItems; i >= 1; i-- {
		closed = append(closed, bdRow{ID: fmt.Sprintf("yoyodyne-ifd.%d", i), Title: "closed work", Status: "closed"})
	}
	bd := &pagingBD{closed: closed}
	forge := &fakeForge{
		open: []publish.PullRequest{
			{Number: 12, URL: "https://forge.invalid/pull/12", HeadBranch: "yoyodyne/yoyodyne-ifd-1/ffffffff", BaseBranch: "main", HeadCommit: staleCommit},
			{Number: 470, URL: "https://forge.invalid/pull/470", HeadBranch: "yoyodyne/yoyodyne-ifd-310/cccccccc", BaseBranch: "main", HeadCommit: liveCommit},
		},
	}
	sweeper := Sweeper{Forge: forge, Tracker: beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}}

	notices, err := sweeper.Notice(context.Background(), nil)
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 1 || notices[0].Number != 12 || !notices[0].ItemClosed || notices[0].WorkItemID != "yoyodyne-ifd.1" {
		t.Fatalf("notices = %+v, want the superseded #12 reported for its closed item yoyodyne-ifd.1, which is past bd's default page", notices)
	}
	for _, notice := range notices {
		if notice.Number == 470 {
			t.Errorf("the live request #470 was reported: %+v", notice)
		}
	}
	if len(bd.args) != 1 {
		t.Fatalf("bd was run %d times with %v, want one listing of the closed work", len(bd.args), bd.args)
	}

	// A later pass, given what this one reported, has nothing left to say about
	// #12 — and would have had, every hour, off a page.
	later, err := sweeper.Notice(context.Background(), map[int]bool{12: true})
	if err != nil {
		t.Fatalf("Notice() on the later pass error = %v", err)
	}
	if len(later) != 0 {
		t.Fatalf("later pass notices = %+v, want nothing: #12 was reported and #470 is live", later)
	}
}

// A request nothing here opened has no item to read, and can still be reported
// for a branch its base already carries — that is a fact about the forge.
func TestARequestNobodyHereOpenedIsReportedOnlyForItsBranch(t *testing.T) {
	t.Parallel()

	forge := &fakeForge{
		open:      []publish.PullRequest{{Number: 9, HeadBranch: "feature/by-hand", BaseBranch: "main", HeadCommit: mergedCommit}},
		contained: map[string]bool{mergedCommit: true},
	}
	notices, err := Sweeper{Forge: forge, Tracker: &fakeTracker{}}.Notice(context.Background(), nil)
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if len(notices) != 1 || notices[0].WorkItemID != "" || !notices[0].Contained {
		t.Fatalf("notices = %+v, want the request reported for its branch with no item named", notices)
	}
	if issue := notices[0].Finding().Issue; !strings.Contains(issue, "no work item") {
		t.Errorf("finding = %q, want it to say no item could be named", issue)
	}
}
