package orchestrator

import "github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"

// The fakes of other packages' interfaces live in orchestratortest, so a test
// package outside this one can use them as well. These names are what this
// package's tests have always called them; a test moved out uses the
// orchestratortest names directly.
type (
	fakeTracker            = orchestratortest.Tracker
	fakeBackend            = orchestratortest.Backend
	fakeForge              = orchestratortest.Forge
	fakePricer             = orchestratortest.Pricer
	partialWorktreeManager = orchestratortest.PartialWorktreeManager
)

var (
	roleBackend      = orchestratortest.RoleBackend
	withVerification = orchestratortest.WithVerification
	connectionReset  = orchestratortest.ConnectionReset
)

// Each fake still answers for the interface this package asks of it.
var (
	_ WorkTracker     = (*fakeTracker)(nil)
	_ PullRequests    = (*fakeForge)(nil)
	_ Pricer          = (*fakePricer)(nil)
	_ WorktreeManager = partialWorktreeManager{}
)
