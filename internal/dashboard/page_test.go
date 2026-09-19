package dashboard

// These tests are the evidence for the page: its five sections, each with an
// empty, a loading, and an error state beside its ready one, rendered from the
// read model and from nothing else.
//
// The page is drawn by its own script, which a Go test cannot run. So the
// script is run under Node, against the fixtures under testdata/fixtures, by
// testdata/render.js, and what it leaves in the document is held to the renders
// under testdata/renders — one page per scenario, which a reviewer opens in a
// browser beside the stylesheet or reads as text. Where Node is not installed
// the render test skips and says so; the fixture-shape and route tests below
// hold without it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var updateRenders = flag.Bool("update-renders", false, "rewrite the rendered pages under testdata/renders from the fixtures")

// sections are the five the design names, by the id each carries in the shell.
var sections = []string{"band", "live", "pipeline", "throughput", "capacity"}

// sectionStates are the states every section has, each a child the panel
// shows when its data-state names it.
var sectionStates = []string{"loading", "error", "empty", "ready"}

// fixture reads one fixture as the JSON the server would have sent.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// strict decodes JSON refusing any field the Go type does not carry, which is
// what holds a hand-written fixture to the read model's actual shape.
func strict(t *testing.T, name string, body []byte, into any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("fixture %s is not the read model's shape: %v", name, err)
	}
}

// The shell carries the five sections, and each of them carries its four
// states with the lines the script fills, so a section the script has not
// reached yet says it is reading rather than being blank.
func TestTheShellCarriesFiveSectionsEachWithItsStates(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, body := w.get("/", nil)
	for _, id := range sections {
		if !strings.Contains(body, `<section id="`+id+`" class="panel `+id+`" data-state="loading"`) {
			t.Fatalf("the shell lacks section %q opening in its loading state:\n%s", id, body)
		}
		for _, state := range sectionStates {
			if !strings.Contains(body, `class="section-`+state+`"`) {
				t.Fatalf("the shell lacks a %s state:\n%s", state, body)
			}
		}
		for _, line := range []string{"-problem", "-remedy", "-empty"} {
			if !strings.Contains(body, `id="`+id+line+`"`) {
				t.Fatalf("section %q lacks its %s line:\n%s", id, line, body)
			}
		}
	}
	if strings.Count(body, `class="panel `) != len(sections) {
		t.Fatalf("the shell carries %d panels, not %d:\n%s", strings.Count(body, `class="panel `), len(sections), body)
	}
}

// Every fixture the page is rendered from is the read model's own shape: a
// field the model does not carry is refused, so the renders cannot drift from
// what the server actually sends.
func TestTheFixturesAreTheReadModelsShape(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	standings, throughputs := 0, 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		body := fixture(t, name)
		switch {
		case strings.HasPrefix(name, "standing-"):
			var standing readmodel.Standing
			strict(t, name, body, &standing)
			// A line that could be read is a list, empty rather than absent; a line
			// that could not is absent, as the server sends it, so the page's
			// handling of both is what the renders exercise.
			if standing.ObservedAt.IsZero() || (standing.RunningProblem == "") != (standing.Running != nil) || (standing.NotStartableProblem == "") != (standing.NotStartable != nil) {
				t.Fatalf("fixture %s is not a standing as the server sends one: %+v", name, standing)
			}
			standings++
		case strings.HasPrefix(name, "throughput-"):
			var throughput readmodel.Throughput
			strict(t, name, body, &throughput)
			if len(throughput.Windows) != 2 || throughput.Windows[0].Label != "today" || throughput.Windows[1].Label != "last 7 days" {
				t.Fatalf("fixture %s does not carry the two windows: %+v", name, throughput)
			}
			throughputs++
		default:
			t.Fatalf("fixture %s is neither a standing nor a throughput", name)
		}
	}
	if standings < 4 || throughputs < 3 {
		t.Fatalf("expected the standing and throughput fixtures, found %d and %d", standings, throughputs)
	}
}

