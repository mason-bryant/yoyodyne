package config

// Naming the exact model version an agent's turns are served by, without giving
// up the family alias that answers when the provider has not got it.
//
// A model selector here has always been a family alias that floats — "opus",
// "fable" — and the run evidence records what the provider actually served. That
// is the right default and stays the default: an alias follows the family's
// current best without anybody editing a file, which is what an operator wants
// almost all of the time.
//
// What it cannot do is hold a version still. A comparison across two weeks, a
// reproduction of something a particular version did, a persona tuned against
// one release — each of those needs the selector to stop floating, and the
// alias's whole virtue is that it does not. So an agent may name a version as
// well, and then its turns ask for that version.
//
// A pin is a promise about something the operator does not control. Versions are
// retired, and a pin the provider has stopped serving would be an agent that
// simply stops taking turns — the same stall failover exists to prevent, arrived
// at from the other direction. So the pin is a preference rather than a
// requirement: the version is asked for, and the family alias the agent already
// names answers when the provider has not got it. The alias is the fallback
// because the alias is by definition the family's latest, so there is no table
// here mapping versions to families and nothing to keep up to date when a family
// gains one.
//
// Naming a version is the whole of the switch. There is no enablement beside it,
// unlike failover, because the two settings answer different questions: failover
// asks whether an agent may be moved off the model it was chosen for, which is a
// judgement worth keeping while it is switched off, and this asks which version
// of that same model to ask for, which is either stated or not.

import (
	"fmt"
	"strings"
)

// modelVersionProblems reports what makes one agent's pinned version unusable.
// The agent's own selector and its failover alternate are passed because a pin
// is only wrong in relation to them: it is a version of the family the alias
// names, and it is not the model this agent's turn moves to when that family has
// no capacity.
//
// The alternate is the one it names rather than the one it would reach, so a pin
// is checked even where failover is switched off. A value nobody validates is one
// that fails the day somebody turns it on.
func modelVersionProblems(name, version, model, alternate string) []string {
	pinned := strings.TrimSpace(version)
	if pinned == "" {
		return nil
	}
	var problems []string
	if err := validateModelSelector(pinned); err != nil {
		problems = append(problems, fmt.Sprintf("agent %q model version %s", name, err))
	}
	// A pin naming the alias is not an error the harness can work around by
	// ignoring it: it would leave the agent looking pinned while nothing was held
	// still, and the fallback it would fall back to is itself.
	if pinned == strings.TrimSpace(model) {
		problems = append(problems, fmt.Sprintf("agent %q pins model version %q, which is the selector it already names; a pin is a version of the family the alias floats over", name, version))
	}
	// And failing over to the version the provider has just been found not to have
	// is not an alternate either. It is refused here rather than met as a turn
	// whose record says a model served it that never did.
	if pinned == strings.TrimSpace(alternate) {
		problems = append(problems, fmt.Sprintf("agent %q pins model version %q and names it as its failover model; the model a turn moves to when a family has no capacity is not the version that family was pinned to", name, version))
	}
	return problems
}

// AgentModelVersion is the exact version one configured agent's turns ask for,
// and empty for every agent that has named none — which is every agent until one
// does, and which is the floating alias behaving exactly as it always has.
//
// It answers for the invocations an agent makes as itself: its conversation and
// the rounds where another role asks it something. That is the same set
// AgentFailoverModel answers for, and it is the same set for the same reason —
// these are the turns a role takes, and they are where the substitution is
// recorded per turn.
func (c Config) AgentModelVersion(name string) string {
	return strings.TrimSpace(c.Agents[name].ModelVersion)
}
