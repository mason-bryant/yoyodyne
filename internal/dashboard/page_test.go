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

// popups are the two dialogs the page opens over the sections: a grouping of
// the pipeline listed by title, and one work item's card.
var popups = []string{"grouping", "card"}

// sectionStates are the states every section has, each a child the panel
// shows when its data-state names it. A pop-up has the same four, and is
// closed besides.
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
	// The two pop-ups are dialogs, closed to begin with, each with the same four
	// states a section has and a button that closes it.
	for _, id := range popups {
		if !strings.Contains(body, `<div id="`+id+`" class="popup" role="dialog" aria-modal="true" aria-labelledby="`+id+`-heading" data-state="loading" hidden>`) {
			t.Fatalf("the shell lacks the %s pop-up, closed and a dialog:\n%s", id, body)
		}
		for _, line := range []string{"-problem", "-remedy", "-empty", "-close", "-backdrop", "-heading"} {
			if !strings.Contains(body, `id="`+id+line+`"`) {
				t.Fatalf("pop-up %q lacks its %s:\n%s", id, line, body)
			}
		}
	}
	if strings.Count(body, `class="popup"`) != len(popups) {
		t.Fatalf("the shell carries %d pop-ups, not %d:\n%s", strings.Count(body, `class="popup"`), len(popups), body)
	}
	// Closed means not shown: the stylesheet gives a pop-up a display of its
	// own, which on its own would beat the user agent's `[hidden]` rule and
	// show both dialogs over the page on load, so it holds the attribute off
	// itself — the render driver prunes by attribute and cannot see this.
	_, stylesheet := w.get("/assets/dashboard.css", nil)
	for _, rule := range []string{"[hidden] {\n  display: none !important;\n}", ".popup[hidden] {\n  display: none;\n}"} {
		if !strings.Contains(stylesheet, rule) {
			t.Fatalf("the stylesheet lacks %q, so a closed pop-up would be shown:\n%s", rule, stylesheet)
		}
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
	standings, throughputs, items := 0, 0, 0
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
			// handling of both is what the renders exercise. The item lists follow
			// the queue the same way, and name what the counts count.
			if standing.ObservedAt.IsZero() || (standing.RunningProblem == "") != (standing.Running != nil) || (standing.NotStartableProblem == "") != (standing.NotStartable != nil) {
				t.Fatalf("fixture %s is not a standing as the server sends one: %+v", name, standing)
			}
			if (standing.NotStartableProblem == "") != (standing.AdmittedItems != nil) || (standing.NotStartableProblem == "") != (standing.StartableItems != nil) ||
				(standing.AdmittedItems != nil && len(standing.AdmittedItems) != standing.Admitted) || (standing.StartableItems != nil && len(standing.StartableItems) != standing.Startable) {
				t.Fatalf("fixture %s names items the counts do not count: admitted %d over %d, startable %d over %d", name, standing.Admitted, len(standing.AdmittedItems), standing.Startable, len(standing.StartableItems))
			}
			standings++
		case strings.HasPrefix(name, "throughput-"):
			var throughput readmodel.Throughput
			strict(t, name, body, &throughput)
			if len(throughput.Windows) != 2 || throughput.Windows[0].Label != "today" || throughput.Windows[1].Label != "last 7 days" {
				t.Fatalf("fixture %s does not carry the two windows: %+v", name, throughput)
			}
			for _, window := range throughput.Windows {
				if (throughput.RunsProblem == "") != (window.LandedItems != nil) || (window.LandedItems != nil && len(window.LandedItems) != window.Landed) {
					t.Fatalf("fixture %s names %d landed runs in %q against a count of %d", name, len(window.LandedItems), window.Label, window.Landed)
				}
			}
			throughputs++
		case strings.HasPrefix(name, "item-"):
			var item readmodel.WorkItem
			strict(t, name, body, &item)
			// The fixture is the item it is named for, with every field the card
			// labels present — the lists empty rather than absent — and its run
			// either carried or accounted for.
			if item.ID != strings.TrimPrefix(name, "item-") || item.ObservedAt.IsZero() || item.Labels == nil {
				t.Fatalf("fixture %s is not a work item as the server sends one: %+v", name, item)
			}
			items++
		default:
			t.Fatalf("fixture %s is neither a standing, a throughput, nor an item", name)
		}
	}
	if standings < 4 || throughputs < 3 || items < 3 {
		t.Fatalf("expected the standing, throughput, and item fixtures, found %d, %d, and %d", standings, throughputs, items)
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
	for _, expected := range []string{`"label":"today"`, `"label":"last 7 days"`, `"landed":4`, `"landed_items":[{"run_id"`, `"spend_problem":"the spend could not be read: open streams: permission denied"`} {
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
	_ readmodel.Mover  = readmodel.Attention{}.Mover
	_ readmodel.Ledger = (*runstate.StreamStore)(nil)
	_ readmodel.Runs   = (*runstate.Store)(nil)
)

// The movers the page names are the model's vocabulary, every one of them, in
// the model's order, and in the model's words, so the tile that counts what
// waits on a person counts by the value each entry carries rather than by a
// reading of the sentence beside it, says the operator's count first because
// the model puts it first, and names each mover as the terminal does.
func TestTheNeedsAHumanTileCountsByTheModelsMovers(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	var named []readmodel.Mover
	labels := map[readmodel.Mover]string{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, `{ mover: "`) {
			continue
		}
		parts := strings.Split(line, `"`)
		if len(parts) < 5 {
			t.Fatalf("the script's mover line does not carry a token and a label: %q", line)
		}
		named = append(named, readmodel.Mover(parts[1]))
		labels[readmodel.Mover(parts[1])] = parts[3]
	}
	movers := readmodel.Movers()
	if len(named) != len(movers) {
		t.Fatalf("the script names %d movers, and the model's vocabulary holds %d: %v against %v", len(named), len(movers), named, movers)
	}
	for i, mover := range movers {
		if named[i] != mover {
			t.Fatalf("the script's mover %d is %q, and the model's is %q", i, named[i], mover)
		}
		if labels[mover] != mover.Possessive() {
			t.Fatalf("the script names %q as %q, and the model says %q", mover, labels[mover], mover.Possessive())
		}
	}
	if !strings.Contains(script, "entry.mover") {
		t.Fatalf("the script does not read each entry's mover from the model:\n%s", script)
	}
	if strings.Contains(script, "entry.whose") || strings.Contains(script, "entry.what") {
		t.Fatalf("the script reads the mover off the sentence beside it rather than the value the model carries:\n%s", script)
	}
}

