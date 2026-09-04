package config

// The declarative path's default, and the one key that rolls it back.
//
// The default has to reach a project that wrote nothing — a file from before the
// key existed, and the configuration `yoyo init` writes — because the flip is the
// harness changing what a run does rather than something every project has to
// take. The rollback has to reach the effective configuration from the file, and
// to be legible once it has: a rollback the harness reads and nothing can show is
// one an operator cannot confirm they took.

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestTheDeclarativePathIsWhatAProjectWritingNothingGets(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if !inherited.Config.Execution.DeclarativeDelivery {
		t.Fatalf("a project that extends the bundle and says nothing is on the legacy path")
	}
	if origin := inherited.Origins["execution.declarative_delivery"]; origin != OriginDefault {
		t.Errorf("origin = %q, want %q; the default is a value the harness supplies", origin, OriginDefault)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if !generated.Execution.DeclarativeDelivery {
		t.Errorf("the configuration yoyo init writes is on the legacy path")
	}
}

func TestTheLegacyPathIsTakenByWritingTheOneKeyInTheFile(t *testing.T) {
	t.Parallel()

	off := loadProject(t, minimalProjectConfig+"execution:\n  declarative_delivery: false\n", nil)
	if off.Config.Execution.DeclarativeDelivery {
		t.Fatalf("the project wrote declarative_delivery: false and the effective configuration still reads true")
	}
	if origin := off.Origins["execution.declarative_delivery"]; origin == "" || origin == OriginDefault {
		t.Errorf("origin = %q, want the file that rolled back", origin)
	}

	// A project that states the default explicitly is not rolling back, and the
	// origin still names its file: the two are told apart by the value rather
	// than by whether anybody wrote it down.
	on := loadProject(t, minimalProjectConfig+"execution:\n  declarative_delivery: true\n", nil)
	if !on.Config.Execution.DeclarativeDelivery {
		t.Errorf("declarative_delivery: true did not reach the effective configuration")
	}
}

// The rollback has to be visible in what `config show --effective` renders, which
// is this type marshalled. A `false` the encoder drops is a project that rolled
// back and reads exactly like one that did not — and the configuration revision,
// which is the same encoding digested, could not tell them apart either.
func TestARollbackIsLegibleInTheEffectiveConfiguration(t *testing.T) {
	t.Parallel()

	off := loadProject(t, minimalProjectConfig+"execution:\n  declarative_delivery: false\n", nil).Config
	rendered, err := yaml.Marshal(off)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(rendered), "declarative_delivery: false") {
		t.Errorf("the effective configuration of a project that rolled back renders no declarative_delivery: false")
	}
	if on := loadProject(t, minimalProjectConfig, nil).Config; on.Revision() == off.Revision() {
		t.Errorf("a project on the declarative path and one that rolled back share the revision %s", on.Revision())
	}
}
