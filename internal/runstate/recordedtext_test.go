package runstate

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// structuredStringFields are the string fields on State that are not free text:
// identifiers, commits, paths, and enumerations, each held to a shape or a value
// set somewhere rather than to a byte bound, and none of them a sentence anybody
// writes. A field belongs here only if that is true of it. A reason, an error,
// or anything a provider, a forge, or an agent phrases belongs in
// State.recordedTexts instead, where it is bounded.
var structuredStringFields = map[string]string{
	"run_id":                  "matched against the run id pattern",
	"product_id":              "a validated identifier",
	"repository_id":           "an identifier the configuration names",
	"work_item_id":            "the tracker's identifier",
	"work_item_labels":        "the tracker's label identifiers",
	"backend":                 "an enumeration",
	"account_alias":           "matched against the account alias pattern",
	"config_revision":         "matched against the configuration revision pattern",
	"build":                   "matched against the revision pattern",
	"workflow_instance_id":    "a validated identifier",
	"provider_session_id":     "the provider's session identifier",
	"provider_model":          "a model selector",
	"provider_resolved_model": "the provider's model identifier",
	"developer_model":         "a model selector from the configuration",
	"status":                  "an enumeration",
	"phase":                   "an enumeration",
	"worktree_path":           "a path the harness cut",
	"branch":                  "a branch name the harness cut",
	"base_commit":             "matched against the commit pattern",
	"harness_commit":          "matched against the commit pattern",
	"artifacts_retired_by":    "a run id",
	"preserved_work_ref":      "a ref the harness names",
	"target_branch":           "a local branch name",
	"review_session_id":       "the provider's session identifier",
	"review_model":            "a model selector",
	"review_resolved_model":   "the provider's model identifier",
	"review_base_commit":      "matched against the commit pattern",
	"review_head_commit":      "matched against the commit pattern",
	"review_decision":         "an enumeration",
	"review_approves":         "an enumeration",
	"landing_outcome":         "an enumeration",
	"landing_blocked_by":      "a work item identifier resolved against the tracker",
	"usage_limit_model":       "a model selector",
	"pause_cause":             "an enumeration",
	"provider_outage_channel": "an enumeration",
	"provider_stop":           "an enumeration",
}

// Every string field on the run record is either free text held to a bound or a
// structured value, and the test says which by enumerating the fields rather
// than by naming the ones somebody remembered. Three work items bounded three
// fields one at a time, and each moved the unbounded case onto the next field
// nobody had listed; a field added to State now fails here until it is bounded
// in State.recordedTexts or listed above as structured.
func TestEveryStringFieldOnTheRunRecordIsBoundedOrStructured(t *testing.T) {
	t.Parallel()

	var state State
	bounded := make(map[string]recordedText)
	for _, field := range state.recordedTexts() {
		if _, repeated := bounded[field.key]; repeated {
			t.Errorf("%s is bounded twice", field.key)
		}
		bounded[field.key] = field
	}

	value := reflect.ValueOf(&state).Elem()
	stringFields := make(map[string]bool)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		kind := field.Type.Kind()
		if kind == reflect.Slice {
			kind = field.Type.Elem().Kind()
		}
		if kind != reflect.String {
			continue
		}
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		stringFields[key] = true
		recorded, isBounded := bounded[key]
		_, isStructured := structuredStringFields[key]
		switch {
		case isBounded && isStructured:
			t.Errorf("%s (State.%s) is both bounded and listed as structured; it is one or the other", key, field.Name)
		case !isBounded && !isStructured:
			t.Errorf("%s (State.%s) is a string field that is neither bounded in State.recordedTexts nor listed as structured", key, field.Name)
		case isBounded:
			// The entry has to bound the field it names, or the list is bounding
			// one field under another's name.
			if recorded.text != value.Field(index).Addr().Interface().(*string) {
				t.Errorf("recordedTexts names %s but bounds a different field", key)
			}
			if recorded.limit <= len(recorded.cutNote) {
				t.Errorf("%s is bounded to %d bytes, which its own cut note does not fit in", key, recorded.limit)
			}
		}
	}
	for key := range bounded {
		if !stringFields[key] {
			t.Errorf("recordedTexts names %s, which is not a string field on State", key)
		}
	}
	for key := range structuredStringFields {
		if !stringFields[key] {
			t.Errorf("structuredStringFields names %s, which is not a string field on State", key)
		}
	}
}

// And each bounded field is bounded in all three places the rule has to hold:
// the schema names it when it is over, the store cuts it on the way in rather
// than refusing the record, and a record written before the bound existed still
// loads, with the field cut and saying so.
func TestEveryBoundedFieldIsCutOnWriteRefusedBySchemaAndToleratedOnRead(t *testing.T) {
	t.Parallel()

	var probe State
	for _, field := range probe.recordedTexts() {
		t.Run(field.key, func(t *testing.T) {
			t.Parallel()

			store := newTestStore(t)
			state := validStateCarrying(t, field.key)
			if err := store.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			const head = "what the record has to keep: "
			long := head + strings.Repeat("x", field.limit)
			text := func(s *State) *string {
				for _, candidate := range s.recordedTexts() {
					if candidate.key == field.key {
						return candidate.text
					}
				}
				t.Fatalf("%s is not among the recorded texts", field.key)
				return nil
			}

			*text(&state) = long
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), field.key+" is ") {
				t.Fatalf("Validate() over-long %s error = %v, want it named", field.key, err)
			}

			if err := store.Save(state); err != nil {
				t.Fatalf("Save() over-long %s error = %v, want it cut rather than refused", field.key, err)
			}
			saved, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			assertCut(t, "saved", *text(&saved), field, head)

			// What a harness writing before the bound left on disk. It goes to the
			// file directly because every write the store offers cuts it.
			path, err := store.statePath(state.RunID)
			if err != nil {
				t.Fatalf("statePath() error = %v", err)
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			historical, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() pre-bound record error = %v", err)
			}
			assertCut(t, "historical", *text(&historical), field, head)
		})
	}
}

func assertCut(t *testing.T, which, got string, field recordedText, head string) {
	t.Helper()
	if len(got) > field.limit {
		t.Fatalf("%s %s is %d bytes, over its %d byte bound", which, field.key, len(got), field.limit)
	}
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, field.cutNote) {
		t.Fatalf("%s %s lost its head or did not say it was cut: %q", which, field.key, got)
	}
}

// validStateCarrying is a record on which the named field may be set at all,
// since a few of them are only coherent beside another.
func validStateCarrying(t *testing.T, key string) State {
	t.Helper()
	state := testState(t, StatusRunning)
	switch key {
	case "workflow_divergence":
		state.WorkflowInstanceID = "instance-1"
	case "landing_impediment_problem":
		state.LandingOutcome = LandingEvidence
		state.LandingReason = "the work is not doable yet"
	}
	return state
}
