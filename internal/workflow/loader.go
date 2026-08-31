package workflow

import (
	"fmt"
	"io"
	"os"

	"github.com/mason-bryant/yoyodyne/internal/action"
)

// Loader reads a workflow definition and produces the graph an instance runs, or
// refuses it whole.
//
// It is the strict door, and strict means two things. A definition that is not
// exactly what this build can run is refused rather than partially adopted:
// nothing comes back but the defect. And it is refused here — before a work item
// is claimed, before a worktree exists, and before an instance is recorded —
// which is the only place a refusal is free. Everything downstream of a load has
// already spent something.
//
// A loader is the three things a definition has to be read against: what code
// registered, what authority the caller holds, and which definition the caller is
// already running. Each is a refusal of its own, and none of them is optional —
// a zero Loader registers nothing and confers nothing, so it refuses everything.
type Loader[S any] struct {
	// Registry is the actions this build registered. A definition is validated
	// against this registry's own catalog and compiled against the registry itself,
	// so what a definition may select and what this build can perform are one list
	// rather than two that drift.
	Registry action.Registry[S]
	// Grant is the authority the compiled workflow may draw on. A definition
	// selecting an action that requires more than this is refused; see Grant.
	Grant Grant
	// Pin, when set, is the digest the caller is already running: a definition that
	// digests to anything else is refused rather than adopted, which is what keeps
	// an instance running the definition it pinned after the file changed
	// underneath it. Empty is an unpinned load, which is where a pin comes from.
	Pin string
}

// LoadFile reads a definition from a file and compiles it, naming the file in
// whatever it refuses. This is the door a project's own workflow file comes
// through.
func (l Loader[S]) LoadFile(path string) (Graph[S], error) {
	file, err := os.Open(path)
	if err != nil {
		return Graph[S]{}, fmt.Errorf("read workflow definition: %w", err)
	}
	defer file.Close()

	graph, err := l.Load(file)
	if err != nil {
		return Graph[S]{}, fmt.Errorf("%s: %w", path, err)
	}
	return graph, nil
}

// Load reads a definition, validates it against what this loader's registry
// registered, and compiles it.
//
// It is the package-level Load — decode strictly, then validate — with the
// catalog taken from the registry rather than supplied, and with the compile
// after it. A caller that already holds a Validated wants Compile.
func (l Loader[S]) Load(reader io.Reader) (Graph[S], error) {
	catalog, err := CatalogFrom(l.Registry)
	if err != nil {
		// The registry is assembled in Go from a literal table, so this is a defect
		// in the build rather than in the definition being read. It is reported as
		// what it is instead of as something wrong with somebody's file.
		return Graph[S]{}, fmt.Errorf("the actions this build registered are not a catalog a definition can be validated against: %w", err)
	}
	validated, err := Load(reader, catalog)
	if err != nil {
		return Graph[S]{}, err
	}
	return l.Compile(validated)
}
