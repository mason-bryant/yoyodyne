package readmodel

// How the queue of proposed changes stands, and which of them the owning role
// has already argued, derived once for every surface.
//
// Two things about the queue are waiting on a person and were said by nothing.
// The first is the queue itself getting old: forty-four proposals stood
// undecided against the architect's documents with the oldest weeks old, and
// the attention line named each one and folded most of them into "and 34
// things not named here", so a reader saw a list and never a queue that had
// stopped draining. The second is the owner's recommendations: a recurring pass
// puts the undecided proposals to the owning role and records what it argued,
// and that batch is the operator's to decide from — which nothing said either,
// because the sweep log is read by `yoyo sweeps` and nothing else.
//
// So both are derived here, from the amendment log and the sweep log and
// nowhere else. The count-and-age line is the sibling of the report pile's, on
// the same threshold and for the same reason; the recommendations are read
// off the recorded passes, latest first per proposal, and a proposal the
// operator has decided since is dropped from the batch wherever it is read —
// the account on the sweep record is the role's own words and is never
// rewritten, so dropping happens here rather than there.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// Sweeps is the recurring tasks' durable reports, read for the recommendations
// an owning role recorded on the proposed changes put to it. It is satisfied by
// *runstate.SweepStore.
type Sweeps interface {
	List() ([]runstate.Sweep, []runstate.UnreadableSweep, error)
}

// maxUndecidedAmendmentAge is how long the oldest undecided proposal may go
// unanswered before the queue is something waiting on a person rather than
// something a cadence is working through. It is the pile's threshold, because
// it catches the same failure: a cadence not running or not keeping up, which
// is invisible in any one reading and shows only as the oldest one's age
// climbing past anything a working cadence would leave.
const maxUndecidedAmendmentAge = maxUndecidedReportAge

// RecommendedAmendment is one undecided proposal the owning role has argued on
// a recurring pass: what it recommended, why, and where that is recorded. It
// is the unit of the batch the operator decides from, so a surface that puts
// the batch to him reads these rather than the sweep log.
type RecommendedAmendment struct {
	Proposal string           `json:"proposal"`
	Artifact string           `json:"artifact"`
	Owner    domain.AgentRole `json:"owner"`
	// Verdict, Reason, and Into are the recommendation as the role gave it.
	Verdict sweep.Recommended `json:"verdict"`
	Reason  string            `json:"reason"`
	Into    string            `json:"into,omitempty"`
	// Task is the recurring task whose pass recorded it and RecommendedAt when
	// that pass started, so `yoyo sweeps --task` finds the whole account.
	Task          string    `json:"task"`
	RecommendedAt time.Time `json:"recommended_at"`
}

// RecommendedAmendments is every undecided proposal the owning role has argued,
// oldest proposal first, each carrying the latest recommendation recorded on
// it. A recommendation on a proposal that is decided, or that nothing raised,
// names nothing waiting and is left out: the batch is what the operator still
// has to decide, not what the role once said. And a recommendation recorded on
// a pass of any role but the proposal's owner is left out too, whatever it
// says: the batch is the owner's argument, and a line that said "the architect
// recommends" over something another role wrote would be putting words in the
// one mouth the record says decides.
func RecommendedAmendments(sweeps []runstate.Sweep, records []amendment.Record) []RecommendedAmendment {
	pending := amendment.Pending(records)
	byID := make(map[string]amendment.Proposal, len(pending))
	for _, proposal := range pending {
		byID[proposal.ID] = proposal
	}
	latest := map[string]RecommendedAmendment{}
	for _, recorded := range sweeps {
		if recorded.Result == nil {
			continue
		}
		for _, recommendation := range recorded.Result.Recommendations {
			proposal, waiting := byID[recommendation.Proposal]
			if !waiting || recorded.Role != proposal.Owner {
				continue
			}
			// A later pass's recommendation on the same proposal stands over an
			// earlier one: the role may have changed its mind, and what the operator
			// acts on is its latest argument.
			if earlier, argued := latest[proposal.ID]; argued && !recorded.StartedAt.After(earlier.RecommendedAt) {
				continue
			}
			latest[proposal.ID] = RecommendedAmendment{
				Proposal:      proposal.ID,
				Artifact:      proposal.Artifact,
				Owner:         proposal.Owner,
				Verdict:       recommendation.Verdict,
				Reason:        strings.Join(strings.Fields(recommendation.Reason), " "),
				Into:          recommendation.Into,
				Task:          recorded.Task,
				RecommendedAt: recorded.StartedAt,
			}
		}
	}
	recommended := make([]RecommendedAmendment, 0, len(latest))
	for _, proposal := range pending {
		if entry, argued := latest[proposal.ID]; argued {
			recommended = append(recommended, entry)
		}
	}
	sort.SliceStable(recommended, func(i, j int) bool {
		return byID[recommended[i].Proposal].RaisedAt.Before(byID[recommended[j].Proposal].RaisedAt)
	})
	return recommended
}

// amendmentReading is the queue as one standing reading has it: the records,
// the summary, the batch the owners have argued, and what could not be read.
type amendmentReading struct {
	records     []amendment.Record
	queue       amendment.Queue
	recommended []RecommendedAmendment
	// problem is what stopped the queue being read, and sweepProblem what
	// stopped the recommendations: the two are said apart because the queue is
	// readable without the passes and a reader told neither concludes there is
	// nothing waiting.
	problem      string
	sweepProblem string
}