// The pipeline reads the model's own figures: the startable count and each
// run's stage arrive on the standing, and the script keeps no list of phases
// and makes no subtraction of one line from another to get either.
func TestThePipelineReadsTheModelsCountAndFold(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	// The groupings list what the model names — the admitted items, the
	// startable ones, and the landed runs — rather than assembling a list from
	// the other lines.
	for _, expected := range []string{"standing.startable", "run.stage === name", `item.kind === "stalled"`, "standing.admitted_items", "standing.startable_items", "period.landed_items", `"/api/items/" + encodeURIComponent(id)`} {
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
// five sections reaches each of its four states in at least one scenario, each
// of the two pop-ups reaches each of its four and is closed in another, the
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
		Popups   map[string]string `json:"popups"`
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
		for _, id := range popups {
			state, present := result.Popups[id]
			if !present {
				t.Fatalf("scenario %s says nothing about the %s pop-up", scenario, id)
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
	for _, id := range popups {
		for _, state := range append([]string{"closed"}, sectionStates...) {
			if !reached[id][state] {
				t.Errorf("no scenario leaves the %s pop-up %s", id, state)
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
			// What waits on a person, counted per mover in the model's order: the
			// operator's is the figure, and each role's and the harness's are
			// beside it, out of the whole the terminal prints.
			`<dt>Needs a human</dt>`,
			`<span class="figure">2</span>`,
			`<span class="unit">things waiting on the operator</span>`,
			`<span class="detail">of 7 things waiting in all; the product manager's: 1, the architect's: 2, the development manager's: 1, the harness's: 1</span>`,
			"Needs a human: 7 things waiting on a person — the operator's: 2, the product manager's: 1, the architect's: 2, the development manager's: 1, the harness's: 1.",
			"22 runs reached the target branch",
			"at least $1,232.58 from 452 invocations",
			"1 exchange record could not be read, so the cost is a floor",
			`<span class="held-state">capacity-blocked</span>`,
			"What to do: nothing needs doing",
			"no reset named, and nothing probes: the run stopped",
		},
		"quiet":      {"The harness is idle", "Nothing is running, and no conversation has a turn in flight.", "The backlog is empty", "Nothing ran and nothing was spent in the last 7 days", "No run or conversation is waiting on provider capacity"},
		"degraded":   {`<span class="figure">—</span>`, `<li class="stage stage-unreadable">`, "Could not be read: the admitted work could not be read", `<button class="grouping-open pile-label" type="button" data-grouping="stage:developing">developing</button>`},
		"unreadable": {"Could not be read: the recorded runs could not be read: open runs: input/output error; the spend could not be read", "yoyo doctor says whether bd answers in this checkout"},
		"held": {
			`<p id="banner" class="banner" role="status">Every role is paused`, "Every role is held: 5 agents on opus, and none names an alternate", "pullable, and nothing is choosing", "the harness is choosing nothing: Paused on the provider's usage window until 18:50Z",
			// One thing waiting, and it is the operator's: the figure says so and
			// there is no breakdown to give.
			`<span class="figure">1</span>`, `<span class="unit">thing waiting on the operator</span>`, "Needs a human: 1 thing waiting on a person — the operator's: 1.",
		},
		"stale":            {`class="freshness freshness-stale">stale<`, "so this is the reading from 14:05:09"},
		"throughput-stale": {`class="freshness freshness-stale">stale<`, "The last reading failed for the throughput", `<p id="throughput-stale" class="stale" role="status">The last reading failed`},
		"refused":          {"permission denied", "yoyo doctor says what cannot be read"},
		"wrong-token":      {"that is not the token this dashboard printed when it started"},
		// The card: every field under a plain label, the run in the terminal's
		// words, and a field the item has nothing in saying so.
		"card": {
			`<h2 id="card-heading" class="popup-title">Dashboard 3 of 3: the page, five sections, with its empty, loading, and error states</h2>`,
			"<dt>Id</dt>", "<dt>Title</dt>", "<dt>Status</dt>", "<dt>Priority</dt>", "<dt>Labels</dt>", "<dt>Parent</dt>", "<dt>Description</dt>", "<dt>Design</dt>", "<dt>Acceptance criteria</dt>", "<dt>Notes</dt>", "<dt>Run</dt>",
			"<dd>in_progress</dd>", "<dd>P1 (0 is the most urgent, 4 the least)</dd>", "<dd>dashboard</dd>", "<dd>yoyodyne-ifd.141</dd>",
			`<p class="card-run-flight">in flight — developing, 12m elapsed, $3.41 so far</p>`,
			"branch: yoyodyne/yoyodyne-ifd-141-3/c07d6849",
			"developer session: 0f2c41ab-7e05-4c3d-9a1b-6e8f0d2a4c71",
		},
		"card-loading": {"Reading the work item…", `<h2 id="card-heading" class="popup-title">yoyodyne-ifd.201</h2>`},
		"card-missing": {`<p id="card-empty" class="empty">No work item is recorded under yoyodyne-ifd.212`},
		"card-refused": {"Could not be read: the work item could not be read: bd show failed", "yoyo status yoyodyne-ifd.230 says the same thing at the terminal"},
		// The grouping: the items behind a figure, by title, each a button that
		// opens its card, with the pipeline's own word for it beside.
		"grouping": {
			`<h2 id="grouping-heading" class="popup-title">Held back (6 items)</h2>`,
			`<button class="item-open grouping-title" type="button" data-item="yoyodyne-ifd.200">Slack threads carry the item's title</button>`,
			`<span class="grouping-detail">waiting on yoyodyne-ifd.199</span>`,
			`data-item="yoyodyne-ifd.219"`,
		},
		"grouping-landed":  {`Landed last 7 days (4 items)`, "from 2026-09-13, local days, newest first", `data-item="yoyodyne-ifd.439"`, "landed 2026-09-19 13:41:00", `data-item="yoyodyne-ifd.435"`},
		"grouping-empty":   {`<h2 id="grouping-heading" class="popup-title">Startable</h2>`, "the harness is choosing nothing: Paused on the provider's usage window until 18:50Z", `<p id="grouping-empty" class="empty">No admitted item is startable.</p>`},
		"grouping-error":   {`<h2 id="grouping-heading" class="popup-title">Admitted</h2>`, "Could not be read: the admitted work could not be read: bd list", "yoyo doctor says whether bd answers in this checkout"},
		"grouping-loading": {`<h2 id="grouping-heading" class="popup-title">Landed today</h2>`, "Pricing the week…"},
		// The card over the grouping it was opened from: a stopped run with its
		// change preserved, said as `yoyo status` says it.
		"grouping-card": {
			`<h2 id="grouping-heading" class="popup-title">Held back: held for a person (2 items)</h2>`,
			"waiting on: the development manager", `data-item="yoyodyne-ifd.150"`,
			`<h2 id="card-heading" class="popup-title">Triage names the phase a run stopped in</h2>`,
			`<p class="card-run-preserved">preserved: stopped, reviewing — work preserved</p>`,
			"reason: the reviewer asked for repair 3 times",
			`<dd class="card-none">none</dd>`,
			"cost $18.62",
		},
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
	// A closed pop-up is not in the render at all: a page scenario carries no
	// pop-up, a pop-up scenario carries the pop-ups it left open over the page
	// it names rather than that page again, and the one that closed both with
	// Escape carries neither.
	for _, scenario := range []string{"busy", "closed"} {
		if strings.Contains(page(scenario), `class="popup"`) {
			t.Errorf("the %s render carries a pop-up nobody opened", scenario)
		}
	}
	for scenario, beneath := range map[string]string{"card": "busy", "grouping-error": "degraded", "closed": "busy"} {
		if body := page(scenario); strings.Contains(body, `class="panel `) || !strings.Contains(body, "the "+beneath+" render") {
			t.Errorf("the %s render does not stand alone over the %s render", scenario, beneath)
		}
	}
	// Every item the busy page names opens a card, from Running now and from
	// every grouping: the title and the id of each running item, and each entry
	// of a grouping, carry the item they open.
	for _, id := range []string{"yoyodyne-ifd.141.3", "yoyodyne-ifd.201", "yoyodyne-ifd.212", "yoyodyne-ifd.230"} {
		if strings.Count(page("busy"), `data-item="`+id+`"`) != 2 {
			t.Errorf("the busy render does not open %s from both its title and its id", id)
		}
	}
	for _, key := range []string{"admitted", "held", "startable", "running", "landed:today", "landed:week", "pile:held", "pile:directive", "stage:developing", "stage:integrating"} {
		if !strings.Contains(page("busy"), `data-grouping="`+key+`"`) {
			t.Errorf("the busy render has nothing that opens the %s grouping", key)
		}
	}
	// A stage that could not be read still opens, so the reason is readable in
	// full rather than only as a dash.
	for _, key := range []string{"admitted", "held", "startable"} {
		if !strings.Contains(page("degraded"), `data-grouping="`+key+`"`) {
			t.Errorf("the degraded render has nothing that opens the unreadable %s stage", key)
		}
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
