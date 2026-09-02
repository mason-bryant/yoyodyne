package config

// The three-way comparison a baseline makes possible, and the one line every
// surface says it with.
//
// This is the derivation itself rather than a copy of it kept in a command. "Is
// this project current" is a question the CLI asks today and the dashboard and
// the workspace will ask later, and two surfaces answering it differently is a
// disagreement only the operator could settle -- so the classification and the
// wording of the notice both live here, and a surface projects them rather than
// recomputing them. See the `surfaces-project-one-read-model` invariant.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Class is what became of one value between the bundle a project materialized
// from and the bundle in the executable asking.
//
// The middle two are the entire point. A two-way diff against a freshly
// generated file reports them identically, because it has no third side to tell
// a value the operator changed from a value the bundle improved.
type Class string

const (
	// ClassUnchanged is a value the project and the bundle agree on, which is
	// ordinarily one neither of them moved and occasionally one they both moved
	// to the same place. Either way there is nothing to offer.
	ClassUnchanged Class = "unchanged"
	// ClassYours is a value the project changed and the bundle did not. It is
	// never offered and never touched.
	ClassYours Class = "yours"
	// ClassAvailable is a value the bundle improved and the project never
	// edited. This is the improvement a project that materialized would
	// otherwise never hear about.
	ClassAvailable Class = "available"
	// ClassConflicting is a value both sides moved, to different values. It is
	// reported with both values and settled by the operator, never adopted.
	ClassConflicting Class = "conflicting"
)

// Value is one key's answer, with the three sides that produced it so a report
// can show them rather than assert a verdict.
type Value struct {
	Key      string `json:"key"`
	Class    Class  `json:"class"`
	Baseline string `json:"baseline"`
	Yours    string `json:"yours"`
	Bundle   string `json:"bundle"`
}

// Drift is the whole comparison for one project.
type Drift struct {
	// Known reports whether there was a baseline to compare against at all. A
	// project that predates the baseline, or deleted it, has no third side and
	// is not compared -- which is an answer nobody can give rather than a
	// project that is current.
	Known bool `json:"known"`
	// Bundle names the template the baseline was taken from.
	Bundle string `json:"bundle,omitempty"`
	// BaselineRevision and BundleRevision are what the bundle supplied then and
	// what it supplies now. Equal revisions mean nothing moved, which is the
	// answer for almost every run.
	BaselineRevision string `json:"baseline_revision,omitempty"`
	BundleRevision   string `json:"bundle_revision,omitempty"`
	// Values is every compared key, in key order.
	Values []Value `json:"values,omitempty"`
}

// Available is the improvements a project could take: the bundle moved them and
// the project never did. It is what the unprompted surfaces speak, and the only
// class any of them speaks.
func (d Drift) Available() []Value { return d.OfClass(ClassAvailable) }

// Conflicting is the values both sides moved. Nothing adopts one; it is shown
// with both values until the operator settles it.
func (d Drift) Conflicting() []Value { return d.OfClass(ClassConflicting) }

// OfClass is every value with one answer, in key order. A surface that prints
// the classes it was asked for reads them through here rather than filtering the
// values itself, which is the same reason the classification is in this file.
func (d Drift) OfClass(class Class) []Value {
	var matched []Value
	for _, value := range d.Values {
		if value.Class == class {
			matched = append(matched, value)
		}
	}
	return matched
}

// Current reports whether the bundle has moved at all since the project
// materialized, which is what `config show` says in one word.
func (d Drift) Current() bool { return d.Known && d.BaselineRevision == d.BundleRevision }

// CompareToBaseline sorts every value the baseline recorded into one of the four
// answers, against what the bundle in this executable supplies now and what the
// project's configuration says today.
//
// Only keys the baseline recorded are compared. A key the running bundle states
// that the baseline never saw has no third side either -- the project may have
// written that value deliberately, and a baseline reconstructed by assuming it
// did not is a guess dressed as a record -- so it is left out rather than
// reported as an improvement nobody can be sure is one.
func CompareToBaseline(lock Lock, effective Config) (Drift, error) {
	current, err := BundleValues(lock.Bundle)
	if err != nil {
		return Drift{}, err
	}
	yours := ProjectValues(effective)

	drift := Drift{
		Known:            true,
		Bundle:           lock.Bundle,
		BaselineRevision: lock.Revision,
		BundleRevision:   baselineRevision(current),
	}
	keys := make([]string, 0, len(lock.Values))
	for key := range lock.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bundleNow, supplied := current[key]
		if !supplied {
			// The bundle no longer states this value, so there is nothing it is
			// offering. Whatever the project holds is the project's now.
			continue
		}
		baseline := lock.Values[key]
		drift.Values = append(drift.Values, Value{
			Key:      key,
			Class:    classify(baseline, yours[key], bundleNow),
			Baseline: baseline,
			Yours:    yours[key],
			Bundle:   bundleNow,
		})
	}
	return drift, nil
}

