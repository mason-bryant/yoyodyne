package config

// A developer model chosen per work item by label.
//
// Model spend follows the work rather than the role: a docs item and a change
// to the scheduler are both developer runs, and only one of them needs the
// developer's own model. execution.developer_models is how a project says so —
// an ordered list of label-to-model entries, read when a run starts, against
// the labels the item carried when it was pulled.
//
// It selects a model and nothing else. A run on a mapped model is claimed,
// developed, checked, reviewed, and promoted exactly as any run is, under the
// same contract and the same authority table, which is the invariant
// configuration-never-grants-authority. The reviewer's model is not reachable
// from here at all: the mapping has no key for it, because a reviewer's posture
// is a safety property rather than a spend decision.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DeveloperModelRule is one entry of execution.developer_models: a label, and
// the model a run over an item carrying it asks for.
//
// One label per entry is deliberate. An item can carry several the mapping
// names, and what it then takes is the first entry in the mapping's order, so
// the order has to be a reading of the file rather than of a set — and an entry
// preferring several labels would make "the first match" a question about which
// of that entry's labels matched first.
type DeveloperModelRule struct {
	Label string `yaml:"label" json:"label"`
	Model string `yaml:"model" json:"model"`
}

// DeveloperModelChoice is the model one run's developer invocations ask for and
// why that model. The reason is recorded with the run because the mapping is
// configuration and configuration is edited: a run that says only which model it
// asked for cannot afterwards say which entry sent it there, or that no entry
// did.
type DeveloperModelChoice struct {
	Model  string
	Reason string
}

// Chosen reports a choice this mapping actually made. A project that configured
// no mapping makes none, and such a run records nothing here and asks for the
// developer's configured model, which is exactly what every run did before the
// mapping existed.
func (c DeveloperModelChoice) Chosen() bool {
	return strings.TrimSpace(c.Model) != ""
}

// ResolveDeveloperModel is the model a run over an item carrying these labels
// asks for, and why.
//
// The mapping is read in its own order and the first entry any of the labels
// matches is what the item takes; the rest are named in the reason rather than
// dropped, because an operator reading a run that took sonnet over an item they
// remember labelling for opus is reading exactly the case the order decided. An
// item carrying no label the mapping names takes the developer's configured
// model, and the reason says so — an unmapped item is the ordinary case, and a
// run that recorded nothing about it would be indistinguishable from one the
// mapping was never read for.
//
// Labels are compared exactly, as the tracker stores them and as a developer
// slot's preference does: "Docs" and "docs" are two labels, and a mapping that
// folded them would move work nobody labelled for it onto another model.
func ResolveDeveloperModel(mapping []DeveloperModelRule, labels []string, configured string) DeveloperModelChoice {
	if len(mapping) == 0 {
		return DeveloperModelChoice{}
	}
	matched := make([]DeveloperModelRule, 0, len(mapping))
	for _, rule := range mapping {
		if carries(labels, rule.Label) {
			matched = append(matched, rule)
		}
	}
	if len(matched) == 0 {
		return DeveloperModelChoice{
			Model: configured,
			Reason: fmt.Sprintf("the item carries no label execution.developer_models names, so it takes the developer's configured model %s",
				strings.TrimSpace(configured)),
		}
	}
	taken := matched[0]
	reason := fmt.Sprintf("the item's %q label is mapped to %s by execution.developer_models",
		strings.TrimSpace(taken.Label), strings.TrimSpace(taken.Model))
	if len(matched) > 1 {
		reason += ", which is the first of the item's labels the mapping names; it also carries " + namedRules(matched[1:])
	}
	return DeveloperModelChoice{Model: strings.TrimSpace(taken.Model), Reason: reason}
}

// carries reports an item labelled with exactly this label.
func carries(labels []string, label string) bool {
	wanted := strings.TrimSpace(label)
	for _, carried := range labels {
		if strings.TrimSpace(carried) == wanted {
			return true
		}
	}
	return false
}

// namedRules is the entries an item matched and did not take, said as a phrase:
// `"tests", mapped to haiku`.
func namedRules(rules []DeveloperModelRule) string {
	named := make([]string, 0, len(rules))
	for _, rule := range rules {
		named = append(named, fmt.Sprintf("%q, mapped to %s", strings.TrimSpace(rule.Label), strings.TrimSpace(rule.Model)))
	}
	if len(named) == 1 {
		return named[0]
	}
	return strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1]
}

// developerModelProblems refuses a mapping entry nothing could ever act on: a
// label the tracker would not carry, a model selector that cannot name a model,
// and a label mapped twice. The last is refused for the reason a slot list
// longer than the capacity is — the first match in the mapping's order is what
// an item takes, so a second entry for one label is a mapping the operator
// believes is active and that nothing will ever reach.
func developerModelProblems(execution Execution) []string {
	var problems []string
	seen := make(map[string]struct{}, len(execution.DeveloperModels))
	for index, rule := range execution.DeveloperModels {
		entry := fmt.Sprintf("execution.developer_models entry %d", index+1)
		if err := domain.ValidateLabel(rule.Label); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", entry, err))
		} else {
			label := strings.TrimSpace(rule.Label)
			if _, repeated := seen[label]; repeated {
				problems = append(problems, fmt.Sprintf("%s: label %q is mapped twice; an item takes the first entry in the mapping's order that its labels match, so a second entry for one label is one nothing would ever act on", entry, label))
			}
			seen[label] = struct{}{}
		}
		if err := ValidateModelSelector(rule.Model); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", entry, err))
		}
	}
	return problems
}
