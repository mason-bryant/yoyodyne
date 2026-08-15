package review

import (
	"fmt"
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

			got, err := Decode([]byte(test.input))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertVerdictEqual(t, got, test.want)
		})
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
			name:    "unknown top-level field",
			input:   `{"decision":"approve","summary":"Fine.","confidence":0.9}`,
			wantErr: `unknown field "confidence"`,
		},
		{
			name:    "unknown finding field",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"Fix.","suggestion":"do it"}]}`,
			wantErr: `unknown field "suggestion"`,
		},
		{
			name:    "unknown location field",
			input:   `{"decision":"repair","summary":"Bad.","findings":[{"severity":"major","message":"Fix.","location":{"file":"a.go","column":3}}]}`,
			wantErr: `unknown field "column"`,
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

			got, err := Decode([]byte(test.input))
			if err == nil {
				t.Fatalf("Decode() = %#v, want error", got)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Decode() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestDecodeRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	summary := strings.Repeat("a", MaxVerdictBytes)
	input := fmt.Sprintf(`{"decision":"approve","summary":%q}`, summary)
	if len(input) <= MaxVerdictBytes {
		t.Fatalf("test input is %d bytes, want more than %d", len(input), MaxVerdictBytes)
	}

	_, err := Decode([]byte(input))
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

	got, err := Decode([]byte(input))
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
