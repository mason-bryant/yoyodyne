package config

// The declarative trial's opt-in, held to being one.
//
// It is the whole of what a project writes to enter the trial, so the key has to
// reach the effective configuration from the file — a setting the harness reads
// and the document cannot carry is an opt-in nobody can take — and it has to be
// off everywhere nobody wrote it, the bundle included. An opt-in that arrived by
// extending a bundle or by upgrading the executable would not be one.

import "testing"

func TestTheDeclarativeTrialIsOffUntilAProjectWritesTheKey(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Execution.DeclarativeDelivery {
		t.Fatalf("a project that extends the bundle and says nothing is already in the trial")
	}
	if origin := inherited.Origins["execution.declarative_delivery"]; origin != "" {
		t.Errorf("origin = %q, want none; off is the absence of the key rather than a value some layer supplied", origin)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if generated.Execution.DeclarativeDelivery {
		t.Errorf("the configuration yoyo init writes is already in the trial")
	}
}

func TestTheDeclarativeTrialIsTakenByWritingItInTheFile(t *testing.T) {
	t.Parallel()

	opted := loadProject(t, minimalProjectConfig+"execution:\n  declarative_delivery: true\n", nil)
	if !opted.Config.Execution.DeclarativeDelivery {
		t.Fatalf("the project wrote declarative_delivery: true and the effective configuration reads false")
	}
	if origin := opted.Origins["execution.declarative_delivery"]; origin == "" || origin == OriginDefault {
		t.Errorf("origin = %q, want the file that wrote it", origin)
	}

	// Written back out and read again, because turning the trial off is deleting
	// the key or saying false, and a `false` that would not round-trip is a
	// project that cannot leave.
	off := loadProject(t, minimalProjectConfig+"execution:\n  declarative_delivery: false\n", nil)
	if off.Config.Execution.DeclarativeDelivery {
		t.Errorf("declarative_delivery: false left the trial on")
	}
}
