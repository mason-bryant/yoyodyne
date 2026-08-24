package protectedpath

// The paths a grant does not reach.
//
// A grant is an exception to this package's refusal, which is the only refusal
// this package makes. It is not an exception to the provider's. Claude Code
// denies an agent's writes to its own settings files at the tool-permission
// layer, above whatever the harness permits and whatever a work item says: the
// editing tools are refused there however the run is configured, and the shell
// sandbox names the file and cannot be disabled by policy. An item that grants
// one of those paths has therefore admitted work no run it is admitted for can
// do, and the way that is discovered is a run spending its repair rounds against
// it — yoyodyne-ifd.153 spent three before anybody knew.
//
// So the set is recorded here, beside the grants it is checked against, and
// admission checks it before the work reaches the queue. Here rather than
// anywhere nearer the provider, because here is where a grant is read, and a
// boundary the harness knows about but keeps away from the grant is one every
// future admission gets to rediscover at the same price.
//
// The refusal names the provider, because what is wrong is not the item's
// judgement about the path. The change may well be the right one; it is somebody's
// to make by hand rather than a run's to be given, and an operator told only that
// a path is unreachable has nothing to act on. Whose software refuses it is the
// whole of what they can act on.

import (
	"errors"
	"fmt"
)

// ProviderPath is one path an agent's provider refuses writes to, whatever this
// harness grants.
type ProviderPath struct {
	// Path is the repository-relative path in the slash form a grant normalizes
	// to, so a grant and this are compared as paths rather than as the strings
	// somebody happened to write.
	Path string
	// Provider names what does the refusing.
	Provider string
	// Refusal is how that provider refuses, in terms somebody who met it would
	// recognize. It is recorded rather than described in general, because the
	// evidence for an entry here is exactly what some run saw.
	Refusal string
}

// ProviderPaths is every path known to be beyond a grant.
//
// It is a short evidenced list rather than a guess at a provider's whole
// posture. An entry here refuses work at admission, so a path added on suspicion
// costs the project items nobody needed to refuse; each of these is a refusal a
// run actually met. The list grows the same way — by something meeting the wall
// and reporting it — and a path that belongs here and is missing costs one item's
// repair budget rather than being lost, which is the right way round.
var ProviderPaths = []ProviderPath{
	{
		Path:     ".claude/settings.json",
		Provider: "Claude Code",
		Refusal:  "its editing tools are denied against settings files whatever the run is permitted, and its shell sandbox names this file and cannot be disabled by policy",
	},
	{
		Path:     ".claude/settings.local.json",
		Provider: "Claude Code",
		Refusal:  "it is the same settings file under another name, refused the same way and for the same reason",
	},
}

// ProviderInstruction is what the refused role is told to do about it, and it is
// the whole value of refusing here rather than three rounds into a run: the
// change is not wrong, and there is somebody who can make it. It says so, because
// a role told only "no" writes the item again with the grant worded differently
// and buys the same refusal twice.
const ProviderInstruction = "Take the grant out. That file is the operator's to change by hand, so say in the item what has to be in it and that a person puts it there, and admit whatever else the work needs as ordinary work. Nothing an item can say lifts a provider's refusal, so an item rewritten to grant the path again is the same item refused again."

// GrantRefusal is what admission says about one grant it will not admit. It
// names the path, the provider, how that provider refuses, and what to do
// instead, in that order: the first three are why no run can be given this, and
// the last is why hearing it here is better than discovering it later.
func (p ProviderPath) GrantRefusal() string {
	return fmt.Sprintf("a %q line names %s, which is a path no grant reaches: %s refuses an agent's writes to it — %s — above anything this harness permits, so granting it admits work the run it is admitted for cannot do. %s",
		GrantMarker, p.Path, p.Provider, p.Refusal, ProviderInstruction)
}

// BeyondGrant reports which recorded provider paths a set of grants reaches for,
// in the order they are recorded.
//
// A grant naming the file matches it, and so does a grant naming a directory it
// sits inside: an item that grants `.claude` has granted the settings file as
// surely as one that names it, and the run that finds out otherwise pays the same
// rounds either way.
func BeyondGrant(granted []string) []ProviderPath {
	grants := normalizeAll(granted)
	var beyond []ProviderPath
	for _, entry := range ProviderPaths {
		if within(entry.Path, grants) {
			beyond = append(beyond, entry)
		}
	}
	return beyond
}

// GrantProblems reports what is wrong with the grants an item's text carries,
// which is one thing: a grant naming a path the provider refuses. An empty
// result is the ordinary answer, because nearly no item grants anything at all.
//
// It is one predicate every door into the queue asks rather than each deciding
// for itself, for the reason the admission gap is: a door that asked a weaker
// question is the door such an item would arrive through. Which of an item's
// fields are passed is the caller's, because it is the caller that knows which of
// them the item will actually carry.
func GrantProblems(texts ...string) []error {
	var problems []error
	for _, beyond := range BeyondGrant(Grants(texts...)) {
		problems = append(problems, errors.New(beyond.GrantRefusal()))
	}
	return problems
}
