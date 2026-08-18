package review

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeAcceptsValidVerdicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Verdict
	}{
		{
			name:  "approval without findings",
			input: `{"decision":"approve","summary":"Matches the work item and design."}`,
			want:  Verdict{Decision: DecisionApprove, Summary: "Matches the work item and design."},
		},
		{
			name:  "approval with an empty finding list",
			input: `{"decision":"approve","summary":"Checks pass.","findings":[]}`,
			want:  Verdict{Decision: DecisionApprove, Summary: "Checks pass.", Findings: []Finding{}},
		},
		{
			name:  "approval with a non-blocking observation",
			input: `{"decision":"approve","summary":"Good.","findings":[{"severity":"minor","message":"Consider a clearer name."}]}`,
			want: Verdict{
				Decision: DecisionApprove,
				Summary:  "Good.",
				Findings: []Finding{{Severity: SeverityMinor, Message: "Consider a clearer name."}},
			},
		},
		{
			name:  "repair with actionable findings",
			input: `{"decision":"repair","summary":"Two problems.","findings":[{"severity":"blocker","message":"Decoder accepts trailing JSON.","location":{"file":"internal/review/verdict.go","line":42}},{"severity":"major","message":"Missing test coverage.","location":{"file":"internal/review/verdict_test.go"}}]}`,
			want: Verdict{
				Decision: DecisionRepair,
				Summary:  "Two problems.",
				Findings: []Finding{
					{
						Severity: SeverityBlocker,
						Message:  "Decoder accepts trailing JSON.",
						Location: &Location{File: "internal/review/verdict.go", Line: 42},
					},
					{
						Severity: SeverityMajor,
						Message:  "Missing test coverage.",
						Location: &Location{File: "internal/review/verdict_test.go"},
					},
				},
			},
		},
		{
			name:  "explicitly null location is treated as absent",
			input: `{"decision":"repair","summary":"One problem.","findings":[{"severity":"major","message":"Unbounded input.","location":null}]}`,
			want: Verdict{
				Decision: DecisionRepair,
				Summary:  "One problem.",
				Findings: []Finding{{Severity: SeverityMajor, Message: "Unbounded input."}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, unknown, err := Decode([]byte(test.input))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if len(unknown) != 0 {
				t.Fatalf("Decode() unknown fields = %#v, want none", unknown)
			}
			assertVerdictEqual(t, got, test.want)
		})
	}
}

// A model asked for structured output will occasionally embellish the schema.
// An extra field is a verbose verdict rather than a corrupted one, so it decodes
// without the extras and names them for the caller to record instead.
func TestDecodeAcceptsVerdictsCarryingFieldsTheSchemaDoesNotName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		want        Verdict
		wantUnknown []string
	}{
		{
			// The exact shape that killed run-2e5102d105a1c4ad772722b30b3d2635:
			// an approval carrying one field the schema does not name.
			name:        "the severity_note approval",
			input:       `{"decision":"approve","summary":"The change matches the acceptance criteria.","severity_note":"no blocking issues found"}`,
			want:        Verdict{Decision: DecisionApprove, Summary: "The change matches the acceptance criteria."},
			wantUnknown: []string{"severity_note"},
		},
		{
			name:  "extras at every level of the object",
			input: `{"decision":"repair","summary":"Two problems.","confidence":0.9,"findings":[{"severity":"major","message":"Fix.","suggestion":"do it","location":{"file":"a.go","line":7,"column":3}}]}`,
			want: Verdict{
				Decision: DecisionRepair,
				Summary:  "Two problems.",
				Findings: []Finding{{
					Severity: SeverityMajor,
					Message:  "Fix.",
					Location: &Location{File: "a.go", Line: 7},
				}},
			},
			wantUnknown: []string{"confidence", "findings[0].suggestion", "findings[0].location.column"},
		},
		{
			name:  "an extra on the second finding only",
			input: `{"decision":"repair","summary":"Two problems.","findings":[{"severity":"minor","message":"One."},{"severity":"major","message":"Two.","rationale":"because"}]}`,
			want: Verdict{
				Decision: DecisionRepair,
				Summary:  "Two problems.",
				Findings: []Finding{
					{Severity: SeverityMinor, Message: "One."},
					{Severity: SeverityMajor, Message: "Two."},
				},
			},
			wantUnknown: []string{"findings[1].rationale"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, unknown, err := Decode([]byte(test.input))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertVerdictEqual(t, got, test.want)
			if !reflect.DeepEqual(unknown, test.wantUnknown) {
				t.Fatalf("Decode() unknown fields = %#v, want %#v", unknown, test.wantUnknown)
			}
			// Whatever the reviewer invented decides nothing: the verdict resolves
			// exactly as the same verdict without the extras would.
			if _, err := got.Resolve(); err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
		})
	}
}