// readAmendments reads the proposed changes once for both lines that carry
// them. A log that could not be read says so and reports no queue at all: a
// zero here would read as nothing proposed, which is the one thing a broken
// read of it must never look like.
func readAmendments(sources Sources, now time.Time) amendmentReading {
	if sources.Amendments == nil {
		return amendmentReading{problem: "nothing was wired to read the proposed changes"}
	}
	records, err := sources.Amendments.List()
	if err != nil {
		return amendmentReading{problem: fmt.Sprintf("the proposed changes could not be read: %v", err)}
	}
	reading := amendmentReading{records: records, queue: amendment.Summarize(records, now)}
	if sources.Sweeps == nil {
		// Nothing wired to read the passes is the ordinary case for a fixture and
		// a surface older than the cadence, and it is not a problem to name: what
		// is lost is the owner's recommendations, which no surface without the
		// sweep log ever showed.
		return reading
	}
	sweeps, _, err := sources.Sweeps.List()
	if err != nil {
		// A scan that failed part way through returns what it read before the
		// failure; those passes are real and their recommendations stand. The
		// failure is said beside them rather than in place of them.
		reading.sweepProblem = fmt.Sprintf("the recurring passes could not be read to the end, so the owners' recommendations may be incomplete: %v", err)
	}
	reading.recommended = RecommendedAmendments(sweeps, records)
	return reading
}

// attention is the proposed changes as the attention line names them: the
// batch the owners have argued, said once as what the operator has to decide;
// then each undecided proposal, with the owner's recommendation beside the
// ones that carry one. The batch leads because it is the one entry here whose
// next move is the operator's alone.
func (r amendmentReading) attention() []Attention {
	var attention []Attention
	if len(r.recommended) > 0 {
		attention = append(attention, r.batchAttention())
	}
	recommended := make(map[string]RecommendedAmendment, len(r.recommended))
	for _, entry := range r.recommended {
		recommended[entry.Proposal] = entry
	}
	for _, proposal := range amendment.Pending(r.records) {
		what := fmt.Sprintf("a change to %s is proposed and undecided (%s)", proposal.Artifact, proposal.ID)
		if entry, argued := recommended[proposal.ID]; argued {
			what += fmt.Sprintf("; the %s recommends %s", proposal.Owner, entry.says())
		}
		attention = append(attention, Attention{
			What: what,
			Whose: fmt.Sprintf("the %s's — nothing reaches the document until they or the operator decide it",
				proposal.Owner),
		})
	}
	return attention
}

// says is the recommendation in a clause: the verdict, and for a merge what it
// merges into.
func (r RecommendedAmendment) says() string {
	if r.Verdict == sweep.RecommendMerge && r.Into != "" {
		return fmt.Sprintf("%s into %s", r.Verdict, r.Into)
	}
	return string(r.Verdict)
}

// batchAttention is the owners' recommendations as one entry: how many
// proposals carry one, split by verdict, which owner argued them, and where
// the reasons are. It is one entry rather than one per recommendation because
// it is one decision list, and the per-proposal entries below it already name
// each with its verdict.
func (r amendmentReading) batchAttention() Attention {
	counts := map[sweep.Recommended]int{}
	owners := map[domain.AgentRole]bool{}
	tasks := map[string]bool{}
	var latest time.Time
	for _, entry := range r.recommended {
		counts[entry.Verdict]++
		owners[entry.Owner] = true
		tasks[entry.Task] = true
		if entry.RecommendedAt.After(latest) {
			latest = entry.RecommendedAt
		}
	}
	var split []string
	for _, verdict := range []sweep.Recommended{sweep.RecommendApprove, sweep.RecommendDecline, sweep.RecommendMerge} {
		if counts[verdict] > 0 {
			split = append(split, fmt.Sprintf("%d to %s", counts[verdict], verdict))
		}
	}
	who := "the owning roles have"
	if len(owners) == 1 {
		for owner := range owners {
			who = "the " + string(owner) + " has"
		}
	}
	named := make([]string, 0, len(tasks))
	for task := range tasks {
		named = append(named, task)
	}
	sort.Strings(named)
	return Attention{
		What: fmt.Sprintf("%s recommended on %s (%s), the latest in the pass of %s at %s",
			who, count(len(r.recommended), "undecided proposed change"), strings.Join(split, ", "),
			strings.Join(named, " and "), latest.UTC().Format(time.RFC3339)),
		Whose: fmt.Sprintf("the operator's — `yoyo amendment approve|decline <id> --reason ...` records each decision, and `yoyo sweeps --task %s` has the reasons", named[0]),
	}
}

// ageAttention is the count-and-age line, where the queue has stopped
// draining: nothing has decided the oldest undecided proposal for longer than
// any working cadence would leave it. Nothing else says so — the per-proposal
// entries name each and fold the rest, and a list is not an age.
func (r amendmentReading) ageAttention() (Attention, bool) {
	if r.queue.OldestAge <= maxUndecidedAmendmentAge {
		return Attention{}, false
	}
	whose := "the owning roles', or the operator's in their stead"
	if len(r.queue.Owners) == 1 {
		whose = fmt.Sprintf("the %s's, or the operator's in their stead", r.queue.Owners[0])
	}
	return Attention{
		What:  r.queue.Describe(),
		Whose: whose + " — proposals are decided with `yoyo amendment`, and a queue this old says the recurring task that argues them is not keeping up, or none is enabled",
	}, true
}
