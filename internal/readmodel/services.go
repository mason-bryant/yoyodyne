package readmodel

// The parts of the product, as the supervisor last saw them.
//
// The supervisor starts every enabled part and keeps it running, and a part
// that keeps failing is left down and reported as degraded — through the
// standing surfaces, the design says, rather than through a restart loop. This
// is where that reaches them: the supervisor's own record, read beside its
// lease, carried whole for the surfaces that read the model, and said on the
// attention line for each child the supervisor has given up on, because a
// part of the product that is down and is not coming back is waiting on a
// person whatever else the machine is doing.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Supervision is the product's supervisor as the read model asks about it:
// whether one is running, and what it last recorded about the children. It is
// satisfied by *runstate.SupervisionStore.
type Supervision interface {
	Running() (bool, error)
	Load() (runstate.Supervision, bool, error)
}

// Services is the product's parts and its supervisor, as one reading.
type Services struct {
	// SupervisorRunning is the lease's answer, now, rather than anything the
	// record says about itself.
	SupervisorRunning bool `json:"supervisor_running"`
	// Recorded reports whether a supervisor has ever written the record; the
	// record below is meaningful only where it has.
	Recorded bool `json:"recorded"`
	// Record is what the supervisor last knew, in the supervisor's own type so
	// every surface reads one vocabulary of child states.
	Record runstate.Supervision `json:"record"`
}

// readServices reads the supervisor's lease and record. A reading with no
// supervision wired says nothing rather than reporting every part off, for
// the reason every other optional source here does.
func readServices(sources Sources) (*Services, string) {
	if sources.Supervision == nil {
		return nil, ""
	}
	services := &Services{}
	var problem string
	running, err := sources.Supervision.Running()
	if err != nil {
		problem = joinProblems(problem, fmt.Sprintf("whether the product's supervisor is running could not be read: %v", err))
	}
	services.SupervisorRunning = running
	recorded, found, err := sources.Supervision.Load()
	if err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the supervisor's record of the product's parts could not be read: %v", err))
		return services, problem
	}
	services.Recorded = found
	services.Record = recorded
	return services, problem
}

// Attention is each child the supervisor has left down, as something waiting
// on a person: the supervisor's own record of it, carried whole, from which
// the line says the reason it was left down and what brings it back.
func (s *Services) Attention() []Attention {
	if s == nil || !s.Recorded {
		return nil
	}
	degraded := s.Record.Degraded()
	attention := make([]Attention, 0, len(degraded))
	for _, child := range degraded {
		attention = append(attention, degradedServiceAttention(child))
	}
	return attention
}
