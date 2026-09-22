package dashboard

// Node is a development dependency of the dashboard, and where that is said.
//
// The page is drawn by its own script, which a Go test cannot run, so the
// render test in page_test.go runs it under Node and holds what it draws to the
// renders under testdata/renders. That makes Node something `make test` needs
// in any checkout that carries the dashboard's source — this repository — and
// nothing a product that merely serves the dashboard needs: the script runs in
// the operator's browser there, and the binary carries it compiled in.
//
// Three things read the names below so that they agree by construction: the
// render test, which fails naming Node when it is absent and skips only where
// the variable says that is deliberate; `yoyo doctor`, which reports Node's
// presence for a product whose repository carries the script; and
// docs/developing-yoyo.md, which names the dependency.

// NodeUnavailableVariable, set to anything but the empty string, says that
// Node is deliberately not installed where the checks run, and that the render
// test is to skip rather than fail there. It is under the harness's own prefix
// so the explicit environment every check and every provider invocation is
// built from carries it through: a sandbox that has no Node declares so once,
// where the harness reads its environment, and the run's own execution of the
// checks and the harness's afterwards read the same declaration.
//
// Without it a missing Node fails the test. A skip is silent in a green run,
// and the render test is the page's only behavioural evidence, so the default
// is to say so loudly and let the machine that means it declare otherwise.
const NodeUnavailableVariable = "YOYODYNE_NODE_UNAVAILABLE"

// RenderScript is the script the render test runs under Node, relative to the
// repository root. A repository that carries it is one whose checks render the
// page, which is what `yoyo doctor` looks for before it asks about Node at all.
const RenderScript = "internal/dashboard/testdata/render.js"

// RenderTest names the test that runs the script, so a finding about Node can
// say which check it is that fails without it.
const RenderTest = "TestThePageRendersEverySectionInEveryState"
