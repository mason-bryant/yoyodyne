package doctor

// Whether one provider window closing stops every role at once.
//
// Between 2026-09-08 and 09-13 it did. All five agents ran on one model, that
// model's seven-day capacity ran out with a reset five days off, and no agent
// named an alternate — failover had shipped, deliberately off by default so the
// operator could choose it per agent, and this project had never turned it on.
// The harness recorded 134 refusals and waited the whole window out. The
// condition that made it five days instead of one turn was in the
// configuration the whole time, and it is one line to state.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// checkFailover says whether the agents share one model with nothing to fail
// over to. It is a warning rather than a problem: every run proceeds exactly as
// it would have, right up to the window closing, and what it costs is paid then
// — which is what a warning is for.
//
// The condition is the exact one that held this product for five days, and no
// wider. A project whose agents run on two models is stopped whole only when
// both windows close, and one where some agents name an alternate keeps those
// agents moving; both are stated in the healthy line so an operator can see
// the shape, and neither is a finding.
func (d *diagnosis) checkFailover(resolved config.Resolved) Finding {
	const check = "failover"
	cfg := resolved.Config
	if len(cfg.Agents) == 0 {
		return Finding{Check: check, Status: StatusOK, Summary: "no agents are configured, so no window can hold any"}
	}
	endpoints := map[string]struct{}{}
	alternates := 0
	for _, agent := range cfg.Agents {
		endpoints[endpointName(agent.Backend, agent.Model)] = struct{}{}
		if agent.Failover.Alternate() != "" {
			alternates++
		}
	}
	named := make([]string, 0, len(endpoints))
	for endpoint := range endpoints {
		named = append(named, endpoint)
	}
	sort.Strings(named)
	agents := countOf(len(cfg.Agents), "agent")

	if len(named) == 1 && alternates == 0 {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: fmt.Sprintf("every agent runs on one model, %s, and none names an alternate", named[0]),
			Detail: "a capacity window closing on that model stops every role at once until it lifts, and nothing fails over; " +
				"set failover.enabled: true and failover.model on each agent to name the model that takes its turns while the window stands",
			Remedy: fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		}
	}
	summary := fmt.Sprintf("the %s run on %s", agents, strings.Join(named, " and "))
	switch {
	case alternates == len(cfg.Agents):
		summary += ", and every one names an alternate"
	case alternates == 1:
		summary += ", and 1 of them names an alternate"
	case alternates > 1:
		summary += fmt.Sprintf(", and %d of them name an alternate", alternates)
	default:
		summary += ", and none names an alternate"
	}
	return Finding{Check: check, Status: StatusOK, Summary: summary}
}

// endpointName is a model as the finding names it, qualified by the provider
// that serves it: two providers can spell one model name, and the window that
// closes is one provider's.
func endpointName(provider domain.Backend, model string) string {
	model = strings.TrimSpace(model)
	if provider == "" {
		return model
	}
	return model + " on " + string(provider)
}
