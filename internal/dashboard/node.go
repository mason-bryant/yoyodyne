package dashboard

// The page is drawn by its own script, and a Go test cannot run one. What runs
// it is Node, so Node is a development dependency of this product rather than a
// convenience: without it the page's only behavioural evidence -- the renders
// under testdata/renders, compared against what the script actually draws -- is
// never produced, and a suite that passes says nothing about the page.
//
// Three things have to agree about that: the test that needs Node, the doctor
// that asks whether this machine has it, and the prose that tells a developer to
// install it. They agree by naming the same constants rather than by three
// copies of one string, because the copy that drifts is the one that makes a
// green run mean something different from what it says.

const (
	// NodeProgram is what the render test runs the page's script under, and what
	// the doctor looks for on the PATH.
	NodeProgram = "node"

	// NodeUnavailableVariable is how an environment that deliberately has no Node
	// says so, and it is the one thing that turns the render test's absence of
	// Node from a failure into a skip. Nothing in the harness sets it: it is set
	// by whoever built an environment without Node -- a container, a sandbox, a
	// machine kept deliberately bare -- so that the absence is a decision
	// somebody made rather than a tool somebody never installed, which is the
	// case this whole arrangement exists to stop passing quietly. The value is a
	// sentence saying which environment set it, and both the skip and the doctor
	// quote it, so a declared absence names who declared it.
	NodeUnavailableVariable = "YOYODYNE_NODE_UNAVAILABLE"

	// RenderScript is the script that drives the page under Node, from the
	// repository root. A repository carrying it is a product that ships the
	// dashboard, which is what makes Node's absence worth reporting at all --
	// every other product is asked nothing about it.
	RenderScript = "internal/dashboard/testdata/render.js"

	// NodeDocumentation is where Node is written down as a development
	// dependency, named by the failure so that a developer meeting it has
	// somewhere to go.
	NodeDocumentation = "docs/developing-yoyo.md"
)