// The throughput is served as JSON to the token and to nobody else, refused
// whole when the state cannot be read, and carries what the read model said
// about each window — including which source it could not read.
func TestServesTheThroughputToTheTokenAlone(t *testing.T) {
	t.Parallel()
	var throughput readmodel.Throughput
	strict(t, "throughput-degraded", fixture(t, "throughput-degraded"), &throughput)
	w := serve(t, stubReader{standing: standingWith("title"), throughput: throughput})

	response, body := w.get("/api/throughput", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("throughput with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"label":"today"`, `"label":"last 7 days"`, `"landed":22`, `"spend_problem":"the spend could not be read: open streams: permission denied"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("throughput lacks %s: %s", expected, body)
		}
	}
	if strings.Contains(body, "runs_problem") {
		t.Fatalf("a source that was read is reported as a problem: %s", body)
	}
	if response, body := w.get("/api/throughput", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "landed") {
		t.Fatalf("throughput without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/throughput", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("throughput to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/throughput", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("throughput POST: %d", response.StatusCode)
	}

	broken := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})
	response, body = broken.get("/api/throughput", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "windows") {
		t.Fatalf("throughput over unreadable state: %d %s", response.StatusCode, body)
	}
}

// The vocabularies the page reads are the model's own, held at compile time
// where the page's evidence is read: a refusal's kind is the queue's HoldKind
// and not a type of this package's or the model's, a run's stage is the
// model's Stage, and the ledger the throughput is priced from is the stream
// store `yoyo status --spend` prices — the same type, satisfying the same
// interface, so the page cannot be handed a second pricing.
var (
	_ backlog.HoldKind = readmodel.Refused{}.Kind
	_ readmodel.Stage  = readmodel.RunningRun{}.Stage
	_ readmodel.Ledger = (*runstate.StreamStore)(nil)
	_ readmodel.Runs   = (*runstate.Store)(nil)
)

// The pipeline reads the model's own figures: the startable count and each
// run's stage arrive on the standing, and the script keeps no list of phases
// and makes no subtraction of one line from another to get either.
func TestThePipelineReadsTheModelsCountAndFold(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	for _, expected := range []string{"standing.startable", "run.stage === name", `item.kind === "stalled"`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("the script does not read %q from the model:\n%s", expected, script)
		}
	}
	for _, forbidden := range []string{`"checking"`, `"cleaning_up"`, `"completing"`, "standing.admitted -"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script derives what the model provides, through %q:\n%s", forbidden, script)
		}
	}
}

