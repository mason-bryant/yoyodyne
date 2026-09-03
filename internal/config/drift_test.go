package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The table itself. Each row exists because a two-way diff gets it wrong: it
// cannot separate the middle two, which are the only two worth acting on.
func TestEveryValueIsSortedByWhichSideMovedIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                    string
		baseline, yours, bundle string
		want                    Class
	}{
		{"neither side moved it", "opus", "opus", "opus", ClassUnchanged},
		{"you moved it and the template did not", "opus", "haiku", "opus", ClassYours},
		{"the template moved it and you did not", "opus", "opus", "sonnet", ClassAvailable},
		{"both moved it, differently", "opus", "haiku", "sonnet", ClassConflicting},
		// Both moving a value to the same value is not a conflict: there is
		// nothing to decide and nothing to adopt.
		{"both moved it to the same value", "opus", "sonnet", "sonnet", ClassUnchanged},
	} {
		if got := classify(test.baseline, test.yours, test.bundle); got != test.want {
			t.Errorf("%s: classify(%q, %q, %q) = %q, want %q",
				test.name, test.baseline, test.yours, test.bundle, got, test.want)
		}
	}
}

// materialized is a project exactly as `yoyo init` would leave it: the bundle's
// values, and the baseline recording that they came from the bundle.
func materialized(t *testing.T) (Lock, Config) {
	t.Helper()
	lock, err := NewLock(BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	template, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	resolved, err := resolveLayers([]layer{{origin: template.name, document: template.document, personas: template.personas}})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	return lock, resolved.Config
}

// moved rewrites what the baseline says the bundle supplied, which is how a
// bundle that has since improved a value is arranged for without a second
// executable to compare against.
func moved(lock Lock, key, was string) Lock {
	values := make(map[string]string, len(lock.Values))
	for name, value := range lock.Values {
		values[name] = value
	}
	values[key] = was
	return Lock{Version: lock.Version, Bundle: lock.Bundle, Revision: baselineRevision(values), Values: values}
}

func TestAProjectThatMatchesItsTemplateSaysNothing(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	if !drift.Current() {
		t.Errorf("a freshly generated project reads as moved: %s then, %s now", drift.BaselineRevision, drift.BundleRevision)
	}
	if available := drift.Available(); len(available) != 0 {
		t.Errorf("Available() = %v, want nothing available in a project that just materialized", available)
	}
	// Silence is the whole of what makes this printable on every run.
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence", notice)
	}
	if len(drift.Values) == 0 {
		t.Error("nothing was compared at all, so the silence above means nothing")
	}
}

func TestAnImprovedTemplateValueIsAvailableAndIsWhatTheNoticeSpeaks(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	// The bundle used to say sonnet and the project still does; the bundle says
	// opus now.
	lock = moved(lock, "agents.developer.model", "sonnet")
	effective.Agents["developer"] = withModel(effective.Agents["developer"], "sonnet")

	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	available := drift.Available()
	if len(available) != 1 || available[0].Key != "agents.developer.model" {
		t.Fatalf("Available() = %+v, want the one value the template improved", available)
	}
	if available[0].Yours != "sonnet" || available[0].Bundle != "opus" {
		t.Errorf("available value = %+v, want both sides carried so the operator can decide", available[0])
	}
	notice := drift.Notice()
	for _, want := range []string{BuiltinV1, "1 value", "agents.developer.model", "yoyo config drift"} {
		if !strings.Contains(notice, want) {
			t.Errorf("Notice() = %q, want it to name %q", notice, want)
		}
	}
}

// The other grain of the same comparison: one improvement said on its own, for
// the surface that sends one message per improvement rather than a line counting
// them. Both are worded here so a channel and a terminal cannot come to say
// different things about one value.
func TestOneImprovementIsSaidWithBothItsValues(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	lock = moved(lock, "agents.developer.model", "sonnet")
	effective.Agents["developer"] = withModel(effective.Agents["developer"], "sonnet")

	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	available := drift.Available()
	if len(available) != 1 {
		t.Fatalf("Available() = %+v, want the one value the template improved", available)
	}
	said := drift.Improvement(available[0])
	for _, want := range []string{BuiltinV1, "agents.developer.model", `"sonnet"`, `"opus"`} {
		if !strings.Contains(said, want) {
			t.Errorf("Improvement() = %q, want it to carry %q", said, want)
		}
	}
}

// A value long enough to bury the sentence it is in is cut, on a rune boundary,
// and the whole of it is still in `yoyo config drift`. Every list in a
// configuration is one value, so this is the ordinary shape of a replaced list
// rather than a pathological one.
func TestALongValueIsCutRatherThanBuryingTheSentence(t *testing.T) {
	t.Parallel()

	drift := Drift{Known: true, Bundle: BuiltinV1}
	long := strings.Repeat("é", 400)
	said := drift.Improvement(Value{Key: "checks", Class: ClassAvailable, Baseline: "[]", Bundle: long})
	if len(said) > 400 {
		t.Errorf("Improvement() is %d bytes, want a value cut rather than said whole", len(said))
	}
	if !strings.Contains(said, "…") {
		t.Errorf("Improvement() = %q, want the cut said rather than left silent", said)
	}
	if !utf8.ValidString(said) {
		t.Error("Improvement() cut a value in the middle of a rune")
	}
}

// A value the project moved is never offered and never spoken about unprompted.
// It is the operator's, and a harness that kept mentioning it would be nagging
// about a decision that was already made.
func TestAValueTheProjectMovedIsNeverSpokenUnprompted(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	effective.Agents["developer"] = withModel(effective.Agents["developer"], "haiku")

	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence about a value the project owns", notice)
	}
	if got := classOf(drift, "agents.developer.model"); got != ClassYours {
		t.Errorf("agents.developer.model = %q, want %q", got, ClassYours)
	}
}

