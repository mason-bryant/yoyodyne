package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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
		}
	}
}
