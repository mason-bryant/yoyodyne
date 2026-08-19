package config

import (
	"fmt"
	"strings"
	"testing"
)

// The four per-item caps a project never mentions, which is every project
// written before they existed. They have to arrive as harness defaults or an
// upgraded configuration would silently read as "triage may do nothing and no
// item may be reviewed at all".
func TestTriageCapsArriveAsHarnessDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	for _, want := range []struct {
		field string
		got   int
		value int
	}{
		{"triage_repair_grants_per_item", cfg.Execution.TriageRepairGrantsPerItem, 1},
		{"triage_reruns_per_item", cfg.Execution.TriageRerunsPerItem, 1},
		{"triage_merge_rearms_per_item", cfg.Execution.TriageMergeRearmsPerItem, 2},
		// Four is the one the architect fixed: one round past the three a run
		// spends on its own at the default repair budget, so a grant has room for
		// exactly one more and no more than one.
		{"triage_review_rounds_per_item", cfg.Execution.TriageReviewRoundsPerItem, 4},
	} {
		if want.got != want.value {
			t.Errorf("%s = %d, want the harness default %d", want.field, want.got, want.value)
		}
	}
}

// A value the harness filled in has to say so, because `config show --origins`
// is what answers "why is this what it is" and a cap nobody wrote down is
// exactly the value somebody will ask that about.
func TestTriageCapOriginsAreRecordedAsHarnessDefaults(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	for _, key := range triageCapKeys() {
		if got := resolved.Origins["execution."+key]; got != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, got, OriginDefault)
		}
	}
}

// A layer that states a cap replaces the inherited one, including an explicit
// zero -- which is the value that says "triage never takes this action, hand the
// item to a person". A zero silently read as "unset, use the default" would turn
// that instruction into its opposite.
func TestStatedTriageCapsReplaceTheDefaultsAndKeepAnExplicitZero(t *testing.T) {
	t.Parallel()

	project := minimalProjectConfig + `execution:
  triage_repair_grants_per_item: 0
  triage_reruns_per_item: 3
  triage_merge_rearms_per_item: 0
  triage_review_rounds_per_item: 9
`
	resolved := loadProject(t, project, nil)
	execution := resolved.Config.Execution
	if execution.TriageRepairGrantsPerItem != 0 || execution.TriageMergeRearmsPerItem != 0 {
		t.Fatalf("explicit zeroes = %d and %d, want both to survive",
			execution.TriageRepairGrantsPerItem, execution.TriageMergeRearmsPerItem)
	}
	if execution.TriageRerunsPerItem != 3 || execution.TriageReviewRoundsPerItem != 9 {
		t.Fatalf("stated caps = %d re-runs and %d rounds, want 3 and 9",
			execution.TriageRerunsPerItem, execution.TriageReviewRoundsPerItem)
	}
	// And the provenance follows the value: a cap the project stated is the
	// project's, not the harness's.
	for _, key := range triageCapKeys() {
		if got := resolved.Origins["execution."+key]; got != resolved.Path {
			t.Errorf("origin[%q] = %q, want the project file %q", key, got, resolved.Path)
		}
	}
}

// Zero is a decision and a negative cap is a mistake, so each is refused where it
// is written and named by the key that carries it. All four are checked, because
// a validation loop that missed one would leave that cap unbounded below.
func TestNegativeTriageCapsAreRefusedByName(t *testing.T) {
	t.Parallel()

	for _, key := range triageCapKeys() {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			project := minimalProjectConfig + fmt.Sprintf("execution:\n  %s: -1\n", key)
			_, err := loadProjectError(t, project, nil)
			if err == nil {
				t.Fatalf("LoadResolved() error = nil, want %s refused", key)
			}
			if !strings.Contains(err.Error(), key+" cannot be negative") {
				t.Fatalf("LoadResolved() error = %v, want it to name %s", err, key)
			}
		})
	}
}

// The generated file states every cap it resolved, each against its own key. The
// scaffold renders them positionally, so a key silently carrying its neighbour's
// value is the failure this pins: two of the four defaults are equal, and
// comparing whole structs would not catch them being swapped.
func TestScaffoldWritesEachTriageCapAgainstItsOwnKey(t *testing.T) {
	t.Parallel()

	resolved := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	generated, err := renderScaffoldSource(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	execution := resolved.Config.Execution
	for key, value := range map[string]int{
		"triage_repair_grants_per_item": execution.TriageRepairGrantsPerItem,
		"triage_reruns_per_item":        execution.TriageRerunsPerItem,
		"triage_merge_rearms_per_item":  execution.TriageMergeRearmsPerItem,
		"triage_review_rounds_per_item": execution.TriageReviewRoundsPerItem,
	} {
		want := fmt.Sprintf("\n  %s: %d\n", key, value)
		if !strings.Contains(generated, want) {
			t.Errorf("the generated configuration is missing %q", strings.TrimSpace(want))
		}
	}
	// And what it wrote is what the harness would have filled in, so an operator
	// editing the generated file is editing the value that was in force.
	defaults, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if execution.TriageRepairGrantsPerItem != defaults.Execution.TriageRepairGrantsPerItem ||
		execution.TriageRerunsPerItem != defaults.Execution.TriageRerunsPerItem ||
		execution.TriageMergeRearmsPerItem != defaults.Execution.TriageMergeRearmsPerItem ||
		execution.TriageReviewRoundsPerItem != defaults.Execution.TriageReviewRoundsPerItem {
		t.Fatalf("scaffolded caps = %+v, want the harness defaults %+v", execution, defaults.Execution)
	}
}

// triageCapKeys names the four keys in the order the configuration states them,
// so a test that walks them covers every one rather than the ones somebody
// remembered.
func triageCapKeys() []string {
	return []string{
		"triage_repair_grants_per_item",
		"triage_reruns_per_item",
		"triage_merge_rearms_per_item",
		"triage_review_rounds_per_item",
	}
}

// renderScaffoldSource is the generated configuration as text, which is what a
// positional rendering has to be asserted against: parsing it back would hide a
// key that carried the wrong value under a name that happens to parse.
func renderScaffoldSource(t *testing.T, options ScaffoldOptions) (string, error) {
	t.Helper()
	scaffold, err := NewScaffold(BuiltinV1, options)
	if err != nil {
		return "", err
	}
	return string(scaffold.Config.Content), nil
}
