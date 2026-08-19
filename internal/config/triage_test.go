package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// A project that says nothing about triage still gets every threshold, and each
// one is attributed to the harness rather than to a layer that never mentioned
// it. The grant is the exception in kind rather than in provenance: it is
// reported as derived from the repair budget, because that is where its size
// came from.
func TestTriageThresholdsDefaultWhenAbsent(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	triage := resolved.Config.Triage
	if triage.StuckMergeAge != Duration(2*time.Hour) {
		t.Errorf("stuck_merge_age = %s, want 2h", triage.StuckMergeAge)
	}
	if triage.ReviewRoundsCap != 4 {
		t.Errorf("review_rounds_cap = %d, want 4", triage.ReviewRoundsCap)
	}
	if triage.RepairGrantAttempts != 2 {
		t.Errorf("repair_grant_attempts = %d, want the configured repair budget of 2", triage.RepairGrantAttempts)
	}
	for _, key := range []string{"triage.stuck_merge_age", "triage.review_rounds_cap"} {
		if got := resolved.Origins[key]; got != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, got, OriginDefault)
		}
	}
	if got := resolved.Origins["triage.repair_grant_attempts"]; got != OriginDerivedRepairGrant {
		t.Errorf("grant origin = %q, want %q", got, OriginDerivedRepairGrant)
	}
}

// The thresholds resolve through the same three layers everything else does,
// and a project that states one keeps everything it did not state.
func TestTriageThresholdsResolveFromEveryLayer(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil).Config.Triage
	if inherited.StuckMergeAge != Duration(2*time.Hour) || inherited.ReviewRoundsCap != 4 {
		t.Fatalf("inherited triage = %+v, want the harness defaults", inherited)
	}

	resolved := loadProject(t, minimalProjectConfig+`triage:
  stuck_merge_age: 45m
`, nil)
	if got := resolved.Config.Triage.StuckMergeAge; got != Duration(45*time.Minute) {
		t.Fatalf("overridden stuck_merge_age = %s, want 45m", got)
	}
	if origin := resolved.Origins["triage.stuck_merge_age"]; origin != resolved.Path {
		t.Fatalf("stuck_merge_age origin = %q, want %q", origin, resolved.Path)
	}
	if got := resolved.Config.Triage.ReviewRoundsCap; got != 4 {
		t.Fatalf("review_rounds_cap = %d, want the default 4 an unrelated override left alone", got)
	}

	// A generated project inherits no bundle and writes the thresholds down
	// itself, so it is where a misplaced argument in the scaffold template would
	// show up: both stated values are asserted, because swapping two adjacent
	// ones leaves a file that still parses and still validates.
	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config.Triage
	if generated.StuckMergeAge != Duration(2*time.Hour) {
		t.Errorf("generated stuck_merge_age = %s, want 2h", generated.StuckMergeAge)
	}
	if generated.ReviewRoundsCap != 4 {
		t.Errorf("generated review_rounds_cap = %d, want 4", generated.ReviewRoundsCap)
	}
	if generated.RepairGrantAttempts != 2 {
		t.Errorf("generated repair_grant_attempts = %d, want the repair budget of 2", generated.RepairGrantAttempts)
	}
}

// An unstated grant is the size of the effective repair budget, whatever the
// layer that set that budget was. A project that repairs nothing routinely is
// the one case where the two part company: triage may still grant one attempt,
// because the grant is its deliberate exception to that budget rather than
// another helping of it.
func TestTriageRepairGrantFollowsTheRepairBudget(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		body   string
		want   int
		origin string
	}{
		{
			name:   "follows a raised budget",
			body:   "execution:\n  repair_attempts_before_replan: 5\n",
			want:   5,
			origin: OriginDerivedRepairGrant,
		},
		{
			name:   "floored at one when nothing is repaired routinely",
			body:   "execution:\n  repair_attempts_before_replan: 0\n",
			want:   1,
			origin: OriginDerivedRepairGrant,
		},
		{
			name:   "a stated grant is not derived at all",
			body:   "execution:\n  repair_attempts_before_replan: 5\ntriage:\n  repair_grant_attempts: 1\n",
			want:   1,
			origin: "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			resolved := loadProject(t, minimalProjectConfig+testCase.body, nil)
			if got := resolved.Config.Triage.RepairGrantAttempts; got != testCase.want {
				t.Fatalf("repair_grant_attempts = %d, want %d", got, testCase.want)
			}
			wantOrigin := testCase.origin
			if wantOrigin == "" {
				wantOrigin = resolved.Path
			}
			if got := resolved.Origins["triage.repair_grant_attempts"]; got != wantOrigin {
				t.Fatalf("grant origin = %q, want %q", got, wantOrigin)
			}
		})
	}
}

