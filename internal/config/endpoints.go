package config

// The pool an invocation is chosen from, keyed on endpoints.
//
// An account was the unit while there was one provider and one model per
// project: rotating accounts was the whole of the choice, because everything
// else about where an invocation went was fixed. It stops being the unit the
// moment a project names two providers or two models. The same account reaching
// two providers is two sandboxes with two postures; the same provider under two
// accounts is two subscriptions with two limits; the same account and provider
// asking two models is two capacity windows. A pool keyed on any one of those
// alone cannot say which of them refused, and — worse — cannot refuse to send a
// role somewhere its tool posture cannot be held.
//
// So the pool holds endpoints. The accounts underneath it rotate and reserve
// exactly as they always did, which is why the ordering here is the same
// ordering ChooseAccount reads: what changes is that the answer names the
// provider, the adapter that reaches it, the account, and the model together,
// and that an endpoint whose provider cannot serve the asking role is left out
// with the reason named rather than chosen and then refused by somebody else.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// EndpointChoice is one endpoint the pool holds, with the account it
// authenticates under. The two travel together because an invocation needs
// both: the endpoint is what the record says served the turn, and the account
// endpoint is where on this machine the provider is actually reached.
type EndpointChoice struct {
	Endpoint backend.Endpoint
	Account  AccountEndpoint
}

// EndpointFor is the endpoint one configured agent's invocation is made on when
// the account is already settled — a run that reserved one, or an agent assigned
// to one. It refuses an agent this configuration does not name, a provider this
// project does not name, and an endpoint the agent's role may not be served on,
// each with the reason named.
//
// The role check is here as well as at configuration load deliberately. Loading
// validates the configuration as it stands; this answers for the endpoint an
// invocation is actually about to be made on, which is the same question asked
// where it can still be refused.
func (c Config) EndpointFor(providers *backend.Registry, stateRoot, agentName, alias string) (EndpointChoice, error) {
	agent, named := c.Agents[strings.TrimSpace(agentName)]
	if !named {
		return EndpointChoice{}, fmt.Errorf("agent %q is not one this configuration names", agentName)
	}
	account, err := c.Endpoint(stateRoot, alias)
	if err != nil {
		return EndpointChoice{}, err
	}
	endpoint, err := providers.Endpoint(agent.Backend, account.Alias, agent.Model)
	if err != nil {
		return EndpointChoice{}, fmt.Errorf("resolve the endpoint agent %q runs on: %w", agentName, err)
	}
	if err := providers.EligibleFor(endpoint, agent.Role); err != nil {
		return EndpointChoice{}, fmt.Errorf("agent %q cannot be served: %w", agentName, err)
	}
	return EndpointChoice{Endpoint: endpoint, Account: account}, nil
}

// AgentEndpoint is the endpoint one configured agent's own invocations are made
// on. It is the endpoint form of AgentAccountAlias and inherits its answer,
// including why it is a stable choice rather than a rotating one: a conversation
// and an exchange round last longer than a run, and an agent moved between
// accounts each turn would have no provider session left to resume.
func (c Config) AgentEndpoint(providers *backend.Registry, stateRoot, agentName string) (EndpointChoice, error) {
	return c.EndpointFor(providers, stateRoot, agentName, c.AgentAccountAlias(agentName))
}

// ChooseEndpoint picks the endpoint the next invocation of one agent is served
// by: the pool rotated past the endpoint last served, the weekly budgets
// honoured, and an endpoint the agent's role may not be served on left out.
//
// The cursor is an endpoint rather than an account, which is what keying the
// pool on endpoints buys: a cursor left by an invocation on some other endpoint
// family — another provider, another model — does not rotate this one, so two
// agents on different endpoints do not read each other's position as their own.
// An endpoint the pool no longer holds leaves the order as it stands, which
// starts the rotation from the top rather than from nowhere.
//
// spentUSD is what each alias has cost over the window the budgets are stated
// in, exactly as ChooseAccount reads it: a budget is the operator's limit on an
// account, so it stays keyed on the account whichever endpoint is asking.
func (c Config) ChooseEndpoint(providers *backend.Registry, stateRoot, agentName string, lastServed backend.Endpoint, spentUSD map[string]float64) (EndpointChoice, error) {
	agent, named := c.Agents[strings.TrimSpace(agentName)]
	if !named {
		return EndpointChoice{}, fmt.Errorf("agent %q is not one this configuration names", agentName)
	}
	// Whether this agent's role may be served on this provider at all does not
	// vary by account, so it is asked once and refused before any rotation: a pool
	// that passed over every endpoint in turn would report a budget where the
	// answer is a posture.
	if err := providers.Serves(agent.Backend, agent.Role); err != nil {
		return EndpointChoice{}, fmt.Errorf("agent %q cannot be served: %w", agentName, err)
	}

	cursor := ""
	if lastServed.Provider == agent.Backend && strings.TrimSpace(lastServed.Model) == strings.TrimSpace(agent.Model) {
		cursor = lastServed.AccountAlias
	}
	order := c.rotatedAliases(cursor)
	for _, alias := range order {
		if !c.withinBudget(alias, spentUSD) {
			continue
		}
		return c.EndpointFor(providers, stateRoot, agentName, alias)
	}
	return EndpointChoice{}, c.noAccountLeft(order, spentUSD)
}
