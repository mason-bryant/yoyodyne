package readmodel

// The provider refusing every role at once, read from the refusals the harness
// already wrote down.
//
// Between 2026-09-08T04:55Z and 2026-09-13 nothing landed. All five agents ran
// on one model, that model's seven-day capacity ran out with a reset five days
// off, and no agent named an alternate. The harness knew: it recorded 134
// refusals in the usage-limit log, every one carrying the reset time, and fired
// the development manager's sweep twenty times a day into the same refusal. It
// waited the whole window out, and the operator heard about it from his
// assistant five days later.
//
// Every refusal was said, once each, in the channel. What nothing said was the
// fact they added up to: not one turn refused, but every role held, on a known
// reset, with nothing configured to move the work elsewhere. That is a different
// message from a line stopped for reasons nobody can name — the stall alarm — and
// from the session's own account of waiting out a window, which is only written
// when the session choosing work is the thing refused. Here the session was
// idle over items waiting on a decision, and the role that would have decided
// was the one being refused.
//
// So this derives the hold from the log and the agents' configuration, once,
// for every surface that says it: the channel repeats it while it stands, the
// four lines carry it as the banner, and the attention line names whose move it
// is.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UsageLimits is the durable account of the provider refusing the harness
// outside a run. It is satisfied by *runstate.UsageLimitStore.
type UsageLimits interface {
	List() ([]runstate.UsageLimitExhaustion, error)
}

// AgentEndpoint is one configured agent as this reading needs it: what it asks
// for, and what it may be served by instead. It is passed in rather than read
// from the configuration here because the read model reads records and not
// files, and because a test has to be able to state a project's agents in three
// lines.
type AgentEndpoint struct {
	Name     string
	Provider domain.Backend
	Model    string
	// Alternate is the model this agent fails over to, and empty for an agent that
	// fails over to nothing — which is failover off, whatever else its block says.
	Alternate         string
	AlternateProvider domain.Backend
}

// last is the model an agent's turn ends on: its alternate where it names one,
// because the alternate is only asked once the model itself has refused, and its
// own model otherwise. A refusal of that model is a turn nothing else will take.
func (a AgentEndpoint) last() string {
	if alternate := strings.TrimSpace(a.Alternate); alternate != "" {
		return alternate
	}
	return strings.TrimSpace(a.Model)
}

// chain is the whole of what an agent asks for, in order, as one comparable
// string. Two agents with equal chains are refused together.
func (a AgentEndpoint) chain() string {
	return strings.Join([]string{
		strings.TrimSpace(string(a.Provider)), strings.TrimSpace(a.Model),
		strings.TrimSpace(string(a.AlternateProvider)), strings.TrimSpace(a.Alternate),
	}, "\x00")
}

// CapacityHold is the provider holding every configured agent at once: since
// when, until when where the provider said, and what the configuration left
// nothing to fail over to.
type CapacityHold struct {
	Holding bool `json:"holding,omitempty"`
	// Since is the earliest standing refusal, which is when the hold began as far
	// as the record can say.
	Since time.Time `json:"since,omitempty"`
	// ResetsAt is when the provider said the last of the standing refusals lifts.
	// It is zero where the provider named no time, which is a different fact from
	// a wait of unknown length: the harness asks again rather than being told
	// when.
	ResetsAt time.Time `json:"resets_at,omitempty"`
	// Refusals is how many turns the provider has actually stopped inside this
	// hold. A substitution is not one of them: the work carried on.
	Refusals int `json:"refusals,omitempty"`
	// Kind is the provider's own name for the limit, from the latest refusal, and
	// empty where it named none.
	Kind string `json:"kind,omitempty"`
	// Agents is every configured agent, by name and sorted, because a hold is
	// precisely the case where every one of them is held.
	Agents []string `json:"agents,omitempty"`
	// Models is the distinct models the agents ask for, and Alternates the distinct
	// models they fail over to. Both are sorted, and Alternates is empty for a
	// project that fails over to nothing — which is the configuration this
	// reading exists to name.
	Models     []string `json:"models,omitempty"`
	Alternates []string `json:"alternates,omitempty"`
}

