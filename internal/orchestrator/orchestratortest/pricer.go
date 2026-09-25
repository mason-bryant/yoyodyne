package orchestratortest

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Pricer stands in for the ledger that prices work items. It records the
// items it was asked about, which is what makes "a run prices the item it
// served" an assertion rather than a claim.
type Pricer struct {
	Cost   beads.Cost
	Err    error
	Priced []string
}

func (f *Pricer) Record(_ context.Context, workItemID string) (*beads.Cost, error) {
	f.Priced = append(f.Priced, workItemID)
	if f.Err != nil {
		return nil, f.Err
	}
	cost := f.Cost
	return &cost, nil
}
