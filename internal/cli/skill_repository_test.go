package cli

// The tests here read the shipped setup prompt — skills/yoyo-setup/SKILL.md —
// against the reports it tells an agent to act on.
//
// The prompt is the one piece of this installation path that nothing executes.
// A newcomer's own agent session reads it, keys on the fields it names, and
// decides what to run from the remedies it says are there; so a field renamed
// here, a status value added, or a schema version bumped leaves the document
// wrong in a way that costs somebody a broken setup rather than a red test. The
// document is written against the structure of these reports on purpose — that
// is what makes an automated repair possible at all — and this is what holds it
// to them.
//
// What is here is the document against the reports' shape. Its claims about the
// commands themselves — that `yoyo setup --json` asks nothing and changes
// nothing, that exit 1 is a diagnosis to read rather than a failure to retry,
// and that the keychain step is left to a terminal somebody is at — are pinned
// where those commands are exercised, in setup_test.go and doctor_test.go,
// because comparing prose against a struct cannot reach them.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// setupSkillPath is the shipped prompt, read from the package the test lives in.
// It is a skill directory rather than a document under docs/ because it carries
// the frontmatter a skill loader reads, and frontmatter under docs/ is artifact
// identity — which internal/goal holds the repository to.
const setupSkillPath = "../../skills/yoyo-setup/SKILL.md"

// TestTheSetupSkillNamesEveryFieldOfTheReportsItActsOn holds the prompt to the
// two reports' shapes. Every field is required rather than only the ones the
// document happens to use: an agent acts on what it was told is there, so a
// field nothing named is one it can neither read nor be blamed for missing.
func TestTheSetupSkillNamesEveryFieldOfTheReportsItActsOn(t *testing.T) {
	t.Parallel()

	document := readSetupSkill(t)
	for _, reported := range []any{doctor.Report{}, doctor.Finding{}, setupReport{}, setupStep{}} {
		for _, field := range jsonFieldNames(reflect.TypeOf(reported)) {
			if !strings.Contains(document, "`"+field+"`") {
				t.Errorf("%s does not name the %q field of %T, which an agent reading it would never look at",
					setupSkillPath, field, reported)
			}
		}
	}
	// The values it decides on, from both reports. A status the document does
	// not name is one an agent falls through: doctor's three decide whether a
	// remedy is run at all, and setup's four decide whether a step is somebody
	// else's to finish.
	for _, value := range []string{
		string(doctor.StatusOK), string(doctor.StatusWarning), string(doctor.StatusProblem),
		string(setupAlready), string(setupDone), string(setupSkipped), string(setupHandedOff),
	} {
		if !strings.Contains(document, "`"+value+"`") {
			t.Errorf("%s does not name the status %q, which an agent would fall through", setupSkillPath, value)
		}
	}
}

// TestTheSetupSkillIsWrittenAgainstTheSchemaThisBuildEmits pins the version the
// document tells an agent to expect. The document's own instruction on a version
// it does not recognize is to stop and say so, which is worth nothing if the
// number it recognizes is one this build stopped emitting.
func TestTheSetupSkillIsWrittenAgainstTheSchemaThisBuildEmits(t *testing.T) {
	t.Parallel()

	// The document states one version for both reports, which is only honest
	// while they agree. They are separate numbers deliberately, so this is a
	// live claim rather than a tautology.
	if setupSchemaVersion != doctor.SchemaVersion {
		t.Fatalf("setup emits schema_version %d and doctor emits %d; %s states one number for both",
			setupSchemaVersion, doctor.SchemaVersion, setupSkillPath)
	}
	document := readSetupSkill(t)
	if version := fmt.Sprintf("`schema_version: %d`", doctor.SchemaVersion); !strings.Contains(document, version) {
		t.Fatalf("%s is not written against %s, which is what this build emits", setupSkillPath, version)
	}
}

// TestTheSetupSkillExampleIsAReportThisBuildCouldHaveProduced reads the worked
// example as the schema rather than as prose. It is the part of the document an
// agent patterns off hardest, and an example carrying a field that no longer
// exists teaches exactly the wrong thing while every other test stays green.
func TestTheSetupSkillExampleIsAReportThisBuildCouldHaveProduced(t *testing.T) {
	t.Parallel()

	block, err := fenced.Split(readSetupSkill(t), "```json", "json")
	if err != nil || !block.Found {
		t.Fatalf("the worked example in %s could not be read: %v", setupSkillPath, err)
	}
	decoder := json.NewDecoder(strings.NewReader(block.Payload))
	// Nothing the report does not carry: a field the example invents is one an
	// agent would look for in a real report and never find.
	decoder.DisallowUnknownFields()
	var example doctor.Report
	if err := decoder.Decode(&example); err != nil {
		t.Fatalf("the worked example in %s is not a report this build emits: %v", setupSkillPath, err)
	}
	if example.SchemaVersion != doctor.SchemaVersion {
		t.Errorf("the worked example carries schema_version %d, want %d", example.SchemaVersion, doctor.SchemaVersion)
	}
	if example.Status != doctor.StatusProblem {
		t.Errorf("the worked example is %s, want a broken installation to walk through", example.Status)
	}
	// The premise the whole loop rests on, shown rather than only asserted: what
	// an agent does with a finding is run what the finding carries.
	var problems, warnings, healthy int
	for _, finding := range example.Findings {
		switch finding.Status {
		case doctor.StatusProblem:
			problems++
		case doctor.StatusWarning:
			warnings++
		case doctor.StatusOK:
			healthy++
		default:
			t.Errorf("the worked example carries the status %q, which doctor does not emit", finding.Status)
		}
		if finding.Status != doctor.StatusOK && strings.TrimSpace(finding.Remedy) == "" {
			t.Errorf("the example finding %q is %s with no remedy, which is not a report doctor would produce",
				finding.Check, finding.Status)
		}
	}
	if problems == 0 || warnings == 0 || healthy == 0 {
		t.Errorf("the worked example has %d problems, %d warnings and %d healthy findings; it teaches all three",
			problems, warnings, healthy)
	}
}

// TestTheSetupSkillIsReachableFromTheReadme is the whole of what "shipped"
// means for a prompt: a newcomer arrives at the README, and a document nothing
// there points at is one they never learn exists.
func TestTheSetupSkillIsReachableFromTheReadme(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(readme), "skills/yoyo-setup/SKILL.md") {
		t.Fatalf("the README does not point at %s, so nobody following it would find the prompt", setupSkillPath)
	}
}

func readSetupSkill(t *testing.T) string {
	t.Helper()

	document, err := os.ReadFile(setupSkillPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(document)
}

// jsonFieldNames is what a report's fields are called on the wire, which is the
// only name an agent reading the document ever sees.
func jsonFieldNames(reported reflect.Type) []string {
	names := make([]string, 0, reported.NumField())
	for index := 0; index < reported.NumField(); index++ {
		tag, _, _ := strings.Cut(reported.Field(index).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			names = append(names, tag)
		}
	}
	return names
}