// ReadCapacityHold says whether the provider is holding every configured agent
// at the moment asked about.
//
// A refusal stands for as long as runstate.UsageLimitExhaustion.WindowClosed
// says it does, which is the same reading failover takes of the same log: the
// provider's own reset time where it named a usable one, and the configured
// probe interval where it did not.
//
// An agent is held when the model its turn ends on is refused: its alternate
// where it names one, since the alternate is only asked once its own model has
// refused, and its own model otherwise. The refusals are matched by model name,
// as failover matches them — a provider is written on a refusal only where a
// turn crossed one, so it cannot be relied on to be there.
//
// A refusal that names no model was written by a process that did not say
// which model it asked, which is every process before the model was recorded
// and is what the whole of the 2026-09-08 log looks like. It is read as a
// refusal of the one chain every agent shares, where they share one: on a
// project whose agents all ask for the same thing, whichever of them was refused
// was refused on that. Where the agents differ it names nobody, because an
// unnamed refusal cannot be attributed and a hold invented over one would send
// somebody to look at roles that are being served.
//
// Two things keep this from firing over a machine that is working. At least one
// standing refusal has to be a turn the provider actually stopped rather than a
// substitution something served through — a project whose every refusal was
// answered by an alternate has no role held, however many windows are closed —
// and every agent has to be held, not most of them: a project with one agent
// still being served is a project whose work is moving, and that is the stall
// alarm's business rather than this reading's.
func ReadCapacityHold(agents []AgentEndpoint, refusals []runstate.UsageLimitExhaustion, now time.Time, unknownResetPause time.Duration) CapacityHold {
	if len(agents) == 0 {
		return CapacityHold{}
	}
	// What is standing, by model. An availability substitution names the same
	// field and means something else: the provider has not got that selector,
	// which is not a window and must not be read as one.
	refused := map[string]bool{}
	var stopped []runstate.UsageLimitExhaustion
	unnamed := false
	for _, refusal := range refusals {
		if refusal.Substituted() && refusal.Reason() == runstate.SubstitutedForAvailability {
			continue
		}
		if !refusal.WindowClosed(now, unknownResetPause) {
			continue
		}
		model := strings.TrimSpace(refusal.Model)
		if model != "" {
			refused[model] = true
		}
		if refusal.Substituted() {
			continue
		}
		stopped = append(stopped, refusal)
		if model == "" {
			unnamed = true
		}
	}
	if len(stopped) == 0 {
		return CapacityHold{}
	}
	shared := true
	for _, agent := range agents[1:] {
		if agent.chain() != agents[0].chain() {
			shared = false
			break
		}
	}
	hold := CapacityHold{Holding: true}
	models, alternates := map[string]struct{}{}, map[string]struct{}{}
	for _, agent := range agents {
		held := refused[agent.last()] || (unnamed && shared)
		if !held {
			return CapacityHold{}
		}
		hold.Agents = append(hold.Agents, agent.Name)
		if model := strings.TrimSpace(agent.Model); model != "" {
			models[model] = struct{}{}
		}
		if alternate := strings.TrimSpace(agent.Alternate); alternate != "" {
			alternates[alternate] = struct{}{}
		}
	}
	sort.Strings(hold.Agents)
	hold.Models = sortedKeys(models)
	hold.Alternates = sortedKeys(alternates)
	// When the hold began and when the provider says it lifts are read from the
	// refusals that actually stopped something: the earliest of them is the
	// start, and the latest reset any of them named is the end. The kind is the
	// latest refusal's, because that is the limit the provider is quoting now.
	for _, refusal := range stopped {
		if hold.Since.IsZero() || refusal.At.Before(hold.Since) {
			hold.Since = refusal.At.UTC()
		}
		if refusal.ResetsAt != nil && refusal.ResetsAt.After(hold.ResetsAt) {
			hold.ResetsAt = refusal.ResetsAt.UTC()
		}
	}
	hold.Refusals = len(stopped)
	hold.Kind = strings.TrimSpace(stopped[len(stopped)-1].Kind)
	return hold
}