// A value both sides moved is reported by the command that was asked and never
// by the unprompted line: it needs a decision the operator has not been asked
// for, and adopting one is exactly what this must not do.
func TestAValueBothSidesMovedIsHeldForTheOperatorAndNotSpokenUnprompted(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	lock = moved(lock, "agents.developer.model", "sonnet")
	effective.Agents["developer"] = withModel(effective.Agents["developer"], "haiku")

	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	if conflicting := drift.Conflicting(); len(conflicting) != 1 || conflicting[0].Key != "agents.developer.model" {
		t.Fatalf("Conflicting() = %+v, want the one value both sides moved", conflicting)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want the unprompted line silent about a conflict", notice)
	}
}

// A persona is the improvement this whole record exists for, and the one the
// serialized configuration cannot see: its text is deliberately out of that
// form, so an unchanged path and byte count would report a persona that was
// rewritten as identical.
func TestAnImprovedPersonaIsNoticedThoughTheConfigurationSaysNothingAboutItsText(t *testing.T) {
	t.Parallel()

	lock, effective := materialized(t)
	developer := effective.Agents["developer"]
	lock = moved(lock, "agents.developer.persona.text", personaTextDigest(developer.Persona.Text+" as it used to read"))
	developer.Persona.Text += " as it used to read"
	effective.Agents["developer"] = developer

	drift, err := CompareToBaseline(lock, effective)
	if err != nil {
		t.Fatalf("CompareToBaseline() error = %v", err)
	}
	if got := classOf(drift, "agents.developer.persona.text"); got != ClassAvailable {
		t.Fatalf("agents.developer.persona.text = %q, want %q", got, ClassAvailable)
	}
	if notice := drift.Notice(); !strings.Contains(notice, "agents.developer.persona.text") {
		t.Errorf("Notice() = %q, want the improved persona named", notice)
	}
}

// The notice is one line however much moved. A line that printed forty keys is
// one an operator scrolls past, which is the failure the rule about nagging is
// about.
func TestTheNoticeNamesAFewValuesAndCountsTheRest(t *testing.T) {
	t.Parallel()

	drift := Drift{Known: true, Bundle: BuiltinV1}
	for index := range maxNamedImprovements + 3 {
		drift.Values = append(drift.Values, Value{
			Key:   fmt.Sprintf("agents.role%d.model", index),
			Class: ClassAvailable,
		})
	}
	notice := drift.Notice()
	if strings.Count(notice, "\n") != 0 {
		t.Errorf("Notice() = %q, want one line", notice)
	}
	if !strings.Contains(notice, "and 3 more") {
		t.Errorf("Notice() = %q, want the unnamed values counted", notice)
	}
	if !strings.Contains(notice, fmt.Sprintf("%d values", maxNamedImprovements+3)) {
		t.Errorf("Notice() = %q, want the whole count stated", notice)
	}
}

// A project with no baseline is not a project that is current. Nothing fails
// over it, and nothing is claimed about it.
func TestAProjectWithNoBaselineIsUnknownRatherThanCurrent(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, FileName)
	if err := os.WriteFile(path, []byte("version: 1\nextends: builtin:v1\nproduct:\n  id: unbaselined\n  repository: .\nchecks:\n  - make test\n"), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	drift, unknown := ReadDrift(resolved)
	if drift.Known || drift.Current() {
		t.Fatalf("drift = %+v, want an unknown baseline rather than a current project", drift)
	}
	if !unknown.Absent {
		t.Errorf("unknown = %+v, want a project with no baseline reported as an absence", unknown)
	}
	if !strings.Contains(unknown.Reason, LockFileName) {
		t.Errorf("reason = %q, want it to name the file that is missing", unknown.Reason)
	}
	// Unknown is silent on the surfaces that speak unprompted: a project that
	// predates the record decides nothing about how it runs, and being told so on
	// every invocation is the nagging this was told not to do.
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence where there is nothing to compare against", notice)
	}
}

func TestAnUnreadableBaselineIsReportedRatherThanRaised(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, FileName)
	if err := os.WriteFile(path, []byte("version: 1\nextends: builtin:v1\nproduct:\n  id: edited\n  repository: .\nchecks:\n  - make test\n"), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	if err := os.WriteFile(LockPath(path), []byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	drift, unknown := ReadDrift(resolved)
	if drift.Known {
		t.Fatalf("drift = %+v, want a baseline that does not digest its own values refused", drift)
	}
	// A baseline that is on disk and is being refused is not an absence, and
	// saying it was would tell its operator the opposite of what they can see.
	if unknown.Absent {
		t.Errorf("unknown = %+v, want a baseline that exists reported as unusable rather than missing", unknown)
	}
	if !strings.Contains(unknown.Reason, "edited") {
		t.Errorf("reason = %q, want it to say the baseline was edited", unknown.Reason)
	}
	// The way back is version control. `yoyo init --force` regenerates the
	// configuration and every persona from the template, so sending somebody
	// there over a corrupt record costs them the edits this exists to protect.
	if !strings.Contains(unknown.Reason, "version control") {
		t.Errorf("reason = %q, want it to name the non-destructive way back", unknown.Reason)
	}
	if strings.Contains(unknown.Reason, "init --force") && !strings.Contains(unknown.Reason, "every persona") {
		t.Errorf("reason = %q, names `yoyo init --force` without saying what it overwrites", unknown.Reason)
	}
}

func classOf(drift Drift, key string) Class {
	for _, value := range drift.Values {
		if value.Key == key {
			return value.Class
		}
	}
	return ""
}

func withModel(agent AgentConfig, model string) AgentConfig {
	agent.Model = model
	return agent
}
