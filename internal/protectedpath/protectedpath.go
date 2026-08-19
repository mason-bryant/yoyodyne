// Package protectedpath decides which files a developer's change may touch at
// all, before anybody is asked to judge what it did to them.
//
// The documents a run derives its intent from — the project configuration, the
// product's own artifacts, the designs, and the decision records with the
// invariants extracted from them — are upstream of the change being made. A
// developer that edits one is redefining what its own work is measured against,
// and no reading of the diff tells a legitimate edit from that: both look like a
// file that changed. What does tell them apart is whether anybody decided the
// path was in scope, and that decision exists in one place already. The work
// item is written and reviewed before the run starts, so an exception stated in
// it is an exception somebody admitted, and an exception stated nowhere is one
// the developer granted itself.
//
// So these paths are default-deny for a developer's diff, and the way out is the
// work item rather than the run. What that buys is determinism at the point the
// cost is lowest: a reviewer catches this class by judgement, one Opus review at
// a time, and the same finding here costs a string comparison. The refusal names
// the grant mechanism for the same reason it exists at all — a developer that
// genuinely needs one of these paths has somewhere to raise it, and a gate with
// no stated way out is a gate agents work around.
package protectedpath

import (
	"path"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// GrantMarker is how a work item admits one of these paths into a run's scope.
// It is a deliberately unlovely token rather than a phrase like "protected
// paths", because item text discusses these paths in prose — the item that
// introduced this gate names every one of them in its own description — and a
// marker that ordinary prose can produce is a marker that grants by accident.
const GrantMarker = "protected-path grant:"

// GrantInstruction is what a refused developer is told to do about it. It is a
// constant here, beside the rule it explains, because a refusal that does not
// say how an exception is made is a refusal an agent has to guess its way past.
//
// It says where a grant lives and not which role puts it there. Who writes an
// item's text is a question about the fixed set of roles and their authority,
// which is the architect's to settle and not something a refusal message should
// decide by being worded one way rather than another.
const GrantInstruction = "These paths are yours to change only when the work item says so. An item grants one by naming it after `" + GrantMarker +
	"` on a line of its own, and that grant covers every attempt the item makes. Nothing you write grants a path: the item is written and reviewed before the run starts, which is what makes an exception somebody's decision rather than yours. " +
	"If your work genuinely needs one of these paths, take it back out of your change, and use your summary and an amendment proposal to say which path you need and why. The grant goes into the work item, which is not yours to write."

// Set is the paths one project protects, as repository-relative directory
// prefixes in slash form.
type Set struct {
	directories []string
}

// Protect builds the set a configuration describes: the harness's own project
// directory and the artifact homes the roles upstream of a developer own. It is
// derived from the configuration rather than fixed, because a project that keeps
// its designs somewhere else has not thereby made them a developer's to rewrite.
//
// The invariants home is named separately from the decisions home it sits inside
// by default, so a project that moves it out still protects it.
func Protect(cfg config.Config) Set {
	return protect(
		config.DirectoryName,
		cfg.Product.Specifications,
		cfg.Product.Designs,
		cfg.Product.Decisions,
		cfg.Product.Invariants,
	)
}

func protect(directories ...string) Set {
	set := Set{}
	for _, directory := range directories {
		if clean, ok := normalize(directory); ok {
			set.directories = appendUnique(set.directories, clean)
		}
	}
	sort.Strings(set.directories)
	return set
}

// Directories is what this set protects, in a stable order, so a refusal can
// tell a developer the rule rather than only the paths it caught.
func (s Set) Directories() []string {
	return append([]string(nil), s.directories...)
}

// Refused reports which of a change's paths this set protects and the work item
// did not grant. An empty result is the ordinary answer: most changes touch none
// of these paths, and a change whose every protected path is granted is as clear
// as one that touched none.
//
// Both sides are matched as paths rather than as strings. A grant naming a
// directory covers what is inside it, and a grant naming a file covers that file
// alone, which is what lets an item admit one design document without admitting
// the design home.
func (s Set) Refused(changed, granted []string) []string {
	grants := make([]string, 0, len(granted))
	for _, grant := range granted {
		clean, ok := normalize(grant)
		if !ok {
			continue
		}
		grants = append(grants, clean)
	}
	var refused []string
	for _, candidate := range changed {
		clean, ok := normalize(candidate)
		if !ok {
			continue
		}
		if !within(clean, s.directories) || within(clean, grants) {
			continue
		}
		refused = appendUnique(refused, clean)
	}
	sort.Strings(refused)
	return refused
}

// Grants reads the paths a work item's own text admits. Every field of the item
// is read, because which field an operator writes a grant into is a habit rather
// than a rule, and a grant that only counts in one of them is a grant that
// silently does not count.
func Grants(texts ...string) []string {
	var granted []string
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			remainder, found := cutMarker(line)
			if !found {
				continue
			}
			for _, entry := range strings.FieldsFunc(remainder, isSeparator) {
				if clean, ok := normalize(strings.Trim(entry, "`'\"*_()[]")); ok {
					granted = appendUnique(granted, clean)
				}
			}
		}
	}
	sort.Strings(granted)
	return granted
}

// cutMarker finds the grant marker at the start of one line and returns what
// follows it. The marker has to start the line — after whatever a Markdown
// bullet, quote, or emphasis put in front of it — so a sentence that mentions the
// marker while discussing it does not grant anything.
func cutMarker(line string) (string, bool) {
	trimmed := strings.TrimLeft(strings.TrimSpace(line), "-*>#` \t")
	if len(trimmed) < len(GrantMarker) || !strings.EqualFold(trimmed[:len(GrantMarker)], GrantMarker) {
		return "", false
	}
	return trimmed[len(GrantMarker):], true
}

func isSeparator(r rune) bool {
	return r == ',' || r == ';' || r == ' ' || r == '\t'
}

// normalize turns a written path into the repository-relative slash form both
// sides are compared in. It reports false for anything that cannot name a file
// inside the repository, and for the repository root itself: a grant of "." is a
// grant of everything, which is not an exception anybody stated.
func normalize(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", false
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// within reports whether a normalized path is one of the prefixes or sits inside
// one of them. The separator is required, so "docs/products" is not inside
// "docs/product".
func within(candidate string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