// classify is the table itself, written once so no surface restates it.
//
// Agreement is asked first, and it is what makes the last row of the table a
// row about different values rather than about both sides having moved: a
// project that changed a value to what the bundle has since changed it to has
// nothing to adopt and nothing to settle, and reporting that as a conflict would
// be asking the operator to decide between a value and itself.
func classify(baseline, yours, bundle string) Class {
	switch {
	case yours == bundle:
		return ClassUnchanged
	case yours == baseline:
		return ClassAvailable
	case bundle == baseline:
		return ClassYours
	default:
		return ClassConflicting
	}
}

// ProjectValues is a project's effective configuration keyed the way a baseline
// is, so the two are comparable value by value.
func ProjectValues(effective Config) map[string]string {
	values := flattenConfig(effective)
	for name, agent := range effective.Agents {
		if !agent.Persona.Defined() {
			continue
		}
		values[personaTextKey(name)] = personaTextDigest(agent.Persona.Text)
	}
	return values
}

// LockPath is where a configuration's baseline lives: beside the configuration,
// so a checkout that has one configuration has the baseline that goes with it.
func LockPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), LockFileName)
}

// ReadDrift is the whole derivation for a loaded configuration: read the
// baseline beside it, and compare.
//
// Everything it cannot answer answers "unknown" rather than raising. A project
// with no baseline, one written by an executable that reads a different record,
// one naming a bundle this executable does not have -- none of those is a
// failure of the command that asked, and refusing one would break a project over
// a file that decides nothing about how it runs. The reason is carried back so a
// surface that was asked outright can say it.
func ReadDrift(resolved Resolved) (Drift, string) {
	if resolved.Path == "" {
		return Drift{}, "the configuration was not read from a project directory, so it has no baseline beside it"
	}
	file, err := os.Open(LockPath(resolved.Path))
	if err != nil {
		if os.IsNotExist(err) {
			return Drift{}, fmt.Sprintf("this project has no %s, so what its template supplied when it was generated was never recorded", LockFileName)
		}
		return Drift{}, fmt.Sprintf("the baseline beside this configuration could not be read: %v", err)
	}
	defer file.Close()

	lock, err := DecodeLock(file)
	if err != nil {
		return Drift{}, err.Error()
	}
	drift, err := CompareToBaseline(lock, resolved.Config)
	if err != nil {
		return Drift{}, err.Error()
	}
	return drift, ""
}

// maxNamedImprovements bounds how many keys the one-line notice names before it
// counts the rest. A notice that printed forty keys is one an operator scrolls
// past, which is the failure mode the whole rule about nagging is about.
const maxNamedImprovements = 5

// Notice is the line the unprompted surfaces say, and the empty string when
// there is nothing to say -- which is the ordinary answer and the reason this
// can be printed on every run.
//
// It speaks the available class and nothing else. What the project changed is
// the project's, what both sides changed is waiting on a decision the operator
// has not been asked for, and an unprompted line about either would be the
// harness asking for attention it was told not to ask for. Both are still there
// for anybody who runs `yoyo config drift`.
func (d Drift) Notice() string {
	available := d.Available()
	if len(available) == 0 {
		return ""
	}
	named := make([]string, 0, maxNamedImprovements)
	for _, value := range available[:min(len(available), maxNamedImprovements)] {
		named = append(named, value.Key)
	}
	listed := strings.Join(named, ", ")
	if further := len(available) - len(named); further > 0 {
		listed += fmt.Sprintf(", and %d more", further)
	}
	return fmt.Sprintf("note: %s has improved %s this project has not edited (%s); `yoyo config drift` shows what each one was and is",
		d.Bundle, countOfValues(len(available)), listed)
}

func countOfValues(count int) string {
	if count == 1 {
		return "1 value"
	}
	return fmt.Sprintf("%d values", count)
}
