package config

// What serves an agent's turn when the model it is configured for has no
// capacity.
//
// On 2026-09-07 at 06:00 the model the management roles run on closed its
// capacity window while the model the developers and reviewers run on still had
// one. The deciders stopped and the doers did not, so every item held for a
// development manager decision sat behind a role that could not take a turn,
// until a person hand-edited three agents onto the other model. Nothing about
// that was a failure the harness could see: each turn was refused rather than
// answered badly, and a refused turn is exactly what waiting out a window looks
// like.
//
// So an agent may name one alternate and be served by it while its own model's
// window is closed. It is per agent and off until the agent says otherwise,
// because which personas are worth serving from a second model is the operator's
// judgement rather than the harness's: a management role that must keep deciding
// and a developer whose work can wait out a window are different answers to the
// same question.
//
// What this never does is widen anything. The alternate is a model the operator
// wrote in this agent's own block, so failover chooses between two stated models
// and can never reach one nobody named — the same line every other key here
// holds: configuration selects, and never grants.

import (
	"fmt"
	"strings"
)

// Failover is one agent's answer to its model having no capacity.
//
// Enabled and Model are read together and stated together. A named alternate
// with Enabled false is how an operator turns the behaviour off without losing
// the model they had chosen, which is what "willing to try it" needs: turning it
// back on is one word rather than a decision made again.
type Failover struct {
	// Enabled says this agent's turn may be served by the alternate below. It is
	// false unless the agent says otherwise, deliberately: an agent that acquired
	// failover by inheriting a bundle or by upgrading the executable would be one
	// nobody chose it for.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Model is the permitted alternate — the one model this agent's turn may be
	// served by instead. There is exactly one because a list is a routing policy
	// and this is a fallback: the second model either has capacity or the turn
	// waits, and a third to try after it would be the harness deciding which
	// models an agent is interchangeable across.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

// Alternate is the model this agent's turn may be served by, and empty where
// nothing may serve it. Reading the enablement and the model as one answer is
// what keeps every caller from having to remember that a named alternate an
// operator switched off is not one.
func (f Failover) Alternate() string {
	if !f.Enabled {
		return ""
	}
	return strings.TrimSpace(f.Model)
}

// problems reports what makes one agent's failover block unusable. The agent's
// own model is passed because the two are only wrong together: an alternate is
// the model that is not this one.
func (f Failover) problems(name, model string) []string {
	var problems []string
	alternate := strings.TrimSpace(f.Model)
	if f.Enabled && alternate == "" {
		problems = append(problems, fmt.Sprintf("agent %q enables failover and names no alternate model; there is nothing for its turn to be served by", name))
	}
	if alternate != "" {
		if err := validateModelSelector(alternate); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q failover %s", name, err))
		}
		// Failing over to the model whose window just closed is a second refusal
		// rather than an alternate, so it is refused here rather than met as a turn
		// that fails twice.
		if alternate == strings.TrimSpace(model) {
			problems = append(problems, fmt.Sprintf("agent %q names %q as its own failover model; an alternate is the model that is not the one whose window closed", name, alternate))
		}
	}
	return problems
}

// AgentFailoverModel is the alternate one configured agent's turn may be served
// by, and empty for every agent that has not enabled failover — which is every
// agent until one says otherwise.
//
// It answers for an invocation that belongs to an agent rather than to a work
// item, which is the same set AgentAccountAlias answers for: a conversation and
// an exchange round are turns a role takes, and a role that cannot take one is
// the shape this exists for.
func (c Config) AgentFailoverModel(name string) string {
	return c.Agents[name].Failover.Alternate()
}