// The page's script draws every section in every state from the fixtures, and
// what it draws is what the renders under testdata/renders hold. Each of the
// five sections reaches each of its four states in at least one scenario, the
// page reaches its own four, and no scenario sets a style or sends the token
// anywhere but as a bearer to this origin — render.js refuses both.
func TestThePageRendersEverySectionInEveryState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed, so the page's script cannot be run here; the renders under testdata/renders are the last run's evidence")
	}
	out := t.TempDir()
	if *updateRenders {
		out = filepath.Join("testdata", "renders")
	}
	command := exec.CommandContext(context.Background(), node, filepath.Join("testdata", "render.js"), "--out", out)
	// The renders carry clock times, so they are rendered in one timezone
	// whatever machine renders them.
	command.Env = append(os.Environ(), "TZ=UTC")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render.js: %v\n%s", err, output)
	}

	var matrix map[string]struct {
		Page     string            `json:"page"`
		Sections map[string]string `json:"sections"`
	}
	rendered, err := os.ReadFile(filepath.Join(out, "matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rendered, &matrix); err != nil {
		t.Fatalf("matrix.json: %v\n%s", err, rendered)
	}
	reached := map[string]map[string]bool{}
	pageStates := map[string]bool{}
	for scenario, result := range matrix {
		pageStates[result.Page] = true
		for _, id := range sections {
			state, present := result.Sections[id]
			if !present {
				t.Fatalf("scenario %s says nothing about section %s", scenario, id)
			}
			if reached[id] == nil {
				reached[id] = map[string]bool{}
			}
			reached[id][state] = true
		}
	}
	for _, id := range sections {
		for _, state := range sectionStates {
			if !reached[id][state] {
				t.Errorf("no scenario renders section %s in its %s state", id, state)
			}
		}
	}
	for _, state := range []string{"signin", "loading", "error", "ready"} {
		if !pageStates[state] {
			t.Errorf("no scenario leaves the page in its %s state", state)
		}
	}

	// The words, not just the states: what the fixtures say has to land on the
	// page as text, and a zero must never stand in for a line that could not be
	// read.
	page := func(scenario string) string {
		body, err := os.ReadFile(filepath.Join(out, scenario+".html"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for scenario, expectations := range map[string][]string{
		"busy": {
			"Dashboard 3 of 3: the page, five sections, with its empty, loading, and error states",
			"cost unknown (its event log is gone)",
			"approved, resuming integration",
			"claude-code · claude-opus-5 · account pool-b",
			"held for a person: 1 awaiting a decision, 1 awaiting carry-out (most)",
			"22 runs reached the target branch",
			"at least $1,232.58 from 452 invocations",
			"1 exchange record could not be read, so the cost is a floor",
			`<span class="held-state">capacity-blocked</span>`,
			"What to do: nothing needs doing",
			"no reset named, and nothing probes: the run stopped",
		},
		"quiet":            {"The harness is idle", "Nothing is running, and no conversation has a turn in flight.", "The backlog is empty", "Nothing ran and nothing was spent in the last 7 days", "No run or conversation is waiting on provider capacity"},
		"degraded":         {`<span class="figure">—</span>`, `<li class="stage stage-unreadable">`, "Could not be read: the admitted work could not be read", `<span class="pile-label">developing</span>`},
		"unreadable":       {"Could not be read: the recorded runs could not be read: open runs: input/output error; the spend could not be read", "yoyo doctor says whether bd answers in this checkout"},
		"held":             {`<p id="banner" class="banner" role="status">Every role is paused`, "Every role is held: 5 agents on opus, and none names an alternate", "pullable, and nothing is choosing", "the harness is choosing nothing: Paused on the provider's usage window until 18:50Z"},
		"stale":            {`class="freshness freshness-stale">stale<`, "so this is the reading from 14:05:09"},
		"throughput-stale": {`class="freshness freshness-stale">stale<`, "The last reading failed for the throughput", `<p id="throughput-stale" class="stale" role="status">The last reading failed`},
		"refused":          {"permission denied", "yoyo doctor says what cannot be read"},
		"wrong-token":      {"that is not the token this dashboard printed when it started"},
	} {
		body := page(scenario)
		for _, expected := range expectations {
			if !strings.Contains(body, expected) {
				t.Errorf("the %s render lacks %q", scenario, expected)
			}
		}
	}
	if strings.Contains(page("degraded"), `<span class="figure">0</span>`) {
		t.Errorf("the degraded render counts an unreadable line as zero")
	}
	if strings.Contains(page("held"), "the harness pulls next") {
		t.Errorf("the held render offers items the harness pulls next under a banner saying it is choosing nothing")
	}
	// A run the harness stopped is never told it asks again.
	for _, scenario := range []string{"busy", "held"} {
		for _, entry := range strings.Split(page(scenario), `<li class="held `)[1:] {
			if strings.HasPrefix(entry, "held-capacity-blocked") && strings.Contains(entry, "it asks again at the probe interval") {
				t.Errorf("the %s render tells a capacity-blocked run it asks again", scenario)
			}
		}
	}
	// Every render is a document a browser opens as the page: the doctype, the
	// html element, and nothing of the driver's own.
	for scenario := range matrix {
		if body := page(scenario); !strings.HasPrefix(body, "<!doctype html>\n<html lang=\"en\">") || strings.Contains(body, "#document") {
			t.Errorf("the %s render is not a plain document:\n%.200s", scenario, body)
		}
	}

	if *updateRenders {
		return
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "renders"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		expected, err := os.ReadFile(filepath.Join("testdata", "renders", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(out, entry.Name()))
		if err != nil {
			t.Fatalf("render.js did not write %s, which testdata/renders holds: %v", entry.Name(), err)
		}
		if !bytes.Equal(expected, actual) {
			t.Errorf("%s differs from the render under testdata/renders; run `go test ./internal/dashboard -run TestThePageRendersEverySectionInEveryState -update-renders` and review the change", entry.Name())
		}
	}
	for scenario := range matrix {
		if _, err := os.Stat(filepath.Join("testdata", "renders", scenario+".html")); err != nil {
			t.Errorf("scenario %s has no render under testdata/renders; run with -update-renders", scenario)
		}
	}
}
