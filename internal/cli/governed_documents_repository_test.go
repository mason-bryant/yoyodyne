package cli

// The tests here load this repository's own governed documents — the brief and
// goals under the specifications home, the designs, the decision records, and
// the invariants — with the loaders the harness uses, through the homes the
// configuration names. Every other artifact and invariant test builds a
// synthetic store in a temporary directory, so a change to a loader or to what
// Validate accepts could refuse every document this repository ships without a
// single check failing: the refusal would first surface when a run or a
// conversation loaded them.
//
// This is the complement of what goals_repository_test.go holds rather than a
// return of what yoyodyne-ifd.326 took off the build. What fails here is a file
// the loader will not read at all, which is the harness disagreeing with the
// documents it governs and is fixed in whichever of the two changed. What the
// documents say about each other — a link upstream that does not hold, a
// revision recorded under a role that does not own the document — is logged
// rather than failed, for 326's reason: it is how somebody wrote a document they
// own, and `yoyo artifact list` reports it in front of them.

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// TestThisRepositoryOwnArtifactsAreAllReadByTheLoader loads every artifact home
// the configuration names and fails on each file the loader refused, naming it.
func TestThisRepositoryOwnArtifactsAreAllReadByTheLoader(t *testing.T) {
	t.Parallel()

	repository, product := repositoryProduct(t)
	set, err := artifactStore(repository, product).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Reported one at a time, because what somebody has to open is one file.
	for _, problem := range set.Problems {
		t.Errorf("the artifact loader does not read a document this repository ships: %s", problem)
	}
	// A loader that stopped finding anything reports no problems either, so an
	// empty set is failed rather than read as a clean one.
	if len(set.Artifacts) == 0 {
		t.Fatalf("no artifact was read from %v; the loader found nothing this repository ships", set.Homes)
	}
	for _, kind := range []artifact.Kind{artifact.KindBrief, artifact.KindGoals, artifact.KindDesign, artifact.KindDecision} {
		if !setHoldsKind(set, kind) {
			t.Errorf("no %s artifact was read from %v, and this repository ships at least one", kind, set.Homes)
		}
	}
	for _, problem := range set.ReferenceProblems {
		t.Logf("artifact reference problem: %s", problem)
	}
}

// TestThisRepositoryOwnInvariantsAreAllReadByTheLoader is the same check over
// the invariants directory, which the artifact loader excludes because its files
// carry an identity scheme of their own and a loader of their own.
func TestThisRepositoryOwnInvariantsAreAllReadByTheLoader(t *testing.T) {
	t.Parallel()

	repository, product := repositoryProduct(t)
	set, err := invariant.Store{RepositoryRoot: repository, Directory: product.Invariants}.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, problem := range set.Problems {
		t.Errorf("the invariant loader does not read an invariant this repository ships: %s", problem)
	}
	if len(set.Active) == 0 {
		t.Fatalf("no active invariant was read from %s; the loader found nothing this repository ships", set.Directory)
	}
}

// repositoryProduct resolves this repository and its product configuration the
// way the artifact and invariant commands do, so the homes read here are the
// ones the harness reads rather than a list a test hardcoded and nobody updates
// when the project moves them.
func repositoryProduct(t *testing.T) (string, config.Product) {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		t.Fatalf("resolve product repository: %v", err)
	}
	return repository, resolved.Config.Product
}

func setHoldsKind(set artifact.Set, kind artifact.Kind) bool {
	for _, loaded := range set.Artifacts {
		if loaded.Kind == kind {
			return true
		}
	}
	return false
}
