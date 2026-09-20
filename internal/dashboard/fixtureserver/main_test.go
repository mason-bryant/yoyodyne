package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// Every scenario loads from the fixtures and answers the way its name says: a
// fixture-backed one with the fixture, the pending one never, the refused one
// with its refusal. A scenario that named a fixture nobody wrote would fail at
// the terminal, which is later than a test.
func TestEveryScenarioLoadsAndAnswersAsNamed(t *testing.T) {
	dir := filepath.Join("..", "testdata", "fixtures")
	for name, s := range scenarios {
		model, err := reader(dir, s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		standing, err := model.Standing(ctx)
		cancel()
		switch {
		case s.pending:
			if err == nil {
				t.Fatalf("%s answered a standing when it should never answer", name)
			}
		case s.refused != "":
			if err == nil || err.Error() != s.refused {
				t.Fatalf("%s refused with %v, not %q", name, err, s.refused)
			}
		default:
			if err != nil || standing.ObservedAt.IsZero() {
				t.Fatalf("%s: standing %+v, %v", name, standing, err)
			}
			throughput, err := model.Throughput(context.Background())
			if err != nil || len(throughput.Windows) != 2 {
				t.Fatalf("%s: throughput %+v, %v", name, throughput, err)
			}
			// Every item the standing names has a card behind it, so a page on
			// this scenario can open any of them, and an id nobody wrote a fixture
			// for is the tracker holding nothing.
			for _, run := range standing.Running {
				if item, err := model.WorkItem(context.Background(), run.WorkItemID); err != nil || item.ID != run.WorkItemID {
					t.Fatalf("%s: running item %s has no fixture card: %+v, %v", name, run.WorkItemID, item, err)
				}
			}
			for _, refused := range standing.NotStartable {
				if item, err := model.WorkItem(context.Background(), refused.WorkItemID); err != nil || item.ID != refused.WorkItemID {
					t.Fatalf("%s: refused item %s has no fixture card: %+v, %v", name, refused.WorkItemID, item, err)
				}
			}
			for _, startable := range standing.StartableItems {
				if item, err := model.WorkItem(context.Background(), startable.WorkItemID); err != nil || item.ID != startable.WorkItemID {
					t.Fatalf("%s: startable item %s has no fixture card: %+v, %v", name, startable.WorkItemID, item, err)
				}
			}
			if _, err := model.WorkItem(context.Background(), "nobody-wrote-this"); !errors.Is(err, readmodel.ErrNoSuchWorkItem) {
				t.Fatalf("%s: an id with no fixture = %v, want ErrNoSuchWorkItem", name, err)
			}
		}
	}
}
