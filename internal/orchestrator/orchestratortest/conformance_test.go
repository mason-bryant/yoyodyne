package orchestratortest_test

import (
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// Each fake answers for the interface the orchestrator asks of it. This is a
// test package of its own, so importing orchestrator here puts nothing into
// orchestratortest itself.
var (
	_ orchestrator.WorkTracker     = (*orchestratortest.Tracker)(nil)
	_ orchestrator.PullRequests    = (*orchestratortest.Forge)(nil)
	_ orchestrator.Pricer          = (*orchestratortest.Pricer)(nil)
	_ orchestrator.WorktreeManager = orchestratortest.PartialWorktreeManager{}
)
