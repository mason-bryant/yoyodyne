package readmodel

// The program manager instances, as the read model carries them.
//
// `docs/designs/program-manager.md` has every surface read an instance from one
// query — the dashboard's card, `yoyo status --json` under
// `standing.program_managers`, and the other instances' opening lines — so that
// the page, the terminal, and the channel cannot disagree about one. What is
// here is the first thing that query carries: the instance's open restart
// requests, which the design says are shown on its report and in the standing
// until the supervisor's pass (yoyodyne-ifd.413) acts on them. The instance's
// status, its lane, and its report are their own work under yoyodyne-ifd.430.13
// and join this entry when they land.

import (
	"fmt"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RestartRequests is the durable log of program managers' restart requests,
// as the read model asks about it. It is satisfied by
// *runstate.RestartRequestStore.
type RestartRequests interface {
	Open() ([]runstate.RestartRequest, error)
}

// ProgramManager is one program manager instance as the read model carries it.
type ProgramManager struct {
	// Agent is the instance: the configured agent's name.
	Agent string `json:"agent"`
	// RestartRequests is every request the instance made that nothing has
	// answered, oldest first, carried whole in the store's own type. It is empty
	// rather than absent for an instance with none.
	RestartRequests []runstate.RestartRequest `json:"restart_requests"`
}

// ReadProgramManagers is every program manager instance: each one the
// configuration names, and each one with an open request on record whether or
// not it is still configured — a request outlives an edit to the configuration,
// and one that is still open is still somebody's to answer. The problem is a
// request log that could not be read; the instances are still listed, and
// their requests are not reported as none.
func ReadProgramManagers(sources Sources) ([]ProgramManager, string) {
	byAgent := map[string]*ProgramManager{}
	for _, agent := range sources.ProgramManagers {
		byAgent[agent] = &ProgramManager{Agent: agent, RestartRequests: []runstate.RestartRequest{}}
	}
	var problem string
	if sources.RestartRequests != nil {
		open, err := sources.RestartRequests.Open()
		if err != nil {
			problem = fmt.Sprintf("the program managers' restart requests could not be read: %v", err)
		}
		for _, request := range open {
			entry, known := byAgent[request.Agent]
			if !known {
				entry = &ProgramManager{Agent: request.Agent, RestartRequests: []runstate.RestartRequest{}}
				byAgent[request.Agent] = entry
			}
			entry.RestartRequests = append(entry.RestartRequests, request)
		}
	}
	if len(byAgent) == 0 {
		return nil, problem
	}
	agents := make([]string, 0, len(byAgent))
	for agent := range byAgent {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	instances := make([]ProgramManager, 0, len(agents))
	for _, agent := range agents {
		instances = append(instances, *byAgent[agent])
	}
	return instances, problem
}

// ProgramManagerOf is one instance's query: what ReadProgramManagers carries
// for it, and whether the read model knows the instance at all.
func ProgramManagerOf(sources Sources, agent string) (ProgramManager, bool, string) {
	instances, problem := ReadProgramManagers(sources)
	for _, instance := range instances {
		if instance.Agent == agent {
			return instance, true, problem
		}
	}
	return ProgramManager{}, false, problem
}
