package amendment

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The queue is summarized once for every surface: how many are undecided, how
// old the oldest of those is, and whose documents they are against. A decided
// proposal counts toward what was proposed and not toward what waits.
func TestTheQueueIsCountedAndAgedFromTheLog(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	oldest := validProposal()
	oldest.RaisedAt = now.Add(-23 * 24 * time.Hour)
	decided := validProposal()
	decided.ID = "amendment-1111111111111111111111111111111a"
	decided.RaisedAt = now.Add(-40 * 24 * time.Hour)
	forThePM := validProposal()
	forThePM.ID = "amendment-2222222222222222222222222222222b"
	forThePM.Artifact, forThePM.Kind, forThePM.Owner = "v1-goals", artifact.KindGoals, domain.RoleProductManager
	forThePM.RaisedAt = now.Add(-2 * time.Hour)
	decision, err := decided.Decide(VerdictDeclined, DeciderOperator, "not this", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	records := []Record{{Proposal: &decided}, {Proposal: &oldest}, {Proposal: &forThePM}, {Decision: &decision}}

	queue := Summarize(records, now)
	if queue.Proposed != 3 || queue.Undecided != 2 {
		t.Fatalf("queue = %+v, want 2 of 3 undecided", queue)
	}
	if !queue.Oldest.Equal(oldest.RaisedAt) || queue.OldestAge != 23*24*time.Hour {
		t.Fatalf("oldest = %s, age = %s; want the decided one not counted", queue.Oldest, queue.OldestAge)
	}
	if len(queue.Owners) != 2 || queue.Owners[0] != domain.RoleArchitect || queue.Owners[1] != domain.RoleProductManager {
		t.Fatalf("owners = %v, want both owners in name order", queue.Owners)
	}
	if described := queue.Describe(); described != "2 of 3 proposed change(s) are undecided, the oldest raised 23d ago, against the architect's and product manager's documents" {
		t.Fatalf("Describe() = %q", described)
	}

	settled := Summarize([]Record{{Proposal: &decided}, {Decision: &decision}}, now)
	if settled.Draining() || settled.Describe() != "nobody is waiting on any of the 1 proposed change(s)" {
		t.Fatalf("Describe() = %q on a settled queue", settled.Describe())
	}
}
