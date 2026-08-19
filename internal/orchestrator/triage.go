package orchestrator

// What one work item may be given, read from the configuration.
//
// The counters themselves are durable state and belong to the product; what
// bounds them is a decision an operator writes down. Assembling the two here
// keeps every caller reading one set of caps: a triage action that refuses and
// the listing that says how close an item is to being refused must not be
// working from different numbers.

import (
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TriageCaps is the configured ceiling on what triage may spend on one work
// item across every run of it.
func TriageCaps(execution config.Execution) runstate.TriageCaps {
	return runstate.TriageCaps{
		RepairGrants: execution.TriageRepairGrantsPerItem,
		Reruns:       execution.TriageRerunsPerItem,
		MergeRearms:  execution.TriageMergeRearmsPerItem,
		ReviewRounds: execution.TriageReviewRoundsPerItem,
	}
}