// A cap of zero is a choice somebody can mean — an item that reaches triage is
// escalated or re-scoped rather than repaired again — so it loads and is kept
// rather than being read as an absent value.
func TestTriageRoundsCapAcceptsZero(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`triage:
  review_rounds_cap: 0
`, nil)
	if got := resolved.Config.Triage.ReviewRoundsCap; got != 0 {
		t.Fatalf("review_rounds_cap = %d, want the stated 0", got)
	}
	if origin := resolved.Origins["triage.review_rounds_cap"]; origin != resolved.Path {
		t.Fatalf("review_rounds_cap origin = %q, want %q", origin, resolved.Path)
	}
}

// Every threshold that cannot describe a triage anybody could take is refused
// when the configuration loads, which is before any work is claimed.
func TestTriageThresholdsFailClosed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "age is not a duration",
			body:    "triage:\n  stuck_merge_age: soon\n",
			wantErr: "parse duration",
		},
		{
			// Unlike the pauses, zero is not a choice here: it dockets every
			// publication the instant it is made.
			name:    "age of no time at all",
			body:    "triage:\n  stuck_merge_age: 0s\n",
			wantErr: "triage.stuck_merge_age must be positive",
		},
		{
			name:    "negative age",
			body:    "triage:\n  stuck_merge_age: -1h\n",
			wantErr: "triage.stuck_merge_age must be positive",
		},
		{
			name:    "negative rounds cap",
			body:    "triage:\n  review_rounds_cap: -1\n",
			wantErr: "triage.review_rounds_cap cannot be negative",
		},
		{
			name:    "a grant of nothing",
			body:    "triage:\n  repair_grant_attempts: 0\n",
			wantErr: "triage.repair_grant_attempts must be at least 1",
		},
		{
			name:    "negative grant",
			body:    "triage:\n  repair_grant_attempts: -2\n",
			wantErr: "triage.repair_grant_attempts must be at least 1",
		},
		{
			name:    "a misspelled threshold",
			body:    "triage:\n  stuck_merge_agee: 2h\n",
			wantErr: "field stuck_merge_agee not found",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectError(t, minimalProjectConfig+testCase.body, nil)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("LoadResolved() error = %v, want one mentioning %q", err, testCase.wantErr)
			}
		})
	}
}

// The stuck-merge age is reported back the way it was written, in both
// renderings, for the reason every other configured duration is: `config show`
// is where an operator finds out how long a publication may sit, and a
// nanosecond count is not an answer to that question.
func TestTriageAgeRendersAsADuration(t *testing.T) {
	t.Parallel()

	triage := loadProject(t, minimalProjectConfig, nil).Config.Triage
	asYAML, err := yaml.Marshal(triage)
	if err != nil {
		t.Fatalf("Marshal() yaml error = %v", err)
	}
	if !strings.Contains(string(asYAML), "stuck_merge_age: 2h0m0s") {
		t.Fatalf("effective YAML did not render the age as a duration:\n%s", asYAML)
	}
	asJSON, err := json.Marshal(triage)
	if err != nil {
		t.Fatalf("Marshal() json error = %v", err)
	}
	if !strings.Contains(string(asJSON), `"stuck_merge_age":"2h0m0s"`) {
		t.Fatalf("effective JSON did not render the age as a duration:\n%s", asJSON)
	}
}
