package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Passes are summed by task and by the model each ran on, inside the window, and
// a pass that took no turn and cost nothing is not a row.
func TestSweepSpendGroupsByTaskAndModelInsideTheWindow(t *testing.T) {
	t.Parallel()

	today := time.Date(2026, 9, 20, 12, 0, 0, 0, time.Local)
	pass := func(task, model string, at time.Time, turns int, cost float64) Sweep {
		return Sweep{Task: task, Role: domain.RoleDevelopmentManager, Model: model, StartedAt: at, Turns: turns, CostUSD: cost}
	}
	rows := SweepSpend([]Sweep{
		pass("dm-sweep", "sonnet", today, 2, 0.5),
		pass("dm-sweep", "sonnet", today.Add(-time.Hour), 1, 0.25),
		pass("dm-sweep", "fable", today, 1, 1.5),
		pass("dm-sweep", "sonnet", today.AddDate(0, 0, -10), 1, 9),
		pass("dm-sweep", "", today, 0, 0),
		pass("pm-sweep", "", today, 1, 2),
	}, LocalDay(today.AddDate(0, 0, -6)))

	want := []SweepModelSpend{
		{Task: "dm-sweep", Role: domain.RoleDevelopmentManager, Model: "fable", Passes: 1, Turns: 1, CostUSD: 1.5},
		{Task: "dm-sweep", Role: domain.RoleDevelopmentManager, Model: "sonnet", Passes: 2, Turns: 3, CostUSD: 0.75},
		{Task: "pm-sweep", Role: domain.RoleDevelopmentManager, Model: "", Passes: 1, Turns: 1, CostUSD: 2},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if all := SweepSpend([]Sweep{pass("dm-sweep", "sonnet", today.AddDate(0, 0, -10), 1, 9)}, ""); len(all) != 1 {
		t.Errorf("no window: rows = %+v, want the old pass counted", all)
	}
}
