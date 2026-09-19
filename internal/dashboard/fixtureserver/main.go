// Command fixtureserver serves the dashboard page over the fixtures under
// internal/dashboard/testdata/fixtures, one dashboard per scenario, so the
// page can be looked at in a browser in every state without a harness behind
// it. It is what a rendered capture of each section in each state is taken
// from, and it is deliberately not part of `yoyo`: it reads fixtures rather than
// a state root, and nothing about it is a surface.
//
//	go run ./internal/dashboard/fixtureserver
//
// It prints one line per scenario — the scenario, the URL, and the token that
// page asks for — and serves until interrupted. Each scenario is a dashboard of
// its own on a port of its own, bound to loopback with the same server and the
// same policy `yoyo dashboard` uses, so what the browser shows is what the
// product shows and the policy is exercised on the way.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// scenario is one dashboard's worth of answers: which fixture each reading is
// served from, or that the reading never answers, or that it is refused.
type scenario struct {
	standing   string
	throughput string
	// pending is a dashboard whose readings never answer, which is what the
	// loading states are looked at under.
	pending bool
	// refused is a dashboard whose state cannot be read at all.
	refused string
}

// scenarios are the same pairings testdata/render.js renders, so a capture and
// a render of one name show one state.
var scenarios = map[string]scenario{
	"quiet":      {standing: "standing-quiet", throughput: "throughput-quiet"},
	"busy":       {standing: "standing-busy", throughput: "throughput-busy"},
	"held":       {standing: "standing-held", throughput: "throughput-busy"},
	"degraded":   {standing: "standing-degraded", throughput: "throughput-degraded"},
	"unreadable": {standing: "standing-unreadable", throughput: "throughput-unreadable"},
	"loading":    {pending: true},
	"refused":    {refused: "the state root could not be resolved: open /Users/somebody/Library/Application Support/Yoyodyne/state: permission denied"},
}

// fixtureReader is a read model that answers from fixtures.
type fixtureReader struct {
	standing   readmodel.Standing
	throughput readmodel.Throughput
	pending    bool
	refused    error
}

func (r fixtureReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Standing{}, ctx.Err()
	}
	return r.standing, r.refused
}

func (r fixtureReader) Throughput(ctx context.Context) (readmodel.Throughput, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Throughput{}, ctx.Err()
	}
	return r.throughput, r.refused
}

func load(dir, name string, into any) error {
	body, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func reader(dir string, s scenario) (dashboard.Reader, error) {
	r := fixtureReader{pending: s.pending}
	if s.refused != "" {
		r.refused = errors.New(s.refused)
	}
	if s.standing != "" {
		if err := load(dir, s.standing, &r.standing); err != nil {
			return nil, err
		}
	}
	if s.throughput != "" {
		if err := load(dir, s.throughput, &r.throughput); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func main() {
	dir := filepath.Join("internal", "dashboard", "testdata", "fixtures")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	stopped := make(chan error, len(names))
	for _, name := range names {
		model, err := reader(dir, scenarios[name])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
		server, err := dashboard.New("yoyodyne", model)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		url, err := server.Listen(0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%-11s %s  token %s\n", name, url, server.Token())
		go func() { stopped <- server.Serve(ctx) }()
	}
	fmt.Println("serving every scenario until interrupted; paste each token into its page")
	<-ctx.Done()
	for range names {
		if err := <-stopped; err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}
