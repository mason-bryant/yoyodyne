package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// briefStatingOneGoal is the root of the chain the fixtures below hang off. The
// brief states its goals as bolded claims, which is the name a goal downstream
// reaches upward to.
const briefStatingOneGoal = "\n## Goals\n\n- **Intent in, software out.** What the product is for.\n"

// oneGoalOnOneLine is a goals document with nothing wrong with it: one goal, on
// one physical line, naming a claim the brief above states.
const oneGoalOnOneLine = "\n## Goals\n\n- A goal written on one line.\n  *Supports: Intent in, software out.*\n"

// oneGoalWrappedOntoTwoLines is the defect the whole arrangement is named for.
// It is invisible to a reader and changes what the recorded goal is.
const oneGoalWrappedOntoTwoLines = "\n## Goals\n\n- A goal somebody hard-wrapped\n  across two physical lines.\n" +
	"  *Supports: Intent in, software out.*\n"

func TestCheckIsSilentAndGreenWhenTheGovernedDocumentsAreWellFormed(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalOnOneLine)
	writeArtifact(t, project, "docs/designs/v1-harness.md",
		artifactDocument("v1-harness", "design", "V1 harness design", []string{"v1-goals"}))

	stdout, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 0 {
		t.Fatalf("check code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// A clean report says where it looked. A check that had stopped reading
	// anything would otherwise be indistinguishable from a clean repository,
	// which is the way this gate can pass while checking nothing.
	for _, want := range []string{"well-formed", "docs/product"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("check stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

// The gate itself: the two defects the work behind this could not fail anywhere
// anybody could act, each in a document owned by a different role, both named
// with the role and the route.
func TestCheckFailsOnAGovernedDocumentDefectAndNamesItsOwner(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalWrappedOntoTwoLines)
	// And a design whose `supports` entry names a document nobody wrote, which is
	// the other half of the pair the item names.
	writeArtifact(t, project, "docs/designs/v1-harness.md",
		artifactDocument("v1-harness", "design", "V1 harness design", []string{"goals-that-moved"}))

	_, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 1 {
		t.Fatalf("check code = %d, want 1; stderr = %q", code, stderr)
	}
	for _, want := range []string{
		// The wrapped goal, whose owner is the product manager.
		"docs/product/goals/v1-goals.md",
		"not written on one line",
		"product manager",
		"yoyo chat",
		// The dangling reference, whose owner is the architect.
		"docs/designs/v1-harness.md",
		"goals-that-moved",
		"architect",
		"yoyo agent chat architect",
		// And the way out of both, which is the amendment rather than the editor.
		"yoyo amendment list",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("check stderr never says %q:\n%s", want, stderr)
		}
	}
}

// The same findings structurally, so anything automating a repair reads them
// rather than parsing the prose above -- which is the discipline `yoyo doctor`
// already holds itself to for the same reason.
func TestCheckCarriesTheOwnerAndTheRouteInItsJSON(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalWrappedOntoTwoLines)

	stdout, stderr, code := runCLI(t, "artifact", "check", "--config", configPath, "--json")
	if code != 1 {
		t.Fatalf("check --json code = %d, want 1; stderr = %q", code, stderr)
	}
	var report struct {
		Defects []struct {
			Path   string `json:"path"`
			Detail string `json:"detail"`
			Home   string `json:"home"`
			Owner  string `json:"owner"`
			Route  string `json:"route"`
		} `json:"defects"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(report.Defects) == 0 {
		t.Fatalf("report = %#v, want the wrapped goal", report)
	}
	wrapped := report.Defects[0]
	if wrapped.Path != "docs/product/goals/v1-goals.md" || wrapped.Home != "docs/product/goals" {
		t.Fatalf("defect = %#v", wrapped)
	}
	if wrapped.Owner != "product-manager" || !strings.Contains(wrapped.Route, "yoyo chat") {
		t.Fatalf("defect = %#v, want the owner and the way to reach them", wrapped)
	}
}

func TestCheckTakesNoPositionalArgument(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	if _, stderr, code := runCLI(t, "artifact", "check", "--config", configPath, "brief"); code != 2 {
		t.Fatalf("check with an id code = %d, want 2; stderr = %q", code, stderr)
	}
}
