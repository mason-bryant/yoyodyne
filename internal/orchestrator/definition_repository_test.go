package orchestrator

// This repository's own copy of its delivery definition, read the way a run of
// this repository reads it.
//
// Yoyodyne is a project like any other project it runs, so it keeps its own
// delivery definition under `.yoyodyne/workflows/` rather than only inside the
// executable. That copy is what every run here executes, which makes it the
// acceptance case for project-owned definitions: if it did not compile, or
// answered to another name, the work would stop before it was claimed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryConfigPath is this repository's own configuration, from the package
// directory these tests run in.
const repositoryConfigPath = "../../.yoyodyne/config.yaml"

// TestThisRepositorysOwnDeliveryDefinitionIsWhatItsRunsExecute reads the
// definition in force for this repository exactly as a run does, and holds it to
// being the project's file rather than the built-in.
func TestThisRepositorysOwnDeliveryDefinitionIsWhatItsRunsExecute(t *testing.T) {
	t.Parallel()

	sequence, err := deliverySequenceFor(DeliveryWorkflowID, repositoryConfigPath)
	if err != nil {
		t.Fatalf("deliverySequenceFor(%s, %s) error = %v", DeliveryWorkflowID, repositoryConfigPath, err)
	}
	if !sequence.Project {
		t.Fatalf("this repository's runs execute the built-in definition; it keeps its own under %s", ProjectDefinitionPath(DeliveryWorkflowID))
	}
	want := filepath.Join("..", "..", ".yoyodyne", filepath.FromSlash(ProjectDefinitionPath(DeliveryWorkflowID)))
	if sequence.Source != want {
		t.Errorf("the definition was read from %s and this repository keeps its own at %s", sequence.Source, want)
	}
	if _, err := compileDelivery(sequence); err != nil {
		t.Fatalf("this repository's own delivery definition does not compile, so every run of it stops before claiming anything: %v", err)
	}
}

// TestThisRepositorysDeliveryDefinitionMatchesTheBuiltIn holds the copy and the
// built-in to one sequence.
//
// The parity harness walks the built-in against the recorded baseline, and this
// repository's runs execute the copy, so a change to one and not the other would
// leave the two measuring different things with nothing saying so. They are held
// by content digest rather than by bytes, because the digest is what an instance
// pins and it deliberately ignores comments — the copy carries a header of its
// own explaining what it is.
//
// This repository diverging from the built-in on purpose is allowed and is what
// project-owned definitions are for. It means changing this test, which is the
// point at which somebody has decided to.
func TestThisRepositorysDeliveryDefinitionMatchesTheBuiltIn(t *testing.T) {
	t.Parallel()

	project, err := observedDeliveryGraph(DeliveryWorkflowID, repositoryConfigPath)
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, %s) error = %v", DeliveryWorkflowID, repositoryConfigPath, err)
	}
	builtin, err := observedDeliveryGraph(DeliveryWorkflowID, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, built in) error = %v", DeliveryWorkflowID, err)
	}
	if project.Digest() != builtin.Digest() {
		t.Errorf("this repository's copy digests to %s and the built-in to %s; the parity harness measures the built-in and the runs here execute the copy",
			project.Digest(), builtin.Digest())
	}
}

// TestThisRepositorysDeliveryDefinitionSaysWhereTheBuiltInIs holds the copy's
// header to naming what it was copied from.
//
// A project file that does not say where it came from is one nobody can update
// against a later build, which is the cost this whole arrangement has: nothing
// is merged, so an improvement to the built-in reaches a project that ejected a
// copy only if somebody goes and looks.
func TestThisRepositorysDeliveryDefinitionSaysWhereTheBuiltInIs(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", ".yoyodyne", filepath.FromSlash(ProjectDefinitionPath(DeliveryWorkflowID)))
	definition, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if !strings.Contains(string(definition), "internal/orchestrator/delivery.yaml") {
		t.Errorf("%s does not name the built-in it was copied from", path)
	}
}
