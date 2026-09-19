package cli

// Serving the read model to a browser on this machine.
//
// `yoyo dashboard` is the standalone command the observability-and-dashboard
// design names as the V1 shape: a process of its own that reads the same
// durable records `yoyo status` reads and projects them at a loopback port, for
// as long as it is left running. It is a projection and nothing else — it owns
// no state, offers no write, and restarting it changes nothing about the
// harness — so it is started and stopped freely, and a later supervisor can
// own its lifecycle without a redesign.
//
// What it prints when it starts is the whole of what an operator needs and the
// one thing that is printed once: the URL, and beside it the token every request
// for the read model has to carry. The token is never put in the URL, where it
// would reach a browser history and every log a proxy keeps, and never in a
// cookie, which on 127.0.0.1 is sent to every port of 127.0.0.1; the page asks
// for it and keeps it in the tab's session storage, scoped to this port.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func serveDashboard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	port := flags.Int("port", 0, "the loopback port to serve on (default: one the operating system chooses)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "dashboard does not accept positional arguments")
		printDashboardUsage(stderr)
		return 2
	}

	// The records are opened once before anything is bound, so a configuration
	// that does not resolve refuses at the terminal rather than at the first
	// request. They are opened again on every request after that, because a
	// dashboard left running for a week must not go on serving a state root
	// that has since stopped being readable.
	reader := dashboardReader{configPath: *configPath}
	if err := reader.ready(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server, err := dashboard.New(string(resolved.Config.Product.ID), reader)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	url, err := server.Listen(*port)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintf(stdout, "dashboard for %s serving at %s\n", resolved.Config.Product.ID, url)
	fmt.Fprintf(stdout, "token: %s\n", server.Token())
	fmt.Fprintln(stdout, "the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing and /api/throughput")
	fmt.Fprintln(stdout, "it is printed here and nowhere else, and a restarted dashboard prints a new one; stop with ctrl-c")

	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "dashboard stopped")
	return 0
}

// dashboardReader is the read model as the dashboard is handed it, over the
// same records and the same wiring `yoyo status` reads: the four lines are one
// derivation, and a dashboard that assembled its own would be the second
// surface the read model exists to prevent.
type dashboardReader struct {
	configPath string
}

// ready opens what the reading needs and closes nothing else: the configuration,
// the state root, and the run store that every other record sits beside. A
// failure here is the state being unreadable, which the verb refuses to start
// on and the read model is refused on afterwards.
func (r dashboardReader) ready() error {
	resolved, err := loadConfiguration(r.configPath)
	if err != nil {
		return err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return err
	}
	if _, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID); err != nil {
		return err
	}
	return nil
}

// Standing is the four lines and everything the model carries beside them. What
// a source that could not be read costs is said inside the answer, line by
// line, exactly as the terminal says it; what refuses the whole answer is the
// state being unreadable at all.
func (r dashboardReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	if err := r.ready(); err != nil {
		return readmodel.Standing{}, err
	}
	return readmodel.ReadStanding(ctx, standingSources(r.configPath)), nil
}

// The two readers the throughput is handed are the terminal's own stores, held
// here to the model's interfaces so the substitution of a second pricing would
// not compile: *runstate.StreamStore is what reportSpend prices `yoyo status
// --spend` from, through its Spend method, and *runstate.Store is what
// `yoyo status` reads each run's Outcome from.
var (
	_ readmodel.Ledger = (*runstate.StreamStore)(nil)
	_ readmodel.Runs   = (*runstate.Store)(nil)
)

// Throughput is what landed and what it cost over the model's two windows.
// readmodel.ReadThroughput derives nothing of its own about money or endings:
// it calls (*runstate.StreamStore).Spend once, over the widest window, and
// splits the report's rows by the local day each carries — the derivation
// `yoyo status --spend 7` prints — and it classifies each run by
// runstate.State.Outcome, the word `yoyo status` prints for it. So a figure on
// the page is a figure the terminal prints, and the two cannot disagree about
// what today cost.
func (r dashboardReader) Throughput(ctx context.Context) (readmodel.Throughput, error) {
	if err := r.ready(); err != nil {
		return readmodel.Throughput{}, err
	}
	resolved, err := loadConfiguration(r.configPath)
	if err != nil {
		return readmodel.Throughput{}, err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return readmodel.Throughput{}, err
	}
	return readmodel.ReadThroughput(ctx, throughputSources(stateRoot, resolved.Config.Product.ID)), nil
}

// throughputSources opens the two stores the throughput is read from. Either
// failing to open costs its half of the reading and not the other, and the
// reason travels with the gap: the reading says "could not be opened: <why>"
// under runs_problem or spend_problem, which is what the page's error state
// shows, rather than that nothing was wired.
func throughputSources(stateRoot string, productID domain.ProductID) readmodel.ThroughputSources {
	sources := readmodel.ThroughputSources{}
	if store, err := runstate.NewStore(stateRoot, productID); err != nil {
		sources.RunsProblem = err.Error()
	} else {
		sources.Runs = store
	}
	if store, err := runstate.NewStreamStore(stateRoot, productID); err != nil {
		sources.LedgerProblem = err.Error()
	} else {
		sources.Ledger = store
	}
	return sources
}

func printDashboardUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo dashboard [options]

Serves the read model -- the same four lines and capacity state `+"`yoyo status`"+`
reads, and what landed and what it cost -- to a browser on this machine, at a
loopback port, until stopped, as a page of five sections: the status band, the
runs and conversations in flight, where admitted work stands in the pipeline,
throughput and cost, and provider capacity. It prints the URL and, once, the
token every request for the read model has to carry as
`+"`Authorization: Bearer <token>`"+`: the page asks for it and keeps it in the tab's
session storage, scoped to this port, and never in a URL or a cookie. It serves
the read model as JSON behind the token -- the four lines and the capacity state
at /api/standing, and what landed and what it cost over today and the last seven
days at /api/throughput; the page shell at / and its own script and style are
static text with nothing of the read model in them, served to the browser before
it has a token. Everything else is refused: a request for the read model with no
token or the wrong one, a Host or Origin that is not the address it bound, and
durable state it cannot read each get a refusal and never part of an answer.

It is a projection. It owns no state, offers no write, and restarting it changes
nothing about the harness.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --port <n>        the loopback port to serve on (default: one the operating system chooses)`)
}
