package orchestrator

// Whether a run reaches the forge is decided here, and diagnosed somewhere else.
//
// `yoyo doctor` reports whether this machine has `gh` installed and
// authenticated, and asking that of a project the harness never publishes for
// would fail an installation that is complete. So doctor gates its forge check
// on its own copy of this predicate, and a copy that drifts is worse than no
// check at all: gated too narrowly it calls a project that will fail at its
// first publish healthy, and gated too widely it demands a forge CLI from
// projects that are entirely local.
//
// It has to be a copy -- doctor cannot import a pipeline to ask, and would have
// nothing to hand one if it could -- so this is where the two are held together.
// The test lives beside the predicate that actually decides, because whoever
// changes that is who has to know doctor moves with it.

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestDoctorAgreesWithThePipelineAboutWhoPublishes(t *testing.T) {
	t.Parallel()

	// Every value the setting is allowed to take, so a third one added later
	// arrives here rather than at somebody's first publish. `human` is the off
	// switch and not an approval gate: the design's publishing matrix gives both
	// of its rows as purely local with nothing pushed, and resolvePublishing
	// returns before the publisher is consulted at all.
	for _, publishing := range []domain.ApprovalMode{domain.ApprovalHuman, domain.ApprovalAutomatic} {
		pipeline := Pipeline{}
		pipeline.Config.Approvals.Publishing = publishing

		decided := pipeline.publishes()
		diagnosed := doctor.HarnessPublishes(publishing)
		if decided != diagnosed {
			t.Fatalf("approvals.publishing %q: the pipeline publishes=%t and doctor diagnoses publishes=%t; "+
				"a project is about to be told the wrong thing about needing gh", publishing, decided, diagnosed)
		}
	}
}
