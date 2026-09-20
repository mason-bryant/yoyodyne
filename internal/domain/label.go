package domain

// A work item's labels, and the developer slot that prefers one.
//
// A label is the tracker's own: a word the product manager or the development
// manager puts on an item, which everything that filters on it then reads. It is
// defined here rather than in the tracker client because two things hold a label
// to the same rule and only one of them talks to the tracker — the client refuses
// to write a label that is not one, and the configuration refuses a developer
// slot that prefers one that is not. One rule in one place is what keeps a slot
// from preferring a label no item could ever carry.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// labelPattern is what a label the harness writes or reads is held to. bd itself
// stores any string, spaces and all, and the narrowing is deliberate: a label is
// a word things are filtered on, so it is an identifier — one token, no
// whitespace, nothing bd's comma-separated flag spelling would split — and a
// label that is a sentence is a note wearing a label's clothes.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// MaxLabelBytes bounds one label. It is exported beside ValidateLabel so a
// caller can state the bound it refuses on rather than only that it refused.
const MaxLabelBytes = 64

// ValidateLabel refuses a label the harness will not write and no slot may
// prefer: anything that is not one identifier-shaped token within MaxLabelBytes.
// bd would accept what this refuses, so a caller that did not ask would find out
// from nobody.
func ValidateLabel(label string) error {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return errors.New("a label cannot be empty")
	}
	if len(trimmed) > MaxLabelBytes {
		return fmt.Errorf("label %q is %d bytes, limit is %d", trimmed, len(trimmed), MaxLabelBytes)
	}
	if !labelPattern.MatchString(trimmed) {
		return fmt.Errorf("label %q is not an identifier: one word of letters, digits, dots, underscores, and hyphens, starting with a letter or digit", trimmed)
	}
	return nil
}

// DeveloperSlot is what one of the configured developer slots prefers. A slot is
// one unit of execution.max_concurrent_developers — the capacity one developer
// run takes — and a slot that prefers a label pulls the backlog's ready work
// carrying that label ahead of the rest, and the rest only when none of its
// label's work is ready. A slot preferring nothing pulls in the product
// manager's order, which is what every slot did before this existed.
//
// It is a preference and never a permission. A slot with a preference runs
// exactly what any slot runs, under the same contract, the same checks, and the
// same reviewer: configuration selects what a slot pulls first and never widens
// what it may do, which is the invariant configuration-never-grants-authority.
type DeveloperSlot struct {
	// Prefer is the labels this slot pulls first, any one of which on an item is
	// enough. Empty is a slot with no preference, which is how a project says
	// slot 1 prefers nothing and slot 2 prefers something.
	Prefer []string `yaml:"prefer,omitempty" json:"prefer,omitempty"`
}

// Preferring reports a slot that pulls labelled work first.
func (s DeveloperSlot) Preferring() bool {
	return len(s.Prefer) > 0
}

// Prefers reports whether an item carrying these labels is what the slot pulls
// first. Labels are compared exactly, as the tracker stores them: "Reliability"
// and "reliability" are two labels, and a slot that folded them would pull work
// nobody labelled for it.
func (s DeveloperSlot) Prefers(labels []string) bool {
	for _, preferred := range s.Prefer {
		for _, label := range labels {
			if strings.TrimSpace(label) == strings.TrimSpace(preferred) {
				return true
			}
		}
	}
	return false
}

// Problems is everything wrong with what a slot prefers: a label the tracker
// would not carry, and a label named twice. A slot preferring nothing has none.
func (s DeveloperSlot) Problems() []error {
	var problems []error
	seen := make(map[string]struct{}, len(s.Prefer))
	for _, label := range s.Prefer {
		if err := ValidateLabel(label); err != nil {
			problems = append(problems, err)
			continue
		}
		trimmed := strings.TrimSpace(label)
		if _, repeated := seen[trimmed]; repeated {
			problems = append(problems, fmt.Errorf("label %q is named twice", trimmed))
		}
		seen[trimmed] = struct{}{}
	}
	return problems
}
