package amendment

// How the queue of proposed changes stands: how many nobody has decided, how
// long the oldest of those has waited, and whose documents they are against.
//
// It is derived here rather than at each surface for the reason the report
// pile's summary is: an operator told one undecided count by the terminal and
// another by the channel has a disagreement to adjudicate rather than a queue
// to work through. What each surface decides for itself is only how to word it.
//
// The age is the load-bearing number. Forty-four undecided proposals look the
// same on the day the cadence that argues them stops as they did the day
// before; what shows a queue nothing is draining is the oldest one's age
// climbing past anything a working cadence would leave.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Queue is how the proposed changes stand.
type Queue struct {
	Proposed  int `json:"proposed"`
	Undecided int `json:"undecided"`
	// Oldest is when the oldest undecided proposal was raised, and OldestAge how
	// long ago that is as of the reading. Both are zero on a queue with nothing
	// undecided in it, which is the state this whole mechanism is aiming at.
	Oldest    time.Time     `json:"oldest,omitempty"`
	OldestAge time.Duration `json:"oldest_age,omitempty"`
	// Owners are the roles whose documents the undecided proposals are against,
	// in name order, so a surface can say whose queue it is rather than only
	// that there is one.
	Owners []domain.AgentRole `json:"owners,omitempty"`
}

// Draining reports whether there is anything left to decide.
func (q Queue) Draining() bool { return q.Undecided > 0 }

// Describe says how the queue stands in one line, for whichever surface is
// saying it. The age is coarse on purpose, as the pile's is: what this answers
// is whether the oldest undecided proposal has been waiting an afternoon or a
// month.
func (q Queue) Describe() string {
	if q.Undecided == 0 {
		return fmt.Sprintf("nobody is waiting on any of the %d proposed change(s)", q.Proposed)
	}
	return fmt.Sprintf("%d of %d proposed change(s) are undecided, the oldest raised %s ago, against the %s documents",
		q.Undecided, q.Proposed, queueAge(q.OldestAge), q.owners())
}

// owners is whose documents the undecided proposals are against, as a phrase.
func (q Queue) owners() string {
	named := make([]string, 0, len(q.Owners))
	for _, owner := range q.Owners {
		named = append(named, owner.Title()+"'s")
	}
	switch len(named) {
	case 0:
		return "owning roles'"
	case 1:
		return named[0]
	default:
		return strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1]
	}
}

// queueAge is an elapsed time as somebody says one.
func queueAge(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		return "no time at all; it is stamped ahead of this reading"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}

// Summarize reads the queue as it stands from the whole log, which is what the
// store holds: what is expensive about a log this size is deciding it, not
// counting it.
func Summarize(records []Record, now time.Time) Queue {
	queue := Queue{Proposed: len(Proposals(records))}
	owners := map[domain.AgentRole]bool{}
	for _, proposal := range Pending(records) {
		queue.Undecided++
		if queue.Oldest.IsZero() || proposal.RaisedAt.Before(queue.Oldest) {
			queue.Oldest = proposal.RaisedAt
		}
		owners[proposal.Owner] = true
	}
	if !queue.Oldest.IsZero() {
		queue.OldestAge = now.UTC().Sub(queue.Oldest.UTC())
	}
	for owner := range owners {
		queue.Owners = append(queue.Owners, owner)
	}
	sort.Slice(queue.Owners, func(i, j int) bool { return queue.Owners[i] < queue.Owners[j] })
	return queue
}
