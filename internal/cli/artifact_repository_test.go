package cli

// The tests here read this repository's own artifacts rather than a fixture,
// because a fixture cannot fail the way that matters.
//
// Every artifact test in the package below builds a synthetic store in a
// temporary directory, and every document in one satisfies whatever the loader
// currently requires, because the test wrote it that way. The documents that are
// actually governed — the brief, the goals, the non-goals, and the decision
// records — were written before any given rule and are the ones a tightened
// loader silently drops. A document that stops loading does not fail loudly: it
// leaves the set, and what referred to it is reported as naming something nobody
// wrote.

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

func TestThisRepositoryOwnArtifactsAreReadableByTheHarness(t *testing.T) {
	t.Parallel()

	set := repositoryArtifacts(t)
	// A repository whose homes moved would otherwise pass this by recording
	// nothing at all.
	if len(set.Artifacts) == 0 {
		t.Fatal("this repository records no artifacts; the homes are wrong or nothing is governed")
	}
	for _, problem := range set.Problems {
		t.Errorf("a governed document is not read as an artifact: %s", problem)
	}
	// Every reference problem is reported over documents that loaded rather than
	// refusing them, so a green load is not on its own evidence that the chain
	// holds. All three kinds fail here rather than only the unauthorized
	// revision: a `supports` entry naming a document nobody wrote and a document
	// nothing connects to the brief are the links this repository's own
	// traceability rests on, and a warning nobody is made to read is what let one
	// of them break unnoticed.
	for _, problem := range set.ReferenceProblems {
		t.Errorf("a governed document's place in the chain is wrong: %s", problem)
	}
}

func TestThisRepositoryOwnArtifactsRecordAnOwnerForEveryKind(t *testing.T) {
	t.Parallel()

	// Each kind this repository actually files is one the ownership table has to
	// place. A kind with no owner cannot be written through the harness at all,
	// so finding one here means a governed document is outside the boundary
	// rather than inside it.
	for _, recorded := range repositoryArtifacts(t).Artifacts {
		if _, known := artifact.Owner(recorded.Kind); !known {
			t.Errorf("%s is of kind %q, which no role owns", recorded.Path, recorded.Kind)
		}
	}
}

// repositoryArtifacts loads this repository's artifacts as the harness loads
// them, through the configuration rather than a guessed set of homes: a test
// that hardcoded the directories would keep passing after the project moved
// them, which is one of the ways a gate quietly stops running.
func repositoryArtifacts(t *testing.T) artifact.Set {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		t.Fatalf("resolve product repository: %v", err)
	}
	set, err := artifactStore(repository, resolved.Config.Product).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return set
}
