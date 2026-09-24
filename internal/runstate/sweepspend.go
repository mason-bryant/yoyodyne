package runstate

import (
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SweepModelSpend is what one recurring task's passes cost on one model.
//
// A pass's turns are turns of the role's own conversation, so the event stream
// prices them among the conversation's other turns and cannot say which were a
// schedule's. The sweep record can: it names the task, what the pass cost, and
// the model it ran on. This is that record read for spend, so what a cadence
// costs, and on which model, is a figure rather than an inference.
type SweepModelSpend struct {
	Task string           `json:"task"`
	Role domain.AgentRole `json:"role"`
	// Model is the model the passes ran on, and empty for the passes whose record
	// names none — those that took no turn, and every one recorded before passes
	// named their model.
	Model   string  `json:"model,omitempty"`
	Passes  int     `json:"passes"`
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd"`
}

// SweepSpend is what the recorded passes cost, by task and by the model each ran
// on, from the local day oldest on — every pass where oldest is empty. It is
// read by the day a pass started on, which is the rule a spend report's window
// applies to everything else it prices. A pass that took no turn and cost
// nothing spent nothing and is left out.
//
// The rows are ordered by task and then by model, so two reports over different
// windows put the same task on the same line.
func SweepSpend(sweeps []Sweep, oldest string) []SweepModelSpend {
	type key struct {
		task  string
		model string
	}
	grouped := map[key]*SweepModelSpend{}
	for _, pass := range sweeps {
		if pass.Turns == 0 && pass.CostUSD == 0 {
			continue
		}
		if oldest != "" && !pass.StartedAt.IsZero() && LocalDay(pass.StartedAt) < oldest {
			continue
		}
		at := key{task: pass.Task, model: strings.TrimSpace(pass.Model)}
		row, seen := grouped[at]
		if !seen {
			row = &SweepModelSpend{Task: at.task, Role: pass.Role, Model: at.model}
			grouped[at] = row
		}
		row.Passes++
		row.Turns += pass.Turns
		row.CostUSD += pass.CostUSD
	}
	rows := make([]SweepModelSpend, 0, len(grouped))
	for _, row := range grouped {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Task != rows[j].Task {
			return rows[i].Task < rows[j].Task
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}