// Tolerating an extra field must not tolerate a value the contract does not
// allow. A verdict that is both verbose and wrong is still refused, and the
// drift beside it is still named.
func TestDecodeStillRefusesInvalidVerdictsThatAlsoCarryUnknownFields(t *testing.T) {
	t.Parallel()

	got, unknown, err := Decode([]byte(`{"decision":"looks-good","summary":"fine","severity_note":"none"}`))
	if err == nil || !strings.Contains(err.Error(), `decision "looks-good"`) {
		t.Fatalf("Decode() error = %v, want the unknown decision refused", err)
	}
	if got.Decision != "" {
		t.Fatalf("Decode() = %#v, want no verdict on refusal", got)
	}
	if !reflect.DeepEqual(unknown, []string{"severity_note"}) {
		t.Fatalf("Decode() unknown fields = %#v, want the drift reported beside the refusal", unknown)
	}
	// A refused verdict was still read, so it is not asked for again.
	var undecodable UndecodableVerdictError
	if errors.As(err, &undecodable) {
		t.Fatalf("a refused verdict was reported as unreadable: %v", err)
	}
}

func TestDecodeRejectsInvalidVerdicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "empty input",
			input:   ``,
			wantErr: "input is empty",
		},
		{
			name:    "malformed json",
			input:   `{"decision":"approve","summary":`,
			wantErr: "decode review verdict",
		},
		{
			name:    "not an object",
			input:   `["approve"]`,
			wantErr: "decode review verdict",
		},
		{
			name:    "trailing json value",
			input:   `{"decision":"approve","summary":"Fine."} {"decision":"repair"}`,
			wantErr: "trailing content",
		},
		{
			name:    "trailing garbage",
			input:   `{"decision":"approve","summary":"Fine."} not-json`,
			wantErr: "trailing content",
		},
		{
			name:    "invalid decision",
			input:   `{"decision":"reject","summary":"Fine."}`,
			wantErr: `decision "reject"`,
		},
		{
			name:    "missing decision",
			input:   `{"summary":"Fine."}`,
			wantErr: `decision ""`,
		},
		{
			name:    "invalid severity",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"critical","message":"Fix."}]}`,
			wantErr: `severity "critical"`,
		},
		{
			name:    "missing summary",
			input:   `{"decision":"approve"}`,
			wantErr: "summary is required",
		},
		{
			name:    "blank summary",
			input:   `{"decision":"approve","summary":"   \n"}`,
			wantErr: "summary is required",
		},
		{
			name:    "blank finding message",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"  "}]}`,
			wantErr: "findings[0]: message is required",
		},
		{
			name:    "location without a file",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"Fix.","location":{"line":7}}]}`,
			wantErr: "findings[0]: location: file is required",
		},
		{
			name:    "negative location line",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"Fix.","location":{"file":"a.go","line":-1}}]}`,
			wantErr: "line -1 cannot be negative",
		},
		{
			name:    "fractional location line",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"Fix.","location":{"file":"a.go","line":1.5}}]}`,
			wantErr: "decode review verdict",
		},
		{
			name:    "repair without findings",
			input:   `{"decision":"repair","summary":"Something is wrong."}`,
			wantErr: "repair requires at least one finding",
		},
		{
			name:    "repair with an empty finding list",
			input:   `{"decision":"repair","summary":"Something is wrong.","findings":[]}`,
			wantErr: "repair requires at least one finding",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := Decode([]byte(test.input))
			if err == nil {
				t.Fatalf("Decode() = %#v, want error", got)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Decode() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

// A reply nothing could read as a verdict is told apart from a verdict that was
// read and then refused, because only the first is worth asking for again.
func TestDecodeReportsWhichRejectionsAreUnreadableReplies(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		input          string
		wantUnreadable bool
	}{
		{name: "empty input", input: ``, wantUnreadable: true},
		{name: "malformed json", input: `{"decision":"approve","summary":`, wantUnreadable: true},
		{name: "not an object", input: `["approve"]`, wantUnreadable: true},
		{name: "prose", input: `Sure! Here is my review.`, wantUnreadable: true},
		{name: "trailing content", input: `{"decision":"approve","summary":"Fine."} Hope that helps!`, wantUnreadable: true},
		{name: "wrongly typed field", input: `{"decision":"approve","summary":["Fine."]}`, wantUnreadable: true},
		{name: "unknown decision", input: `{"decision":"reject","summary":"Fine."}`},
		{name: "unknown severity", input: `{"decision":"repair","summary":"Bad.","findings":[{"severity":"critical","message":"Fix."}]}`},
		{name: "missing summary", input: `{"decision":"approve"}`},
		{name: "repair without findings", input: `{"decision":"repair","summary":"Something is wrong."}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := Decode([]byte(test.input))
			if err == nil {
				t.Fatal("Decode() error = nil, want a rejection")
			}
			var undecodable UndecodableVerdictError
			if errors.As(err, &undecodable) != test.wantUnreadable {
				t.Fatalf("Decode() error = %v, unreadable = %t, want %t", err, !test.wantUnreadable, test.wantUnreadable)
			}
		})
	}

	// The size bound is the remaining unreadable reply, and it is checked before
	// anything tries to parse the oversized input.
	oversized := fmt.Sprintf(`{"decision":"approve","summary":%q}`, strings.Repeat("a", MaxVerdictBytes))
	var undecodable UndecodableVerdictError
	if _, _, err := Decode([]byte(oversized)); !errors.As(err, &undecodable) {
		t.Fatalf("Decode() oversized error = %v, want an unreadable reply", err)
	}
}

func TestDecodeRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	summary := strings.Repeat("a", MaxVerdictBytes)
	input := fmt.Sprintf(`{"decision":"approve","summary":%q}`, summary)
	if len(input) <= MaxVerdictBytes {
		t.Fatalf("test input is %d bytes, want more than %d", len(input), MaxVerdictBytes)
	}

	_, _, err := Decode([]byte(input))
	if err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("Decode() error = %v, want an input size limit error", err)
	}
}

func TestDecodeAcceptsInputAtTheSizeLimit(t *testing.T) {
	t.Parallel()

	prefix := `{"decision":"approve","summary":"`
	suffix := `"}`
	summary := strings.Repeat("a", MaxVerdictBytes-len(prefix)-len(suffix))
	input := prefix + summary + suffix
	if len(input) != MaxVerdictBytes {
		t.Fatalf("test input is %d bytes, want exactly %d", len(input), MaxVerdictBytes)
	}

	got, _, err := Decode([]byte(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Summary != summary {
		t.Fatalf("Decode() summary length = %d, want %d", len(got.Summary), len(summary))
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	verdict := Verdict{
		Decision: "maybe",
		Findings: []Finding{{Severity: "cosmetic", Location: &Location{Line: -2}}},
	}

	err := verdict.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an aggregated error")
	}
	for _, want := range []string{
		`decision "maybe"`,
		"summary is required",
		`severity "cosmetic"`,
		"message is required",
		"file is required",
		"line -2 cannot be negative",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() error = %v, want it to contain %q", err, want)
		}
	}
}

func TestResolveRejectsDecisionsThatContradictTheirFindings(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		verdict Verdict
		want    Decision
		wantErr string
	}{
		{
			name:    "approve with no findings",
			verdict: Verdict{Decision: DecisionApprove, Summary: "clean"},
			want:    DecisionApprove,
		},
		{
			name: "approve with only minor findings",
			verdict: Verdict{Decision: DecisionApprove, Summary: "good enough", Findings: []Finding{
				{Severity: SeverityMinor, Message: "rename this variable"},
			}},
			want: DecisionApprove,
		},
		{
			name: "approve with a blocker finding",
			verdict: Verdict{Decision: DecisionApprove, Summary: "looks fine", Findings: []Finding{
				{Severity: SeverityBlocker, Message: "this drops the error"},
			}},
			wantErr: "contradictory review verdict",
		},
		{
			name: "approve with a major finding",
			verdict: Verdict{Decision: DecisionApprove, Summary: "looks fine", Findings: []Finding{
				{Severity: SeverityMinor, Message: "spelling"},
				{Severity: SeverityMajor, Message: "no test covers the new branch"},
			}},
			wantErr: "contradictory review verdict",
		},
		{
			name: "repair stays authoritative for a minor finding",
			verdict: Verdict{Decision: DecisionRepair, Summary: "small fix", Findings: []Finding{
				{Severity: SeverityMinor, Message: "rename this variable"},
			}},
			want: DecisionRepair,
		},
		{
			name:    "invalid verdicts are rejected before the policy runs",
			verdict: Verdict{Decision: DecisionApprove},
			wantErr: "summary is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decision, err := test.verdict.Resolve()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Resolve() error = %v, want it to contain %q", err, test.wantErr)
				}
				if decision != "" {
					t.Fatalf("Resolve() decision = %q, want empty on rejection", decision)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if decision != test.want {
				t.Fatalf("Resolve() = %q, want %q", decision, test.want)
			}
		})
	}
}

func assertVerdictEqual(t *testing.T, got, want Verdict) {
	t.Helper()

	if got.Decision != want.Decision || got.Summary != want.Summary {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
	if len(got.Findings) != len(want.Findings) {
		t.Fatalf("Decode() findings = %#v, want %#v", got.Findings, want.Findings)
	}
	for i := range want.Findings {
		gotFinding, wantFinding := got.Findings[i], want.Findings[i]
		if gotFinding.Severity != wantFinding.Severity || gotFinding.Message != wantFinding.Message {
			t.Fatalf("Decode() findings[%d] = %#v, want %#v", i, gotFinding, wantFinding)
		}
		switch {
		case wantFinding.Location == nil:
			if gotFinding.Location != nil {
				t.Fatalf("Decode() findings[%d].location = %#v, want nil", i, gotFinding.Location)
			}
		case gotFinding.Location == nil:
			t.Fatalf("Decode() findings[%d].location = nil, want %#v", i, wantFinding.Location)
		case *gotFinding.Location != *wantFinding.Location:
			t.Fatalf("Decode() findings[%d].location = %#v, want %#v", i, gotFinding.Location, wantFinding.Location)
		}
	}
}