// Says is the hold as the one sentence every surface states it in. It carries
// the cause first, in the shape the operator asked a pause to be said in, and it
// carries a remedy where the provider's window does not: nothing shortens a
// window, and failover is what would have moved the work off it.
//
// The reset time is said in full rather than as the hour, because a seven-day
// window resets on another day and "until 03:00Z" read on a Tuesday is a
// sentence that lies by omission.
func (h CapacityHold) Says() string {
	if !h.Holding {
		return ""
	}
	head := "Every role is paused on the provider's usage window"
	if h.ResetsAt.IsZero() {
		head += ", and the provider named no time it lifts"
	} else {
		head += " until " + h.ResetsAt.UTC().Format(time.RFC3339)
	}
	agents := fmt.Sprintf("all %d agents", len(h.Agents))
	if len(h.Agents) == 1 {
		agents = "the one agent"
	}
	var why string
	switch {
	case len(h.Alternates) == 0:
		why = fmt.Sprintf("%s run on %s and none names an alternate, so nothing fails over", agents, list(h.Models))
	default:
		why = fmt.Sprintf("%s run on %s and fail over to %s, and the provider is refusing both", agents, list(h.Models), list(h.Alternates))
	}
	return fmt.Sprintf("%s: %s; %s refused since %s",
		head, why, count(h.Refusals, "turn"), h.Since.UTC().Format(time.RFC3339))
}

// Whose is whose move it is. The window is the provider's, and that is not the
// whole answer the way it is for a session waiting one out: the configuration
// that made the window hold every role is the operator's, and changing it is
// what ends the next one early.
func (h CapacityHold) Whose() string {
	return "the operator's — the window lifts on the provider's clock, and enabling failover on the agents is what would move the work onto another model before it does"
}

// Mark names the hold durably, so a surface that repeats it while it stands can
// say which one it is standing on and re-arm its clock when a different one
// takes over. It is the reset the provider named where it named one, because a
// second window is a second thing to say and the same one is not, and the
// moment the hold began otherwise.
func (h CapacityHold) Mark() string {
	switch {
	case !h.Holding:
		return ""
	case !h.ResetsAt.IsZero():
		return "capacity:" + h.ResetsAt.UTC().Format(time.RFC3339)
	default:
		return "capacity:opened " + h.Since.UTC().Format(time.RFC3339)
	}
}

// Attention is the hold as one thing waiting on a person, in the shape the
// attention line's other entries say it: what, since when, and whose move. It
// is the short form rather than Says, because Says is the banner above the
// lines and a reading that said the whole sentence twice would be repetition
// rather than emphasis.
func (h CapacityHold) Attention() (Attention, bool) {
	if !h.Holding {
		return Attention{}, false
	}
	what := "every role is held by the provider's usage window, since " + h.Since.UTC().Format(time.RFC3339)
	if !h.ResetsAt.IsZero() {
		what += ", until " + h.ResetsAt.UTC().Format(time.RFC3339)
	}
	return Attention{What: what, Whose: h.Whose()}, true
}

// CapacityHoldOf reads the hold from a set of sources, and says why it could
// not where it could not. A reading with no log wired, or no agents to hold, is
// not a hold and not a problem: it is a caller that never asked.
func CapacityHoldOf(sources Sources, now time.Time) (CapacityHold, string) {
	if sources.UsageLimits == nil || len(sources.Agents) == 0 {
		return CapacityHold{}, ""
	}
	refusals, err := sources.UsageLimits.List()
	if err != nil {
		return CapacityHold{}, fmt.Sprintf("what the provider has refused could not be read: %v", err)
	}
	return ReadCapacityHold(sources.Agents, refusals, now, sources.UnknownResetPause), ""
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// list says a few names the way a sentence does.
func list(names []string) string {
	switch len(names) {
	case 0:
		return "nothing the record names"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}
