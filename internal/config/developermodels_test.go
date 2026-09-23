package config

import (
	"reflect"
	"strings"
	"testing"
)

// The developer models block: which label sends a run onto which model, read in
// the file's own order and held to the tracker's label rule and to the model
// selector rule every other selector is held to.

func TestDeveloperModelsAreReadInTheFilesOwnOrder(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_models:
    - label: docs
      model: sonnet
    - label: tests
      model: haiku
`, nil)
	want := []DeveloperModelRule{{Label: "docs", Model: "sonnet"}, {Label: "tests", Model: "haiku"}}
	if got := resolved.Config.Execution.DeveloperModels; !reflect.DeepEqual(got, want) {
		t.Fatalf("developer_models = %+v, want %+v", got, want)
	}
	if origin := resolved.Origins["execution.developer_models"]; !strings.Contains(origin, "config.yaml") {
		t.Fatalf("developer_models origin = %q, want the project file", origin)
	}
}

func TestAProjectThatMapsNoLabelRunsEverythingOnTheDevelopersOwnModel(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if got := resolved.Config.Execution.DeveloperModels; len(got) != 0 {
		t.Fatalf("developer_models = %+v, want none", got)
	}
	// And nothing is chosen, so the run records nothing and asks for the
	// developer's configured model exactly as it did before the mapping existed.
	if choice := ResolveDeveloperModel(nil, []string{"docs"}, "opus"); choice.Chosen() {
		t.Fatalf("an unconfigured mapping chose %+v, want nothing", choice)
	}
}

func TestAMappingEntryNamingALabelTheTrackerWouldNotCarryIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  developer_models:
    - label: "the documentation work"
      model: sonnet
`, nil)
	if err == nil {
		t.Fatal("a mapping keyed on a sentence loaded")
	}
	want := `execution.developer_models entry 1: label "the documentation work" is not an identifier`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want it to say %q", err, want)
	}
}

func TestAMappingEntryNamingNoUsableModelIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  developer_models:
    - label: docs
      model: ""
    - label: tests
      model: "--dangerously-skip-permissions"
`, nil)
	if err == nil {
		t.Fatal("a mapping naming no usable model loaded")
	}
	for _, want := range []string{
		"execution.developer_models entry 1: model selector is required",
		`execution.developer_models entry 2: model selector "--dangerously-skip-permissions" must be a single model name`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %v, want it to say %q", err, want)
		}
	}
}

func TestALabelMappedTwiceIsRefused(t *testing.T) {
	t.Parallel()

	// The first match in the order is what an item takes, so the second entry is
	// one nothing would ever reach — refused rather than left to read as a
	// mapping the operator believes is active.
	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  developer_models:
    - label: docs
      model: sonnet
    - label: docs
      model: haiku
`, nil)
	if err == nil {
		t.Fatal("a label mapped twice loaded")
	}
	if want := `execution.developer_models entry 2: label "docs" is mapped twice`; !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want it to say %q", err, want)
	}
}

func TestAnItemCarryingTwoMappedLabelsTakesTheFirstInTheMappingsOrder(t *testing.T) {
	t.Parallel()

	mapping := []DeveloperModelRule{{Label: "docs", Model: "sonnet"}, {Label: "tests", Model: "haiku"}}
	// The item's own label order is the opposite of the mapping's, so a
	// resolution that read the item rather than the mapping would take haiku.
	choice := ResolveDeveloperModel(mapping, []string{"tests", "docs"}, "opus")
	if choice.Model != "sonnet" {
		t.Fatalf("model = %q, want sonnet, the first entry in the mapping's order", choice.Model)
	}
	for _, want := range []string{`the item's "docs" label is mapped to sonnet`, `it also carries "tests", mapped to haiku`} {
		if !strings.Contains(choice.Reason, want) {
			t.Errorf("reason = %q, want it to say %q", choice.Reason, want)
		}
	}
}

func TestAnUnmappedItemTakesTheDevelopersConfiguredModelAndSaysSo(t *testing.T) {
	t.Parallel()

	mapping := []DeveloperModelRule{{Label: "docs", Model: "sonnet"}}
	choice := ResolveDeveloperModel(mapping, []string{"reliability"}, "opus")
	if !choice.Chosen() || choice.Model != "opus" {
		t.Fatalf("model = %q, want the developer's configured opus", choice.Model)
	}
	if want := "the item carries no label execution.developer_models names"; !strings.Contains(choice.Reason, want) {
		t.Fatalf("reason = %q, want it to say %q", choice.Reason, want)
	}
}

func TestLabelsAreComparedExactlyAsTheTrackerStoresThem(t *testing.T) {
	t.Parallel()

	mapping := []DeveloperModelRule{{Label: "docs", Model: "sonnet"}}
	if choice := ResolveDeveloperModel(mapping, []string{"Docs"}, "opus"); choice.Model != "opus" {
		t.Fatalf("model = %q, want opus: Docs and docs are two labels", choice.Model)
	}
}
