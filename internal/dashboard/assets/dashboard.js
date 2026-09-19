// The page's own script: hold the token, fetch the read model with it, and
// draw the five sections from what comes back. It is served from this origin
// because the policy allows script from nowhere else.
//
// The token lives in sessionStorage and nowhere else. Session storage is scoped
// to the origin — scheme, host, and port — so two dashboards on two ports of
// 127.0.0.1 hold two tokens and neither sees the other's, and it is never sent
// anywhere on its own: this script puts it in the Authorization header of each
// fetch to this origin and nothing else reads it. A cookie would be the wrong
// place, because a cookie on 127.0.0.1 is sent to every port of 127.0.0.1. The
// storage is per tab and ends with the tab, so a new tab asks for the token
// again.
//
// Nothing the read model says is written into the page as markup. Every value
// goes in through textContent, so a work-item title that happens to contain a
// tag is shown as the characters it is. Nothing here sets a style either: the
// policy allows no inline style, so every look is a class the stylesheet owns.
//
// Two readings feed the page, on two clocks. The standing — the four lines and
// the capacity state — is asked for every ten seconds. The throughput — what
// landed and what it cost over today and the last seven days — prices every
// event log a week holds, so it is asked for once a minute. Each section says
// which of its sources it is still waiting for, which one could not be read, and
// what to do about it; none of them ever shows a zero in place of an answer the
// model did not give.
//
// The words are the terminal's. Where `yoyo status` has a way of saying a
// thing — "no developer runs", "cost unknown", "12m", "approved, resuming
// integration" — this says it the same way, because the page and the terminal
// are two projections of one model and a reader moving between them should not
// have to translate.
(function () {
  "use strict";

  var pollStanding = 10000;
  var pollThroughput = 60000;
  var storageKey = "yoyo-dashboard-token";

  var page = document.getElementById("page");
  var observedAt = document.getElementById("observed-at");
  var freshness = document.getElementById("freshness");
  var problem = document.getElementById("problem");
  var remedy = document.getElementById("remedy");
  var stale = document.getElementById("stale");
  var banner = document.getElementById("banner");
  var signin = document.getElementById("signin");
  var signinNote = document.getElementById("signin-note");
  var tokenField = document.getElementById("token");

  // model is what the page has been told so far: each reading, or why it could
  // not be had. A reading once had is kept through a later failure, so a page
  // that was showing something goes on showing it, marked stale, rather than
  // going blank on one dropped poll.
  var model = {
    standing: null,
    standingError: "",
    throughput: null,
    throughputError: ""
  };
  var timers = [];

  // ---- small DOM helpers -------------------------------------------------

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text !== undefined && text !== null) {
      node.textContent = String(text);
    }
    return node;
  }

  function clear(node) {
    while (node.firstChild) {
      node.removeChild(node.firstChild);
    }
  }

  function setHidden(node, hidden) {
    if (hidden) {
      node.setAttribute("hidden", "");
    } else {
      node.removeAttribute("hidden");
    }
  }

  function show(state) {
    page.setAttribute("data-state", state);
  }

  // section moves one panel between its four states. The error state carries
  // what failed and what to do; the empty state carries the sentence that says
  // there is nothing, in words, because an empty panel and a panel nobody
  // filled look the same.
  function section(id, state, text, what) {
    document.getElementById(id).setAttribute("data-state", state);
    document.getElementById(id + "-problem").textContent = state === "error" ? "Could not be read: " + (text || "") : "";
    document.getElementById(id + "-remedy").textContent = state === "error" ? (what || "") : "";
    document.getElementById(id + "-empty").textContent = state === "empty" ? (text || "") : "";
  }

  function listProblems(id, problems) {
    var list = document.getElementById(id);
    clear(list);
    problems.forEach(function (text) {
      if (text) {
        list.appendChild(el("li", null, "Could not be read: " + text));
      }
    });
    setHidden(list, list.firstChild === null);
  }

  // ---- the terminal's words ----------------------------------------------

  function plural(number, noun) {
    return number === 1 ? noun : noun + "s";
  }

  function count(number, noun) {
    if (number === 0) {
      return "no " + noun + "s";
    }
    return number + " " + plural(number, noun);
  }

  function pad(number) {
    return (number < 10 ? "0" : "") + number;
  }

  // age is a Go duration — nanoseconds on the wire — as `yoyo status` says one.
  function age(nanoseconds) {
    var seconds = Math.floor(nanoseconds / 1e9);
    if (seconds < 0) {
      return "no time at all; its record is stamped ahead of this reading";
    }
    if (seconds < 60) {
      return seconds + "s";
    }
    var minutes = Math.floor(seconds / 60);
    if (minutes < 60) {
      return minutes + "m";
    }
    var hours = Math.floor(minutes / 60);
    if (hours < 24) {
      return hours + "h" + pad(minutes % 60) + "m";
    }
    return Math.floor(hours / 24) + "d" + pad(hours % 24) + "h";
  }

  function money(amount) {
    return "$" + Number(amount || 0).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }

  function clock(iso) {
    if (!iso) {
      return "—";
    }
    var when = new Date(iso);
    if (isNaN(when.getTime())) {
      return iso;
    }
    return pad(when.getHours()) + ":" + pad(when.getMinutes()) + ":" + pad(when.getSeconds());
  }

  // named says whether a moment was actually named: the field is absent where
  // the model has none, and a zero time — Go's "0001-01-01" — is the other way a
  // record says there was none.
  function named(iso) {
    return Boolean(iso) && iso.indexOf("0001-") !== 0;
  }

  function dayAndClock(iso) {
    if (!iso) {
      return "—";
    }
    var when = new Date(iso);
    if (isNaN(when.getTime())) {
      return iso;
    }
    return when.getFullYear() + "-" + pad(when.getMonth() + 1) + "-" + pad(when.getDate()) + " " + clock(iso);
  }

  function phaseOf(run) {
    if (run.resuming_integration) {
      return "approved, resuming integration";
    }
    return run.phase || "no phase recorded yet";
  }

  function spendOf(run) {
    if (run.unknown_cost) {
      return "cost unknown (" + run.unknown_cost + ")";
    }
    return money(run.cost_usd || 0) + " so far";
  }

  function provenance(record) {
    var parts = [];
    if (record.backend) {
      parts.push(record.backend);
    }
    if (record.model) {
      parts.push(record.model);
    }
    if (record.account) {
      parts.push("account " + record.account);
    }
    return parts.join(" · ");
  }

  function windowNamed(throughput, label) {
    var found = null;
    (throughput.windows || []).forEach(function (period) {
      if (period.label === label) {
        found = period;
      }
    });
    return found;
  }

  // ---- the header and the banners ----------------------------------------

  // lines is the four lines as the band counts them: the list each one counts,
  // the problem field that replaces the count when the list could not be read,
  // and the words the count is said in.
  var lines = [
    { list: "running", problem: "running_problem", noun: "developer run", suffix: "", label: "Running" },
    { list: "working", problem: "working_problem", noun: "conversation", suffix: " with a turn in flight", label: "Working" },
    { list: "not_startable", problem: "not_startable_problem", noun: "admitted item", suffix: " nothing will pull", label: "Not startable" },
    { list: "needs_human", problem: "needs_human_problem", noun: "thing", suffix: " waiting on a person", label: "Needs a human" }
  ];

  function renderHeader() {
    var standing = model.standing;
    if (standing) {
      observedAt.textContent = clock(standing.observed_at);
      observedAt.setAttribute("datetime", standing.observed_at || "");
    }
    // A poll that fails after one that succeeded marks the page stale rather
    // than blanking it, whichever of the two readings failed: the strip says
    // which, and which reading is still being shown.
    var failed = [];
    if (model.standingError && standing) {
      failed.push("the standing — " + model.standingError + " — so this is the reading from " + clock(standing.observed_at) + ", asked again every ten seconds");
    }
    if (model.throughputError && model.throughput) {
      failed.push("the throughput — " + model.throughputError + " — so its figures are from " + clock(model.throughput.observed_at) + ", asked again every minute");
    }
    if (failed.length > 0) {
      freshness.textContent = "stale";
      freshness.className = "freshness freshness-stale";
      stale.textContent = "The last reading failed for " + failed.join("; and for ") + ".";
      setHidden(stale, false);
    } else {
      freshness.textContent = standing ? "asks again every 10 s" : "";
      freshness.className = "freshness";
      stale.textContent = "";
      setHidden(stale, true);
    }
    // One thing is printed above the sections, and only one: the same sentence
    // the terminal prints above the four lines while the harness is paused on
    // the provider's usage window, or the provider is answering nobody.
    if (standing && standing.paused) {
      banner.textContent = standing.paused;
      setHidden(banner, false);
    } else {
      banner.textContent = "";
      setHidden(banner, true);
    }
  }

  // ---- section 1: the status band ----------------------------------------

  function tile(label, figure, unit, detail, className) {
    var item = el("div", "tile" + (className ? " " + className : ""));
    item.appendChild(el("dt", null, label));
    var value = el("dd");
    value.appendChild(el("span", "figure", figure));
    value.appendChild(el("span", "unit", unit));
    if (detail) {
      value.appendChild(el("span", "detail", detail));
    }
    item.appendChild(value);
    return item;
  }

  function renderBand() {
    var standing = model.standing;
    if (!standing) {
      section("band", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    var unreadable = lines.filter(function (line) { return Boolean(standing[line.problem]); });
    if (unreadable.length === lines.length) {
      section("band", "error", lines.map(function (line) { return standing[line.problem]; }).join("; "), whatToDoAboutTheStanding());
      return;
    }
    var idle = unreadable.length === 0 &&
      standing.running.length === 0 && standing.working.length === 0 &&
      standing.admitted === 0 && standing.needs_human.length === 0;
    if (idle) {
      section("band", "empty", "The harness is idle: nothing is running, no conversation has a turn in flight, nothing is admitted, and nothing waits on a person.");
      return;
    }

    var tiles = document.getElementById("tiles");
    clear(tiles);
    lines.forEach(function (line) {
      // A line whose source could not be read is not counted, because a zero
      // assembled from nothing reads as an empty line. It says so in the count's
      // place, exactly as the terminal does, and the reason is listed under the
      // tiles.
      if (standing[line.problem]) {
        tiles.appendChild(tile(line.label, "—", "could not be read", null, "tile-unreadable"));
        return;
      }
      var items = standing[line.list].length;
      var detail = null;
      if (line.list === "not_startable") {
        detail = "of " + count(standing.admitted, "admitted item");
        if (standing.awaiting_decision || standing.awaiting_carry_out) {
          detail += "; awaiting a decision: " + standing.awaiting_decision + ", awaiting carry-out: " + standing.awaiting_carry_out;
        }
      }
      if (line.list === "needs_human" && items === 0) {
        tiles.appendChild(tile(line.label, "nothing", "waiting on a person", null, "tile-quiet"));
        return;
      }
      tiles.appendChild(tile(line.label, String(items), plural(items, line.noun) + line.suffix, detail, items > 0 && line.list === "needs_human" ? "tile-attention" : null));
    });
    tiles.appendChild(landedTile());
    tiles.appendChild(costTile());
    listProblems("band-problems", unreadable.map(function (line) { return standing[line.problem]; }));
    section("band", "ready");
  }

  function landedTile() {
    var throughput = model.throughput;
    if (!throughput) {
      return tile("Landed", model.throughputError ? "—" : "…", model.throughputError ? "could not be read" : "pricing the week", null, model.throughputError ? "tile-unreadable" : "tile-waiting");
    }
    if (throughput.runs_problem) {
      return tile("Landed", "—", "could not be read", null, "tile-unreadable");
    }
    var today = windowNamed(throughput, "today");
    var week = windowNamed(throughput, "last 7 days");
    return tile("Landed", String(today.landed), "today", count(week.landed, "run") + " in the last 7 days");
  }

  function costTile() {
    var throughput = model.throughput;
    if (!throughput) {
      return tile("Cost", model.throughputError ? "—" : "…", model.throughputError ? "could not be read" : "pricing the week", null, model.throughputError ? "tile-unreadable" : "tile-waiting");
    }
    if (throughput.spend_problem) {
      return tile("Cost", "—", "could not be read", null, "tile-unreadable");
    }
    var today = windowNamed(throughput, "today");
    var week = windowNamed(throughput, "last 7 days");
    return tile("Cost", (today.floor ? "≥ " : "") + money(today.cost_usd), "today", (week.floor ? "at least " : "") + money(week.cost_usd) + " in the last 7 days");
  }

  // ---- section 2: the runs and conversations in flight ---------------------

  function runCard(run) {
    var card = el("li", "card card-run");
    var head = el("div", "card-head");
    head.appendChild(el("span", "card-kind", "developer run"));
    head.appendChild(el("span", "phase", phaseOf(run)));
    card.appendChild(head);
    card.appendChild(el("h3", "card-title", run.title || run.work_item_id));
    var meta = el("p", "card-meta");
    meta.appendChild(el("span", "item-id", run.work_item_id));
    meta.appendChild(el("span", "sep", " · "));
    meta.appendChild(el("span", "elapsed", age(run.elapsed) + " elapsed"));
    meta.appendChild(el("span", "sep", " · "));
    meta.appendChild(el("span", run.unknown_cost ? "spend spend-unknown" : "spend", spendOf(run)));
    card.appendChild(meta);
    var where = provenance(run);
    if (where) {
      card.appendChild(el("p", "card-provenance", where));
    }
    card.appendChild(el("p", "card-id", "started " + dayAndClock(run.started_at) + " · " + run.run_id));
    return card;
  }

  function turnCard(turn) {
    var card = el("li", "card card-turn");
    var head = el("div", "card-head");
    head.appendChild(el("span", "card-kind", "conversation"));
    head.appendChild(el("span", "phase", "a turn in flight"));
    card.appendChild(head);
    card.appendChild(el("h3", "card-title", turn.agent));
    var meta = el("p", "card-meta");
    meta.appendChild(el("span", "item-id", turn.role));
    meta.appendChild(el("span", "sep", " · "));
    meta.appendChild(el("span", "elapsed", "for " + age(turn.elapsed) + " after " + count(turn.turns, "recorded turn")));
    card.appendChild(meta);
    var where = provenance(turn);
    if (where) {
      card.appendChild(el("p", "card-provenance", where));
    }
    return card;
  }

  function renderLive() {
    var standing = model.standing;
    if (!standing) {
      section("live", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    if (standing.running_problem && standing.working_problem) {
      section("live", "error", standing.running_problem + "; " + standing.working_problem, whatToDoAboutTheStanding());
      return;
    }
    var running = standing.running_problem ? [] : standing.running;
    var working = standing.working_problem ? [] : standing.working;
    if (running.length === 0 && working.length === 0 && !standing.running_problem && !standing.working_problem) {
      section("live", "empty", "Nothing is running, and no conversation has a turn in flight.");
      return;
    }
    listProblems("live-problems", [standing.running_problem, standing.working_problem]);
    var cards = document.getElementById("cards");
    clear(cards);
    running.forEach(function (run) { cards.appendChild(runCard(run)); });
    working.forEach(function (turn) { cards.appendChild(turnCard(turn)); });
    // The half that could be read and is empty says so in words, so an
    // unreadable other half does not leave a readable emptiness looking like
    // part of the failure.
    var quiet = [];
    if (!standing.running_problem && running.length === 0) {
      quiet.push("no developer run is in flight");
    }
    if (!standing.working_problem && working.length === 0) {
      quiet.push("no conversation has a turn in flight");
    }
    var note = document.getElementById("live-note");
    note.textContent = quiet.length ? quiet.join(", and ") + "." : "";
    setHidden(note, quiet.length === 0);
    section("live", "ready");
  }

  // ---- section 3: the pipeline --------------------------------------------

  // piles is the queue's own vocabulary for why an admitted item is not pulled,
  // in the order a reader wants them: the ones waiting on a person first, then
  // the ones waiting on the harness or on other work, then the ones nothing
  // here can explain. Each says whose move it is, because a pile with no mover
  // is a pile nobody empties.
  var piles = [
    { kind: "held", label: "held for a person", whose: "the development manager's, or the harness carrying her decision out" },
    { kind: "directive", label: "paused by a directive", whose: "the operator's, through yoyo directive resolve" },
    { kind: "stalled", label: "pullable, and nothing is choosing", whose: "whoever the refusal names" },
    { kind: "parked", label: "parked", whose: "whoever parked it" },
    { kind: "waiting", label: "waiting on other work", whose: "nobody's; it clears as that work lands" },
    { kind: "conversation", label: "carried by a conversation, not a run", whose: "the role the item names" },
    { kind: "unread", label: "not offered, and nothing here can say why", whose: "run yoyo status for the refusal in full" }
  ];

  // stageOrder is the order the read model's three stages are shown in: the
  // developer's part, the reviewer's, the harness's. Which phase is which stage
  // is the model's to say, and each run arrives carrying its stage.
  var stageOrder = ["developing", "reviewing", "integrating"];

  function stage(label, figure, unit, className) {
    var item = el("li", "stage" + (className ? " " + className : ""));
    item.appendChild(el("span", "stage-label", label));
    item.appendChild(el("span", "stage-figure", figure));
    item.appendChild(el("span", "stage-unit", unit));
    return item;
  }

  function renderPipeline() {
    var standing = model.standing;
    if (!standing) {
      section("pipeline", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    // The queue and the runs are the two sources the pipeline stands on. With
    // both unreadable there is nothing to draw and the section says so; with
    // one unreadable the stages that source fills say they could not be read,
    // the rest are drawn, and the reason is listed under them.
    if (standing.not_startable_problem && standing.running_problem) {
      section("pipeline", "error", standing.not_startable_problem + "; " + standing.running_problem, whatToDoAboutTheQueue());
      return;
    }
    var running = standing.running_problem ? [] : standing.running;
    if (standing.admitted === 0 && running.length === 0 && !standing.running_problem && !standing.not_startable_problem) {
      section("pipeline", "empty", "The backlog is empty: nothing is admitted, and nothing is running.");
      return;
    }

    var stages = document.getElementById("stages");
    clear(stages);
    var refused = standing.not_startable_problem ? [] : standing.not_startable;
    if (standing.not_startable_problem) {
      stages.appendChild(stage("Admitted", "—", "could not be read", "stage-unreadable"));
      stages.appendChild(stage("Held back", "—", "could not be read", "stage-unreadable"));
      stages.appendChild(stage("Startable", "—", "could not be read", "stage-unreadable"));
    } else {
      appendQueueStages(stages, standing, refused);
    }

    var runningStage = stage("Running", standing.running_problem ? "—" : String(running.length), standing.running_problem ? "could not be read" : (running.length === 1 ? "developer run" : "developer runs"), standing.running_problem ? "stage-unreadable" : (running.length > 0 ? "stage-flowing" : "stage-clear"));
    if (!standing.running_problem && running.length > 0) {
      var byStage = el("ul", "piles");
      stageOrder.forEach(function (name) {
        var number = running.filter(function (run) { return run.stage === name; }).length;
        if (number === 0) {
          return;
        }
        var entry = el("li", "pile");
        entry.appendChild(el("span", "pile-figure", String(number)));
        entry.appendChild(el("span", "pile-label", name));
        byStage.appendChild(entry);
      });
      runningStage.appendChild(byStage);
    }
    stages.appendChild(runningStage);

    var throughput = model.throughput;
    if (throughput && !throughput.runs_problem) {
      var today = windowNamed(throughput, "today");
      var week = windowNamed(throughput, "last 7 days");
      var landed = stage("Landed", String(today.landed), "today", "stage-landed");
      landed.appendChild(el("span", "stage-detail", count(week.landed, "run") + " in the last 7 days"));
      stages.appendChild(landed);
    } else if (throughput || model.throughputError) {
      stages.appendChild(stage("Landed", "—", "could not be read", "stage-unreadable"));
    } else {
      stages.appendChild(stage("Landed", "…", "pricing the week", "stage-waiting"));
    }

    listProblems("pipeline-problems", [standing.not_startable_problem, standing.running_problem, throughput ? throughput.runs_problem : ""]);
    var note = document.getElementById("pipeline-note");
    var attention = standing.needs_human_problem ? "what waits on a person could not be read: " + standing.needs_human_problem : (standing.needs_human.length === 0 ? "nothing" : count(standing.needs_human.length, "thing")) + " waiting on a person";
    note.textContent = "Needs a human: " + attention + ".";
    section("pipeline", "ready");
  }

  // appendQueueStages draws the three stages the queue fills: what is admitted,
  // what is held back and in which piles, and what the harness pulls next.
  function appendQueueStages(stages, standing, refused) {
    stages.appendChild(stage("Admitted", String(standing.admitted), plural(standing.admitted, "item"), "stage-admitted"));

    var held = stage("Held back", String(refused.length), refused.length === 1 ? "item nothing will pull" : "items nothing will pull", refused.length > 0 ? "stage-held" : "stage-clear");
    var byKind = {};
    refused.forEach(function (item) {
      byKind[item.kind] = (byKind[item.kind] || 0) + 1;
    });
    var breakdown = el("ul", "piles");
    var largest = 0;
    piles.forEach(function (pile) {
      largest = Math.max(largest, byKind[pile.kind] || 0);
    });
    piles.forEach(function (pile) {
      var number = byKind[pile.kind] || 0;
      if (number === 0) {
        return;
      }
      var entry = el("li", "pile" + (number === largest ? " pile-largest" : ""));
      var label = pile.label;
      if (pile.kind === "held" && (standing.awaiting_decision || standing.awaiting_carry_out)) {
        label += ": " + standing.awaiting_decision + " awaiting a decision, " + standing.awaiting_carry_out + " awaiting carry-out";
      }
      entry.appendChild(el("span", "pile-figure", String(number)));
      entry.appendChild(el("span", "pile-label", label + (number === largest ? " (most)" : "")));
      entry.appendChild(el("span", "pile-whose", "whose move: " + pile.whose));
      breakdown.appendChild(entry);
    });
    if (breakdown.firstChild) {
      held.appendChild(breakdown);
    }
    stages.appendChild(held);

    // What the harness pulls next is the model's count, never a subtraction
    // made here: an item a run is carrying is neither refused nor startable,
    // and while the pass-level stall stands every pullable item is refused, so
    // the stage says the harness is choosing nothing rather than naming items
    // it would pull.
    var stalled = refused.filter(function (item) { return item.kind === "stalled"; });
    if (stalled.length > 0) {
      stages.appendChild(stage("Startable", "none", "the harness is choosing nothing: " + stalled[0].reason, "stage-held"));
    } else if (standing.startable > 0) {
      stages.appendChild(stage("Startable", String(standing.startable), standing.startable === 1 ? "item the harness pulls next" : "items the harness pulls next", "stage-flowing"));
    } else {
      stages.appendChild(stage("Startable", "0", "nothing is waiting to be pulled", "stage-clear"));
    }
  }

  // ---- section 4: throughput and cost ------------------------------------

  var kindNouns = { run: "runs", conversation: "conversations", review: "branch reviews", exchange: "exchanges" };

  function figureRow(label, value, className) {
    var row = el("div", "figure-row" + (className ? " " + className : ""));
    row.appendChild(el("dt", null, label));
    row.appendChild(el("dd", null, value));
    return row;
  }

  function windowColumn(period, throughput) {
    var column = el("div", "window");
    column.appendChild(el("h3", "window-label", period.label));
    column.appendChild(el("p", "window-span", period.days === 1 ? "since midnight, local time" : "from " + period.since + ", local days"));
    var figures = el("dl", "figures");
    if (throughput.runs_problem) {
      figures.appendChild(figureRow("Landed", "could not be read", "figure-unreadable"));
    } else {
      figures.appendChild(figureRow("Landed", count(period.landed, "run") + " reached the target branch", period.landed > 0 ? "figure-landed" : null));
      var endings = [];
      if (period.succeeded) { endings.push(period.succeeded + " succeeded without promoting"); }
      if (period.stopped) { endings.push(period.stopped + " stopped for a person"); }
      if (period.cancelled) { endings.push(period.cancelled + " cancelled"); }
      if (period.timed_out) { endings.push(period.timed_out + " timed out"); }
      if (period.failed) { endings.push(period.failed + " failed"); }
      figures.appendChild(figureRow("Other endings", endings.length ? endings.join(", ") : "none"));
      figures.appendChild(figureRow("Started", count(period.started, "run")));
    }
    if (throughput.spend_problem) {
      figures.appendChild(figureRow("Cost", "could not be read", "figure-unreadable"));
    } else {
      figures.appendChild(figureRow("Cost", (period.floor ? "at least " : "") + money(period.cost_usd) + " from " + count(period.invocations, "invocation"), "figure-cost"));
      var split = (period.kinds || []).map(function (kind) {
        return money(kind.cost_usd) + " on " + kind.invocations + " " + (kindNouns[kind.kind] || kind.kind);
      });
      figures.appendChild(figureRow("Of which", split.length ? split.join(", ") : "nothing priced"));
      if (period.unpriced) {
        figures.appendChild(figureRow("Not priced", count(period.unpriced, "exchange record") + " could not be read, so the cost is a floor", "figure-unreadable"));
      }
    }
    column.appendChild(figures);
    return column;
  }

  function renderThroughput() {
    var throughput = model.throughput;
    if (!throughput) {
      section("throughput", model.throughputError ? "error" : "loading", model.throughputError, whatToDoAboutTheThroughput());
      return;
    }
    if (throughput.runs_problem && throughput.spend_problem) {
      section("throughput", "error", throughput.runs_problem + "; " + throughput.spend_problem, whatToDoAboutTheThroughput());
      return;
    }
    var week = windowNamed(throughput, "last 7 days");
    var quiet = week && !throughput.runs_problem && !throughput.spend_problem &&
      week.started === 0 && week.landed === 0 && week.succeeded === 0 && week.stopped === 0 && week.cancelled === 0 && week.timed_out === 0 && week.failed === 0 &&
      week.invocations === 0 && week.unpriced === 0;
    if (quiet) {
      section("throughput", "empty", "Nothing ran and nothing was spent in the last 7 days, from " + week.since + ".");
      return;
    }
    listProblems("throughput-problems", [throughput.runs_problem, throughput.spend_problem]);
    var staleFigures = document.getElementById("throughput-stale");
    staleFigures.textContent = model.throughputError ? "The last reading failed — " + model.throughputError + " — so these are the figures from " + clock(throughput.observed_at) + ". The dashboard asks again every minute." : "";
    setHidden(staleFigures, !model.throughputError);
    var windows = document.getElementById("windows");
    clear(windows);
    (throughput.windows || []).forEach(function (period) {
      windows.appendChild(windowColumn(period, throughput));
    });
    section("throughput", "ready");
  }

  // ---- section 5: provider capacity --------------------------------------

  function heldEntry(kind, title, state, facts, remedyText) {
    var entry = el("li", "held held-" + state);
    var head = el("div", "held-head");
    head.appendChild(el("span", "held-kind", kind));
    head.appendChild(el("span", "held-state", state));
    entry.appendChild(head);
    entry.appendChild(el("h3", "held-title", title));
    var list = el("dl", "held-facts");
    facts.forEach(function (fact) {
      if (fact[1] === null || fact[1] === undefined || fact[1] === "") {
        return;
      }
      list.appendChild(figureRow(fact[0], fact[1]));
    });
    entry.appendChild(list);
    entry.appendChild(el("p", "held-remedy", "What to do: " + remedyText));
    return entry;
  }

  function renderCapacity() {
    var standing = model.standing;
    if (!standing) {
      section("capacity", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    var blocked = standing.capacity_blocked || { runs: [], conversations: [] };
    if (blocked.runs_problem && blocked.conversations_problem) {
      section("capacity", "error", blocked.runs_problem + "; " + blocked.conversations_problem, whatToDoAboutTheStanding());
      return;
    }
    var runs = blocked.runs_problem ? [] : (blocked.runs || []);
    var conversations = blocked.conversations_problem ? [] : (blocked.conversations || []);
    var hold = standing.capacity_hold && standing.capacity_hold.holding ? standing.capacity_hold : null;
    if (!hold && runs.length === 0 && conversations.length === 0 && !blocked.runs_problem && !blocked.conversations_problem) {
      section("capacity", "empty", "No run or conversation is waiting on provider capacity, and no usage window is holding every role.");
      return;
    }

    var holdLine = document.getElementById("capacity-hold");
    if (hold) {
      var until = named(hold.resets_at) ? "until " + dayAndClock(hold.resets_at) : "and the provider named no reset";
      holdLine.textContent = "Every role is held: " + count(hold.agents ? hold.agents.length : 0, "agent") + " on " + (hold.models || []).join(", ") +
        (hold.alternates && hold.alternates.length ? ", failing over to " + hold.alternates.join(", ") : ", and none names an alternate") +
        "; " + count(hold.refusals || 0, "turn") + " refused since " + dayAndClock(hold.since) + ", " + until + ".";
      setHidden(holdLine, false);
    } else {
      holdLine.textContent = "";
      setHidden(holdLine, true);
    }
    listProblems("capacity-problems", [blocked.runs_problem, blocked.conversations_problem]);

    var runList = document.getElementById("capacity-runs");
    clear(runList);
    runs.forEach(function (run) {
      runList.appendChild(heldEntry("run", run.work_item_id, run.state, [
        ["Refused by", run.refused_by],
        ["Phase", run.phase],
        ["Since", dayAndClock(run.since)],
        ["Resets", named(run.resets_at) ? dayAndClock(run.resets_at) : (run.state === "waiting" ? "no reset named; it asks again at the probe interval" : "no reset named, and nothing probes: the run stopped")],
        ["Waited", age((run.waited_seconds || 0) * 1e9) + " of the pause budget"],
        ["Change", run.preserved ? "preserved" : "not preserved"],
        ["Run", run.run_id]
      ], run.remedy));
    });
    var turnList = document.getElementById("capacity-conversations");
    clear(turnList);
    conversations.forEach(function (turn) {
      turnList.appendChild(heldEntry("conversation", turn.waiting, turn.state, [
        ["Refused by", turn.refused_by],
        ["Model", turn.model],
        ["Since", dayAndClock(turn.since)],
        ["Resets", named(turn.resets_at) ? dayAndClock(turn.resets_at) : "no reset named; the next turn finds out"],
        ["Refusals", count(turn.refusals || 0, "turn") + " stopped and still refused"],
        ["Conversation", turn.conversation_id]
      ], turn.remedy));
    });
    section("capacity", "ready");
  }

  // ---- what to do -----------------------------------------------------------

  function whatToDoAboutTheStanding() {
    return "The dashboard asks again every ten seconds. If this stays, yoyo status in the checkout says the same thing with more room, and yoyo doctor says what cannot be read.";
  }

  function whatToDoAboutTheQueue() {
    return "The admitted work is read from the tracker and the runs from the state root; yoyo doctor says whether bd answers in this checkout and what cannot be read, and yoyo status prints the same refusals.";
  }

  function whatToDoAboutTheThroughput() {
    return "The dashboard asks again every minute. yoyo status --spend 7 prices the same records at the terminal and names what could not be read.";
  }

  // ---- the page -------------------------------------------------------------

  function render() {
    if (!model.standing) {
      // Nothing has been read yet. Until the first standing arrives the page is
      // loading; if the first reading failed there is nothing to show but the
      // failure, so the page says that in full and keeps asking.
      if (!model.standingError) {
        show("loading");
        return;
      }
      problem.textContent = model.standingError;
      remedy.textContent = whatToDoAboutTheStanding();
      show("error");
      return;
    }
    show("ready");
    renderHeader();
    renderBand();
    renderLive();
    renderPipeline();
    renderThroughput();
    renderCapacity();
  }

  // ---- the token ------------------------------------------------------------

  function token() {
    try {
      return window.sessionStorage.getItem(storageKey) || "";
    } catch (error) {
      return "";
    }
  }

  function remember(value) {
    try {
      window.sessionStorage.setItem(storageKey, value);
    } catch (error) {
      // Storage refused (a private window, say): the token is held for this
      // page load only, and the page asks again on the next one.
    }
  }

  function forget() {
    try {
      window.sessionStorage.removeItem(storageKey);
    } catch (error) {
      // Nothing to forget where nothing could be kept.
    }
  }

  function stopAsking() {
    timers.forEach(function (timer) { window.clearInterval(timer); });
    timers = [];
  }

  function askForToken(note) {
    stopAsking();
    signinNote.textContent = note || "";
    tokenField.value = "";
    show("signin");
    tokenField.focus();
  }

  // ---- fetching ---------------------------------------------------------------

  // read asks for one reading with the token and hands back the body, or the
  // reason there is none. A 401 is the token being wrong — a mistyped one and a
  // restarted dashboard look the same — and sends the page back to asking for
  // it rather than being reported as a failure of the read model.
  function read(path, current, onBody, onFailure) {
    fetch(path, {
      headers: { Accept: "application/json", Authorization: "Bearer " + current },
      cache: "no-store"
    })
      .then(function (response) {
        if (response.status === 401) {
          forget();
          askForToken("that is not the token this dashboard printed when it started");
          return null;
        }
        return response.json().then(function (body) {
          if (!response.ok) {
            onFailure((body && body.error) || (response.status + " " + response.statusText));
            return null;
          }
          return body;
        }, function () {
          onFailure(response.status + " " + response.statusText + ", and the answer was not JSON");
          return null;
        });
      })
      .then(function (body) {
        if (body) {
          onBody(body);
        }
      })
      .catch(function (error) {
        onFailure("the dashboard could not be reached (" + error.message + "); the yoyo dashboard process may have stopped, and starting it again prints a new token");
      });
  }

  function refreshStanding(current) {
    read("/api/standing", current, function (standing) {
      model.standing = standing;
      model.standingError = "";
      render();
    }, function (reason) {
      model.standingError = reason;
      render();
    });
  }

  function refreshThroughput(current) {
    read("/api/throughput", current, function (throughput) {
      model.throughput = throughput;
      model.throughputError = "";
      render();
    }, function (reason) {
      model.throughputError = reason;
      render();
    });
  }

  function start(current) {
    model = { standing: null, standingError: "", throughput: null, throughputError: "" };
    render();
    refreshStanding(current);
    refreshThroughput(current);
    timers.push(window.setInterval(function () { refreshStanding(current); }, pollStanding));
    timers.push(window.setInterval(function () { refreshThroughput(current); }, pollThroughput));
  }

  signin.addEventListener("submit", function (event) {
    event.preventDefault();
    var entered = tokenField.value.trim();
    if (entered === "") {
      return;
    }
    remember(entered);
    start(entered);
  });

  var held = token();
  if (held !== "") {
    start(held);
  } else {
    askForToken("");
  }
})();
