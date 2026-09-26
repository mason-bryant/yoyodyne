// The page's own script: hold the token, fetch the read model with it, and
// draw the seven sections from what comes back. It is served from this origin
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
// Three readings feed the page, on three clocks. The standing — the four lines
// and the capacity state — is asked for every ten seconds. The throughput —
// what landed over today and the last seven days — and the spend — the last 24
// hours, the last seven local days, and the month of days behind them — are
// each asked for once a minute, the spend because pricing every event log a
// month holds is seconds of work. Each section says which of its sources it is
// still waiting for, which one could not be read, and what to do about it; none
// of them ever shows a zero in place of an answer the model did not give. A
// fourth reading is taken only when asked for: one work item whole, for the
// card a reader opens on it from Running now or from a grouping of the
// pipeline; and a fifth the same way: one program manager instance's current
// lane report, for the card a reader opens on it from the program managers.
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
  var pollSpend = 60000;
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
    throughputError: "",
    spend: null,
    spendError: ""
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

  function windowNamed(reading, label) {
    var found = null;
    (reading.windows || []).forEach(function (period) {
      if (period.label === label) {
        found = period;
      }
    });
    return found;
  }

  // kindNouns is the spend report's own vocabulary for what was invoked, in the
  // words the terminal prints it in, and splitOf is a window's or a day's cost
  // laid out by them — the split the read model summed, never one added up here.
  var kindNouns = { run: "run", conversation: "conversation", review: "branch review", side: "side thread", exchange: "exchange" };

  function splitOf(kinds) {
    var split = (kinds || []).map(function (kind) {
      var noun = kindNouns[kind.kind] || kind.kind;
      return money(kind.cost_usd) + " on " + kind.invocations + " " + plural(kind.invocations, noun);
    });
    return split.length ? split.join(", ") : "nothing priced";
  }

  function figureRow(label, value, className) {
    var row = el("div", "figure-row" + (className ? " " + className : ""));
    row.appendChild(el("dt", null, label));
    row.appendChild(el("dd", null, value));
    return row;
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

  // movers is the read model's own vocabulary for who each thing waiting on a
  // person is waiting on, in the model's order and the model's words: the
  // operator first, because the page is read by the operator and what is his
  // is what the figure has to say, then the roles, then the movers that are
  // not people. Each entry arrives carrying its mover; nothing here reads it
  // off the sentence beside it. A test holds this list to the model's, token
  // and wording alike. On 2026-09-20 the tile said sixty-four things needed a
  // human, and three of them were the human's.
  var movers = [
    { mover: "operator", label: "the operator's" },
    { mover: "product-manager", label: "the Lead Product Manager's" },
    { mover: "architect", label: "the architect's" },
    { mover: "development-manager", label: "the development manager's" },
    { mover: "developer", label: "the developer's" },
    { mover: "reviewer", label: "the reviewer's" },
    { mover: "harness", label: "the harness's" },
    { mover: "forge", label: "the forge's" },
    { mover: "provider", label: "the provider's" },
    { mover: "nobody", label: "nobody's" },
    { mover: "unnamed-role", label: "the role it names" }
  ];

  // byMover counts the entries waiting on each mover, in the vocabulary's
  // order, with the operator's count always present — a zero there is the
  // fact the operator most wants — and every other mover's only where it is
  // not zero. The model refuses an entry carrying a mover outside its
  // vocabulary, so every entry lands under one of the names above.
  function byMover(entries) {
    var counted = {};
    entries.forEach(function (entry) {
      counted[entry.mover] = (counted[entry.mover] || 0) + 1;
    });
    var counts = [];
    movers.forEach(function (named) {
      var number = counted[named.mover] || 0;
      if (number > 0 || named.mover === "operator") {
        counts.push({ mover: named.mover, label: named.label, number: number });
      }
    });
    return counts;
  }

  // moverCounts says the counts as a clause: "the operator's: 3, the
  // architect's: 44, the harness's: 1".
  function moverCounts(counts) {
    return counts.map(function (each) { return each.label + ": " + each.number; }).join(", ");
  }

  // moverLabel is the mover's possessive, from the vocabulary above.
  function moverLabel(mover) {
    var found = null;
    movers.forEach(function (named) {
      if (named.mover === mover) {
        found = named;
      }
    });
    return found ? found.label : String(mover);
  }

  // moverRank is where a mover stands in the vocabulary's order, which is the
  // order the list of what waits on a person is shown in: the operator's
  // first, because the page is his.
  function moverRank(mover) {
    var rank = movers.length;
    movers.forEach(function (named, at) {
      if (named.mover === mover) {
        rank = at;
      }
    });
    return rank;
  }

  // kinds is the read model's own vocabulary for what sort of thing an entry
  // on the attention line is about, in the model's order, each with the plain
  // words its card is headed by. A test holds this list to the model's.
  var kinds = [
    { attention: "amendment", title: "A change proposed to a document" },
    { attention: "conversation-carried-item", title: "A work item carried by a conversation" },
    { attention: "report", title: "The pile of collected reports" },
    { attention: "owed-step", title: "A run that still owes a step" },
    { attention: "publication", title: "A promotion the forge has not published" },
    { attention: "degraded-service", title: "A part of the product left down" },
    { attention: "failing-task", title: "A recurring task failing before its first turn" },
    { attention: "hold", title: "A hold over the harness" },
    { attention: "directive", title: "An unresolved directive" },
    { attention: "outage", title: "The provider answering nobody" },
    { attention: "stall", title: "A queue nothing is pulling from" },
    { attention: "held-work", title: "Admitted work held back" }
  ];

  function kindTitle(kind) {
    var found = null;
    kinds.forEach(function (named) {
      if (named.attention === kind) {
        found = named;
      }
    });
    return found ? found.title : String(kind);
  }

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
    if (model.spendError && model.spend) {
      failed.push("the spend — " + model.spendError + " — so its figures are from " + clock(model.spend.observed_at) + ", asked again every minute");
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

  // tile is one tile of the band. A tile given a grouping has its label open
  // the list behind its figure, as a stage's label does in the pipeline.
  function tile(label, figure, unit, detail, className, grouping) {
    var item = el("div", "tile" + (className ? " " + className : ""));
    var head = el("dt", null, grouping ? null : label);
    if (grouping) {
      head.appendChild(groupingOpener(grouping, "tile-label", label));
    }
    item.appendChild(head);
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
        // The attention line still opens its list, so the reason it could not
        // be read is readable in full rather than only as a dash.
        tiles.appendChild(tile(line.label, "—", "could not be read", null, "tile-unreadable", line.list === "needs_human" ? "attention" : null));
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
      if (line.list === "needs_human") {
        tiles.appendChild(needsHumanTile(line.label, standing.needs_human));
        return;
      }
      tiles.appendChild(tile(line.label, String(items), plural(items, line.noun) + line.suffix, detail));
    });
    tiles.appendChild(landedTile());
    tiles.appendChild(costTile());
    listProblems("band-problems", unreadable.map(function (line) { return standing[line.problem]; }));
    section("band", "ready");
  }

  // needsHumanTile is the attention line counted per mover. The figure is what
  // waits on the operator, because the tile is read by the operator and a
  // figure that counted the roles' waits in with his said the wrong thing on
  // the page's most important line; the detail is the line's whole count, which
  // is the figure the terminal prints, and then each role's and the harness's
  // beside it. It asks for attention when something waits on the operator.
  // Its label opens the list of the entries, each of which opens a card.
  function needsHumanTile(label, entries) {
    if (entries.length === 0) {
      return tile(label, "nothing", "waiting on a person", null, "tile-quiet", "attention");
    }
    var counts = byMover(entries);
    var operator = counts[0];
    var others = counts.slice(1);
    var detail = others.length ? "of " + count(entries.length, "thing") + " waiting in all; " + moverCounts(others) : null;
    return tile(label, String(operator.number), plural(operator.number, "thing") + " waiting on the operator", detail, operator.number > 0 ? "tile-attention" : null, "attention");
  }

  function landedTile() {
    var throughput = model.throughput;
    if (!throughput) {
      return tile("Landed", model.throughputError ? "—" : "…", model.throughputError ? "could not be read" : "reading the week", null, model.throughputError ? "tile-unreadable" : "tile-waiting");
    }
    if (throughput.runs_problem) {
      return tile("Landed", "—", "could not be read", null, "tile-unreadable");
    }
    var today = windowNamed(throughput, "today");
    var week = windowNamed(throughput, "last 7 days");
    return tile("Landed", String(today.landed), "today", count(week.landed, "run") + " in the last 7 days");
  }

  // costTile is the spend box's figure said once more at the top of the page,
  // from the same reading: the last twenty-four hours, with the last seven days
  // beside it. Its label opens the month, as the box's own does.
  function costTile() {
    var spend = model.spend;
    if (!spend) {
      return tile("Cost", model.spendError ? "—" : "…", model.spendError ? "could not be read" : "pricing the month", null, model.spendError ? "tile-unreadable" : "tile-waiting", "spend:days");
    }
    if (spend.problem) {
      return tile("Cost", "—", "could not be read", null, "tile-unreadable", "spend:days");
    }
    var day = windowNamed(spend, "last 24 hours");
    var week = windowNamed(spend, "last 7 days");
    return tile("Cost", (day.floor ? "≥ " : "") + money(day.cost_usd), "in the last 24 hours", (week.floor ? "at least " : "") + money(week.cost_usd) + " in the last 7 days", null, "spend:days");
  }

  // ---- section 2: what the harness is spending ----------------------------

  // The box at the top of the page: the last twenty-four hours, reckoned from
  // this reading rather than from midnight, and the last seven local days,
  // each split by kind, with the count of unpriced records beside a cost that
  // is therefore a floor. Its label opens the listing of the month behind it.
  // Every figure is the read model's; nothing is added up here.
  function spendWindowColumn(period) {
    var column = el("div", "window");
    column.appendChild(el("h3", "window-label", period.label));
    column.appendChild(el("p", "window-span", period.rolling ? "rolling, from " + dayAndClock(period.since) : "from " + period.since_day + ", local days"));
    var figures = el("dl", "figures");
    figures.appendChild(figureRow("Cost", (period.floor ? "at least " : "") + money(period.cost_usd) + " from " + count(period.invocations, "invocation"), "figure-cost"));
    figures.appendChild(figureRow("Of which", splitOf(period.kinds)));
    if (period.unpriced) {
      figures.appendChild(figureRow("Not priced", count(period.unpriced, "exchange record") + " could not be read, so the cost is a floor", "figure-unreadable"));
    }
    column.appendChild(figures);
    return column;
  }

  // Nothing spent is a sentence rather than a column of zeroes, and it says
  // how far the log reaches, because nothing spent in a month and a log that
  // does not go back a month are different answers. The box and the listing
  // behind it both say it, so both read it from here.
  function spentNothing(spend) {
    return spend.unpriced === 0 && (!spend.undated || spend.undated.invocations === 0) &&
      (spend.days || []).every(function (day) { return day.invocations === 0; });
  }

  function nothingSpent(spend) {
    return spend.reaches
      ? "Nothing was spent in the last 30 days; the oldest priced record here is from " + spend.reaches + "."
      : "Nothing is recorded as spent: no run, conversation, branch review, side thread, or exchange here has a priced record.";
  }

  function renderSpend() {
    var spend = model.spend;
    if (!spend) {
      section("spend", model.spendError ? "error" : "loading", model.spendError, whatToDoAboutTheSpend());
      return;
    }
    if (spend.problem) {
      section("spend", "error", spend.problem, whatToDoAboutTheSpend());
      return;
    }
    if (spentNothing(spend)) {
      section("spend", "empty", nothingSpent(spend));
      return;
    }
    var staleFigures = document.getElementById("spend-stale");
    staleFigures.textContent = model.spendError
      ? "The last reading failed — " + model.spendError + " — so these are the figures from " + clock(spend.observed_at) + ". The dashboard asks again every minute."
      : "";
    setHidden(staleFigures, !model.spendError);
    var windows = document.getElementById("spend-windows");
    clear(windows);
    (spend.windows || []).forEach(function (period) {
      windows.appendChild(spendWindowColumn(period));
    });
    var label = document.getElementById("spend-open");
    clear(label);
    label.appendChild(groupingOpener("spend:days", "spend-label", "Every day for the past 30 days"));
    section("spend", "ready");
  }

  // ---- section 3: the runs and conversations in flight ---------------------

  function runCard(run) {
    var card = el("li", "card card-run");
    var head = el("div", "card-head");
    head.appendChild(el("span", "card-kind", "developer run"));
    head.appendChild(el("span", "phase", phaseOf(run)));
    card.appendChild(head);
    // The title and the id each open the item's card: two things to click on
    // because a reader's eye lands on either.
    var title = el("h3", "card-title");
    title.appendChild(itemOpener(run.work_item_id, run.title || run.work_item_id));
    card.appendChild(title);
    var meta = el("p", "card-meta");
    meta.appendChild(itemOpener(run.work_item_id, run.work_item_id, "item-id"));
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

  // ---- section 4: the pipeline --------------------------------------------

  // piles is the queue's own vocabulary for why an admitted item is not pulled,
  // in the order a reader wants them: the ones waiting on a person first, then
  // the ones waiting on the harness or on other work, then the ones nothing
  // here can explain. Each says who it is waiting on, because a pile with no
  // mover is a pile nobody empties.
  var piles = [
    { kind: "held", label: "held for a person", whose: "the development manager, or the harness carrying her decision out" },
    { kind: "directive", label: "paused by a directive", whose: "the operator, through yoyo directive resolve" },
    { kind: "stalled", label: "pullable, and nothing is choosing", whose: "whoever the refusal names" },
    { kind: "parked", label: "parked", whose: "whoever parked it" },
    { kind: "waiting", label: "waiting on other work", whose: "nobody; it clears as that work lands" },
    { kind: "covered", label: "covered by its children", whose: "nobody; the children are the work, and it clears as they land" },
    { kind: "conversation", label: "carried by a conversation, not a run", whose: "the role the item names" },
    { kind: "unread", label: "not offered, and nothing here can say why", whose: "nobody this page can name; run yoyo status for the refusal in full" }
  ];

  // stageOrder is the order the read model's three stages are shown in: the
  // developer's part, the reviewer's, the harness's. Which phase is which stage
  // is the model's to say, and each run arrives carrying its stage.
  var stageOrder = ["developing", "reviewing", "integrating"];

  // stage is one stage of the pipeline. Its label is a button that opens the
  // list of the items the figure counts — or, for a stage whose source could
  // not be read or is still being priced, the pop-up saying so with the reason
  // in full.
  function stage(label, figure, unit, className, grouping) {
    var item = el("li", "stage" + (className ? " " + className : ""));
    item.appendChild(groupingOpener(grouping, "stage-label", label));
    item.appendChild(el("span", "stage-figure", figure));
    item.appendChild(el("span", "stage-unit", unit));
    return item;
  }

  // pile is one pile under a stage: a figure, and a label that opens the list
  // of the items in it.
  function pile(grouping, figure, label, className) {
    var entry = el("li", "pile" + (className ? " " + className : ""));
    entry.appendChild(el("span", "pile-figure", figure));
    entry.appendChild(groupingOpener(grouping, "pile-label", label));
    return entry;
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
      stages.appendChild(stage("Admitted", "—", "could not be read", "stage-unreadable", "admitted"));
      stages.appendChild(stage("Held back", "—", "could not be read", "stage-unreadable", "held"));
      stages.appendChild(stage("Startable", "—", "could not be read", "stage-unreadable", "startable"));
    } else {
      appendQueueStages(stages, standing, refused);
    }

    var runningStage = stage("Running", standing.running_problem ? "—" : String(running.length), standing.running_problem ? "could not be read" : (running.length === 1 ? "developer run" : "developer runs"), standing.running_problem ? "stage-unreadable" : (running.length > 0 ? "stage-flowing" : "stage-clear"), "running");
    if (!standing.running_problem && running.length > 0) {
      var byStage = el("ul", "piles");
      stageOrder.forEach(function (name) {
        var number = running.filter(function (run) { return run.stage === name; }).length;
        if (number === 0) {
          return;
        }
        byStage.appendChild(pile("stage:" + name, String(number), name));
      });
      runningStage.appendChild(byStage);
    }
    stages.appendChild(runningStage);

    var throughput = model.throughput;
    if (throughput && !throughput.runs_problem) {
      var today = windowNamed(throughput, "today");
      var week = windowNamed(throughput, "last 7 days");
      var landed = stage("Landed", String(today.landed), "today", "stage-landed", "landed:today");
      // The week is a grouping of its own, opened from its own line.
      landed.appendChild(groupingOpener("landed:week", "stage-detail", count(week.landed, "run") + " in the last 7 days"));
      stages.appendChild(landed);
    } else if (throughput || model.throughputError) {
      stages.appendChild(stage("Landed", "—", "could not be read", "stage-unreadable", "landed:today"));
    } else {
      stages.appendChild(stage("Landed", "…", "reading the week", "stage-waiting", "landed:today"));
    }

    listProblems("pipeline-problems", [standing.not_startable_problem, standing.running_problem, throughput ? throughput.runs_problem : ""]);
    var note = document.getElementById("pipeline-note");
    // The line under the pipeline is the terminal's Needs-a-human head, with
    // the count said per mover after it, the operator's first.
    var attention;
    if (standing.needs_human_problem) {
      attention = "what waits on a person could not be read: " + standing.needs_human_problem;
    } else if (standing.needs_human.length === 0) {
      attention = "nothing waiting on a person";
    } else {
      attention = count(standing.needs_human.length, "thing") + " waiting on a person — " + moverCounts(byMover(standing.needs_human));
    }
    note.textContent = "Needs a human: " + attention + ".";
    section("pipeline", "ready");
  }

  // appendQueueStages draws the three stages the queue fills: what is admitted,
  // what is held back and in which piles, and what the harness pulls next.
  function appendQueueStages(stages, standing, refused) {
    stages.appendChild(stage("Admitted", String(standing.admitted), plural(standing.admitted, "item"), "stage-admitted", "admitted"));

    var held = stage("Held back", String(refused.length), refused.length === 1 ? "item nothing will pull" : "items nothing will pull", refused.length > 0 ? "stage-held" : "stage-clear", "held");
    var byKind = {};
    refused.forEach(function (item) {
      byKind[item.kind] = (byKind[item.kind] || 0) + 1;
    });
    var breakdown = el("ul", "piles");
    var largest = 0;
    piles.forEach(function (named) {
      largest = Math.max(largest, byKind[named.kind] || 0);
    });
    piles.forEach(function (named) {
      var number = byKind[named.kind] || 0;
      if (number === 0) {
        return;
      }
      var entry = pile("pile:" + named.kind, String(number), pileLabel(named, standing) + (number === largest ? " (most)" : ""), number === largest ? "pile-largest" : null);
      entry.appendChild(el("span", "pile-whose", "waiting on: " + named.whose));
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
      stages.appendChild(stage("Startable", "none", "the harness is choosing nothing: " + stalled[0].reason, "stage-held", "startable"));
    } else if (standing.startable > 0) {
      stages.appendChild(stage("Startable", String(standing.startable), standing.startable === 1 ? "item the harness pulls next" : "items the harness pulls next", "stage-flowing", "startable"));
    } else {
      stages.appendChild(stage("Startable", "0", "nothing is waiting to be pulled", "stage-clear", "startable"));
    }
  }

  // pileLabel is a pile's name, with the held pile split by who it waits on.
  function pileLabel(named, standing) {
    var label = named.label;
    if (named.kind === "held" && (standing.awaiting_decision || standing.awaiting_carry_out)) {
      label += ": " + standing.awaiting_decision + " awaiting a decision, " + standing.awaiting_carry_out + " awaiting carry-out";
    }
    return label;
  }

  function pileNamed(kind) {
    var found = null;
    piles.forEach(function (named) {
      if (named.kind === kind) {
        found = named;
      }
    });
    return found;
  }

  // ---- section 5: throughput ---------------------------------------------

  function windowColumn(period) {
    var column = el("div", "window");
    column.appendChild(el("h3", "window-label", period.label));
    column.appendChild(el("p", "window-span", period.days === 1 ? "since midnight, local time" : "from " + period.since + ", local days"));
    var figures = el("dl", "figures");
    figures.appendChild(figureRow("Landed", count(period.landed, "run") + " reached the target branch", period.landed > 0 ? "figure-landed" : null));
    var endings = [];
    if (period.succeeded) { endings.push(period.succeeded + " succeeded without promoting"); }
    if (period.stopped) { endings.push(period.stopped + " stopped for a person"); }
    if (period.cancelled) { endings.push(period.cancelled + " cancelled"); }
    if (period.timed_out) { endings.push(period.timed_out + " timed out"); }
    if (period.failed) { endings.push(period.failed + " failed"); }
    figures.appendChild(figureRow("Other endings", endings.length ? endings.join(", ") : "none"));
    figures.appendChild(figureRow("Started", count(period.started, "run")));
    column.appendChild(figures);
    return column;
  }

  function renderThroughput() {
    var throughput = model.throughput;
    if (!throughput) {
      section("throughput", model.throughputError ? "error" : "loading", model.throughputError, whatToDoAboutTheThroughput());
      return;
    }
    if (throughput.runs_problem) {
      section("throughput", "error", throughput.runs_problem, whatToDoAboutTheThroughput());
      return;
    }
    var week = windowNamed(throughput, "last 7 days");
    var quiet = week &&
      week.started === 0 && week.landed === 0 && week.succeeded === 0 && week.stopped === 0 && week.cancelled === 0 && week.timed_out === 0 && week.failed === 0;
    if (quiet) {
      section("throughput", "empty", "Nothing ran in the last 7 days, from " + week.since + ".");
      return;
    }
    listProblems("throughput-problems", [throughput.runs_problem]);
    var staleFigures = document.getElementById("throughput-stale");
    staleFigures.textContent = model.throughputError ? "The last reading failed — " + model.throughputError + " — so these are the figures from " + clock(throughput.observed_at) + ". The dashboard asks again every minute." : "";
    setHidden(staleFigures, !model.throughputError);
    var windows = document.getElementById("windows");
    clear(windows);
    (throughput.windows || []).forEach(function (period) {
      windows.appendChild(windowColumn(period));
    });
    section("throughput", "ready");
  }

  // ---- section 6: provider capacity --------------------------------------

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

  // ---- section 7: the program managers ------------------------------------

  // managerStatuses is the read model's word for an instance's status, each
  // with the class its badge is tinted by. The word is always in the badge, so
  // the tint is never the only thing that tells two instances apart; beside it
  // the rule is solid for a working instance, dashed for a blocked one, and
  // dotted for a stale one. A test holds this list to the model's.
  var managerStatuses = [
    { status: "blocked", className: "manager-blocked" },
    { status: "stale", className: "manager-stale" },
    { status: "working", className: "manager-working" }
  ];

  function managerClass(status) {
    var found = null;
    managerStatuses.forEach(function (named) {
      if (named.status === status) {
        found = named;
      }
    });
    return found ? found.className : "manager-unknown";
  }

  // managerWhy is what follows an instance's status word, as `yoyo status`
  // says it: why it is stale, how many open asks of its own block it, and how
  // many of its report's blockers the record does not bear out. Both halves
  // are said where both hold, because stale outranks blocked in the word.
  function managerWhy(instance) {
    var parts = [];
    if (instance.stale) {
      parts.push(instance.stale_says);
    }
    var blockers = instance.blockers || [];
    if (instance.blocked) {
      parts.push("blocked on " + count(blockers.length, "open ask") + " (" + blockers.map(function (blocker) { return blocker.cites; }).join(", ") + ")");
    }
    var claims = instance.claims || [];
    if (claims.length > 0) {
      parts.push(count(claims.length, "blocker") + " its report names that the record does not bear out");
    }
    return parts.join("; ");
  }

  function managerBadge(status) {
    return el("span", "manager-status", status);
  }

  function managerRow(instance) {
    var row = el("li", "held manager " + managerClass(instance.status));
    var head = el("div", "held-head");
    head.appendChild(el("span", "held-kind", "program manager"));
    head.appendChild(managerBadge(instance.status));
    row.appendChild(head);
    row.appendChild(el("h3", "held-title", instance.agent));
    var facts = el("dl", "held-facts");
    facts.appendChild(figureRow("Lane", instance.lane || "no lane configured"));
    var why = managerWhy(instance);
    if (why) {
      facts.appendChild(figureRow("Why", why));
    }
    facts.appendChild(figureRow("Last pass", named(instance.last_completed_pass_at) ? "completed " + dayAndClock(instance.last_completed_pass_at) : "none has completed"));
    facts.appendChild(figureRow("Report", named(instance.report_written_at) ? "written " + dayAndClock(instance.report_written_at) : "none written yet"));
    var requests = instance.restart_requests || [];
    if (requests.length > 0) {
      facts.appendChild(figureRow("Restarts", count(requests.length, "restart request") + " open, which nothing acts on yet"));
    }
    row.appendChild(facts);
    var open = el("p", "manager-open");
    open.appendChild(reportOpener(instance.agent, "Open " + instance.agent + "'s current report"));
    row.appendChild(open);
    return row;
  }

  function renderManagers() {
    var standing = model.standing;
    if (!standing) {
      section("managers", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    var instances = standing.program_managers || [];
    if (instances.length === 0 && standing.program_managers_problem) {
      section("managers", "error", standing.program_managers_problem, whatToDoAboutTheStanding());
      return;
    }
    if (instances.length === 0) {
      section("managers", "empty", "No instance of the program manager role is configured, and none has a restart request open.");
      return;
    }
    listProblems("managers-problems", [standing.program_managers_problem]);
    var list = document.getElementById("managers-list");
    clear(list);
    instances.forEach(function (instance) {
      list.appendChild(managerRow(instance));
    });
    section("managers", "ready");
  }

  // ---- the pop-ups: a grouping's items, and one item's card -------------------

  // Two pop-ups, each a dialog over the page with the four states a section
  // has. The grouping lists what is behind one figure — the work items behind
  // a stage of the pipeline or a pile under one, by title, or the entries
  // behind the Needs-a-human tile, each by what it is and who it is waiting on —
  // drawn from the readings the page already holds and drawn again on every
  // poll while it is open, so it stays as live as the figure it was opened
  // from. The card is one thing whole: a work item, read from /api/items/<id>
  // when it is opened and not before, because it costs a tracker command; or
  // one entry of the attention line, drawn from the standing in hand — an
  // amendment with its proposed change and its reason in full, a run that owes
  // a step, an item a conversation carries — and drawn again on every poll, so
  // an entry settled since the card was opened says so. A card opens over a
  // grouping, and each closes on its button, on its backdrop, or on Escape,
  // putting focus back where it was.
  //
  // Nothing here reads the tracker or the amendment store: the card is the read
  // model's projection of the item or the entry, served by the same process
  // behind the same token. And nothing here acts: every button opens or closes
  // a pop-up and does nothing else.

  var groupingPopup = document.getElementById("grouping");
  var cardPopup = document.getElementById("card");
  // openGrouping is the key of the grouping that is open, or null; openCard is
  // the id of the item whose card is open, and openEntry the key of the
  // attention entry whose card is open — at most one of the two, or neither.
  // Each pop-up remembers what opened it, to give focus back to: the element,
  // and the key it carries, because every poll clears and redraws the
  // sections, so by the time a pop-up closes the element that opened it may be
  // gone from the page and what stands in its place is the element now
  // carrying the same key.
  var openGrouping = null;
  var openCard = null;
  var openEntry = null;
  var openers = { grouping: null, card: null, report: null };
  // openersByKey is every opener on the page by the key it opens, newest last,
  // kept only while it is on the page: what close() gives focus back to when
  // the opener it remembered has been redrawn.
  var openersByKey = {};

  function opener(kind, key, button) {
    var name = kind + "=" + key;
    openersByKey[name] = (openersByKey[name] || []).filter(function (each) { return each.isConnected; });
    openersByKey[name].push(button);
    button.setAttribute("type", "button");
    button.setAttribute(kind, key);
    return button;
  }

  function itemOpener(id, text, className) {
    var button = opener("data-item", id, el("button", "item-open" + (className ? " " + className : ""), text));
    button.addEventListener("click", function () { showCard(id, { element: button, kind: "data-item", key: id }); });
    return button;
  }

  function groupingOpener(key, className, text) {
    var button = opener("data-grouping", key, el("button", "grouping-open" + (className ? " " + className : ""), text));
    button.addEventListener("click", function () { showGrouping(key, { element: button, kind: "data-grouping", key: key }); });
    return button;
  }

  // entryKey names one entry of the attention line so a card can be opened on
  // it and found again on the next poll: its kind and the id of the record it
  // is about, or, for the two kinds about a set rather than a record, which
  // set — held work is one entry per wait, and the report pile is one.
  function entryKey(entry) {
    if (entry.kind === "held-work") {
      return entry.kind + ":" + (entry.held_work ? entry.held_work.awaiting : "");
    }
    return entry.kind + ":" + (entry.id || "");
  }

  function entryOpener(key, text, className) {
    var button = opener("data-entry", key, el("button", "item-open" + (className ? " " + className : ""), text));
    button.addEventListener("click", function () { showEntry(key, { element: button, kind: "data-entry", key: key }); });
    return button;
  }

  function open(popup) {
    setHidden(popup, false);
  }

  // close hides a pop-up and gives focus back to what opened it — the very
  // element where it is still on the page, and otherwise the element now
  // carrying the key it carried, drawn by a poll since.
  function close(popup, which) {
    setHidden(popup, true);
    var back = openers[which];
    openers[which] = null;
    if (!back) {
      return;
    }
    var target = back.element.isConnected ? back.element : null;
    if (!target) {
      var current = (openersByKey[back.kind + "=" + back.key] || []).filter(function (each) { return each.isConnected; });
      target = current.length > 0 ? current[0] : null;
    }
    if (target) {
      target.focus();
    }
  }

  // ---- the grouping pop-up

  // groupingOf is what one grouping key lists, from the readings in hand: its
  // title and a note saying what the list is, and either the items — each with
  // the word the pipeline had for it — or why there are none to list. The keys
  // are the pipeline's own: a stage, `pile:<kind>` for a pile under Held back,
  // `stage:<name>` for a pile under Running, and `landed:today` or
  // `landed:week` — and `attention` for the band's Needs-a-human tile, which
  // lists the entries of the attention line rather than work items.
  function groupingOf(key) {
    var standing = model.standing;
    var parts = key.split(":");
    var kind = parts[0];
    var which = parts[1];
    if (kind === "landed") {
      return landedGrouping(which);
    }
    if (kind === "spend") {
      return spendGrouping();
    }
    if (!standing) {
      return { title: kind === "attention" ? "Needs a human" : "Where the work stands", note: "", state: model.standingError ? "error" : "loading", problem: model.standingError, remedy: whatToDoAboutTheStanding() };
    }
    if (kind === "attention") {
      return attentionGrouping(standing);
    }
    if (kind === "running" || kind === "stage") {
      var running = standing.running_problem ? [] : standing.running;
      var named = kind === "stage" ? running.filter(function (run) { return run.stage === which; }) : running;
      return listing(
        kind === "stage" ? "Running: " + which : "Running",
        kind === "stage" ? "the developer runs in flight whose phase is in the " + which + " stage" : "the developer runs in flight, each with its phase",
        standing.running_problem, whatToDoAboutTheStanding(),
        kind === "stage" ? "No developer run is " + which + "." : "No developer run is in flight.",
        named.map(function (run) { return { id: run.work_item_id, title: run.title, detail: phaseOf(run) + ", " + age(run.elapsed) + " elapsed" }; })
      );
    }
    var refused = standing.not_startable_problem ? [] : standing.not_startable;
    var withReason = function (item) { return { id: item.work_item_id, title: item.title, detail: item.reason }; };
    switch (kind) {
      case "admitted":
        return listing("Admitted", "every admitted item, in the Lead Product Manager's order", standing.not_startable_problem, whatToDoAboutTheQueue(), "No work item is admitted.",
          (standing.admitted_items || []).map(function (item) { return { id: item.work_item_id, title: item.title }; }));
      case "held":
        return listing("Held back", "admitted items nothing will pull, each with the refusal that stops it", standing.not_startable_problem, whatToDoAboutTheQueue(), "No admitted item is held back.", refused.map(withReason));
      case "pile":
        var found = pileNamed(which);
        return listing("Held back: " + (found ? found.label : which), found ? "waiting on: " + found.whose : "", standing.not_startable_problem, whatToDoAboutTheQueue(), "No admitted item is in this pile.",
          refused.filter(function (item) { return item.kind === which; }).map(withReason));
      case "startable":
        var stalled = refused.filter(function (item) { return item.kind === "stalled"; });
        return listing("Startable", stalled.length > 0 ? "the harness is choosing nothing: " + stalled[0].reason : "the admitted items nothing refuses, which the harness pulls next in this order",
          standing.not_startable_problem, whatToDoAboutTheQueue(), "No admitted item is startable.",
          (standing.startable_items || []).map(function (item) { return { id: item.work_item_id, title: item.title }; }));
      default:
        return listing(key, "", "the page asked for a grouping it does not have", "", "", []);
    }
  }

  function landedGrouping(which) {
    var throughput = model.throughput;
    var label = which === "week" ? "last 7 days" : "today";
    var title = "Landed " + label;
    if (!throughput) {
      return { title: title, note: "", state: model.throughputError ? "error" : "loading", waiting: "Reading what the runs came to…", problem: model.throughputError, remedy: whatToDoAboutTheThroughput() };
    }
    var period = windowNamed(throughput, label);
    return listing(title, period ? (which === "week" ? "runs whose work reached the target branch from " + period.since + ", local days, newest first" : "runs whose work reached the target branch since midnight, local time, newest first") : "",
      throughput.runs_problem, whatToDoAboutTheThroughput(), "No run landed its work " + label + ".",
      (period && period.landed_items ? period.landed_items : []).map(function (run) { return { id: run.work_item_id, title: run.title, detail: "landed " + dayAndClock(run.landed_at) }; }));
  }

  // spendGrouping lists what each of the last thirty local days cost, newest
  // first, with the by-kind split the read model summed beside each. A day no
  // priced record goes back to says so rather than reading as a day nothing was
  // spent on, because only one of those is zero. Two lines follow
  // the days where there is anything to say: the spend whose moment could not
  // be read, which is counted in every window of the box and on no day here,
  // and the records that could not be priced at all, which make every figure
  // above a floor. Nothing in the list opens anything — a day is not a record
  // this page can show more of.
  function spendGrouping() {
    var spend = model.spend;
    var title = "Spend by day";
    var note = "one line per local day for the past 30 days, newest first, priced from the spend log";
    if (!spend) {
      return { title: title, note: note, state: model.spendError ? "error" : "loading", waiting: "Pricing the last thirty days…", problem: model.spendError, remedy: whatToDoAboutTheSpend() };
    }
    if (!spend.problem && spentNothing(spend)) {
      return listing(title, note, "", "", nothingSpent(spend), []);
    }
    var lines = (spend.days || []).map(function (day) {
      var detail = "no priced record reaches this far back";
      if (day.reached) {
        detail = day.invocations === 0
          ? "nothing spent"
          : money(day.cost_usd) + " from " + count(day.invocations, "invocation") + " — " + splitOf(day.kinds);
      }
      return { day: day.day, title: day.day, detail: detail, className: day.reached ? null : "grouping-day-unreached" };
    });
    if (spend.undated && spend.undated.invocations > 0) {
      lines.push({
        day: spend.undated.day,
        title: "undated",
        detail: money(spend.undated.cost_usd) + " from " + count(spend.undated.invocations, "invocation") + " whose moment could not be read, counted in every window above and on no day here"
      });
    }
    if (spend.unpriced) {
      lines.push({
        day: "unpriced",
        title: "not priced",
        detail: count(spend.unpriced, "exchange record") + " could not be read, so every figure here and above is a floor",
        className: "grouping-day-unreached"
      });
    }
    // The heading counts lines rather than days, because the two at the foot
    // are not days and a count that called them days would be wrong by two.
    return listing(title, note, spend.problem, whatToDoAboutTheSpend(), "The spend log holds no days.", lines, "line");
  }

  // attentionGrouping lists what waits on a person: each entry of the
  // attention line, in the order the movers' vocabulary puts them — the
  // operator's first, so the list opens on what the tile's figure counted —
  // and within one mover in the order the terminal prints them, each by the
  // thing waiting, with its kind and who it is waiting on beside, and each opening
  // its card.
  function attentionGrouping(standing) {
    var entries = (standing.needs_human_problem ? [] : standing.needs_human).map(function (entry, at) { return { entry: entry, at: at }; });
    entries.sort(function (a, b) {
      return (moverRank(a.entry.mover) - moverRank(b.entry.mover)) || (a.at - b.at);
    });
    return listing("Needs a human", "what waits on a person, the operator's first, each with its kind and who it is waiting on; each opens its card",
      standing.needs_human_problem, whatToDoAboutTheStanding(), "Nothing waits on a person.",
      entries.map(function (each) {
        return { entry: entryKey(each.entry), kind: each.entry.kind, title: each.entry.what, detail: each.entry.whose };
      }), "thing");
  }

  // listing folds a grouping's readings into one of the four states: error
  // where its source could not be read, empty where the source was read and
  // holds nothing, and ready otherwise. The noun is what the heading counts
  // the items as: work items unless the listing says otherwise.
  function listing(title, note, problem, remedy, empty, items, noun) {
    if (problem) {
      return { title: title, note: note, state: "error", problem: problem, remedy: remedy };
    }
    if (items.length === 0) {
      return { title: title, note: note, state: "empty", empty: empty };
    }
    return { title: title, note: note, state: "ready", items: items, noun: noun || "item" };
  }

  function renderGrouping() {
    if (!openGrouping) {
      return;
    }
    var described = groupingOf(openGrouping);
    document.getElementById("grouping-heading").textContent = described.title + (described.state === "ready" ? " (" + count(described.items.length, described.noun) + ")" : "");
    document.getElementById("grouping-note").textContent = described.note || "";
    // What a grouping is waiting for is its own source's: the runs, the month
    // of spend, or where the harness stands.
    document.getElementById("grouping-waiting").textContent = described.waiting || "Reading…";
    section("grouping", described.state, described.state === "error" ? described.problem : described.empty, described.remedy);
    var list = document.getElementById("grouping-items");
    clear(list);
    (described.items || []).forEach(function (item) {
      var entry = el("li", "grouping-item" + (item.className ? " " + item.className : ""));
      // A row is a work item, opening its card by id, or an entry of the
      // attention line, opening its card by key with its kind where the id
      // would stand, or one day of the spend, which opens nothing.
      if (item.day) {
        entry.appendChild(el("span", "grouping-title grouping-day", item.title));
      } else if (item.entry) {
        entry.appendChild(entryOpener(item.entry, item.title, "grouping-title"));
        entry.appendChild(el("span", "item-id", item.kind));
      } else {
        entry.appendChild(itemOpener(item.id, item.title || item.id, "grouping-title"));
        entry.appendChild(el("span", "item-id", item.id));
      }
      if (item.detail) {
        entry.appendChild(el("span", "grouping-detail", item.detail));
      }
      list.appendChild(entry);
    });
  }

  function showGrouping(key, from) {
    openGrouping = key;
    openers.grouping = from || null;
    renderGrouping();
    open(groupingPopup);
    document.getElementById("grouping-close").focus();
  }

  function closeGrouping() {
    openGrouping = null;
    close(groupingPopup, "grouping");
  }

  // ---- the card

  // field is one labeled line of the card. Prose keeps its line breaks; a
  // field the item has nothing in says "none" in words, so a blank is never
  // mistaken for a field the page did not read.
  function field(label, value, className) {
    var row = el("div", "card-field" + (className ? " " + className : ""));
    row.appendChild(el("dt", null, label));
    var body = el("dd", value === "" || value === null || value === undefined ? "card-none" : null, value === "" || value === null || value === undefined ? "none" : value);
    row.appendChild(body);
    return row;
  }

  // runField is the run the harness last made for the item, in the words the
  // terminal lists a run in: in flight with its phase and spend, ended with
  // its outcome and what remains of its change, and the facts under it.
  function runField(item) {
    var row = el("div", "card-field card-field-run");
    row.appendChild(el("dt", null, "Run"));
    var body = el("dd");
    row.appendChild(body);
    if (item.run_problem) {
      body.appendChild(el("p", "problem", "Could not be read: " + item.run_problem));
      return row;
    }
    var run = item.run;
    if (!run) {
      body.appendChild(el("p", "card-none", "none is recorded"));
      return row;
    }
    var lines = [];
    var facts = [];
    if (run.in_flight) {
      lines.push("in flight — " + phaseOf(run) + ", " + age(run.elapsed) + " elapsed, " + spendOf(run));
      facts.push("run " + run.run_id + ", started " + dayAndClock(run.started_at));
    } else {
      var ending = run.outcome + (run.phase ? ", " + phaseOf(run) : "") + " — " + (run.remains || "no artifacts recorded");
      lines.push(run.preserved ? "preserved: " + ending : "nothing is in flight or preserved; the latest run " + ending);
      facts.push("run " + run.run_id + ", started " + dayAndClock(run.started_at) + (run.completed_at ? ", ended " + dayAndClock(run.completed_at) : ""));
      facts.push(run.unknown_cost ? "cost unknown (" + run.unknown_cost + ")" : "cost " + money(run.cost_usd || 0));
    }
    if (run.failure) {
      facts.push("reason: " + run.failure);
    }
    if (run.branch) {
      facts.push("branch: " + run.branch);
    }
    if (run.worktree_path) {
      facts.push("worktree: " + run.worktree_path);
    }
    if (run.provider_session_id) {
      facts.push("developer session: " + run.provider_session_id);
    }
    lines.forEach(function (text) { body.appendChild(el("p", run.in_flight ? "card-run-flight" : (run.preserved ? "card-run-preserved" : "card-run-ended"), text)); });
    var list = el("ul", "card-run-facts");
    facts.forEach(function (text) { list.appendChild(el("li", null, text)); });
    body.appendChild(list);
    return row;
  }

  function renderCard(item) {
    document.getElementById("card-heading").textContent = item.title || item.id;
    document.getElementById("card-note").textContent = item.id + " · read " + clock(item.observed_at);
    var fields = document.getElementById("card-fields");
    clear(fields);
    fields.appendChild(field("Id", item.id, "card-field-id"));
    fields.appendChild(field("Title", item.title));
    fields.appendChild(field("Status", item.status));
    fields.appendChild(field("Priority", item.priority === undefined || item.priority === null ? "" : "P" + item.priority + " (0 is the most urgent, 4 the least)"));
    fields.appendChild(field("Labels", (item.labels || []).join(", ")));
    fields.appendChild(field("Parent", item.parent, "card-field-id"));
    fields.appendChild(field("Description", item.description, "card-field-prose"));
    fields.appendChild(field("Design", item.design, "card-field-prose"));
    fields.appendChild(field("Acceptance criteria", item.acceptance_criteria, "card-field-prose"));
    fields.appendChild(field("Notes", item.notes, "card-field-prose"));
    fields.appendChild(runField(item));
    section("card", "ready");
  }

  // showCard opens the card on one item and asks for it. A 404 is the tracker
  // holding nothing under the id — an item closed or removed since the page
  // last read the standing — which is the card's empty state rather than a
  // failure; a 401 sends the page back to asking for the token, as every
  // reading does; anything else is the card's error state, with the reason.
  function showCard(id, from) {
    openCard = id;
    openEntry = null;
    openers.card = from || null;
    document.getElementById("card-heading").textContent = id;
    document.getElementById("card-note").textContent = "";
    clear(document.getElementById("card-fields"));
    section("card", "loading");
    open(cardPopup);
    document.getElementById("card-close").focus();
    var current = token();
    read("/api/items/" + encodeURIComponent(id), current, function (item) {
      if (openCard !== id) {
        return;
      }
      renderCard(item);
    }, function (reason, status) {
      if (openCard !== id) {
        return;
      }
      if (status === 404) {
        section("card", "empty", "No work item is recorded under " + id + ": it may have been closed or removed since the page last read where the work stands.");
        return;
      }
      section("card", "error", reason, "The card asks once, when it is opened; close it and open it again to ask again. yoyo status " + id + " says the same thing at the terminal, and bd show " + id + " prints the item itself.");
    });
  }

  function closeCard() {
    openCard = null;
    openEntry = null;
    close(cardPopup, "card");
  }

  // ---- the entry card

  // The card on one entry of the attention line. It is drawn from the standing
  // the page holds, so it costs nothing to open and is drawn again on every
  // poll: an entry the harness or a person has settled since it was opened is
  // gone from the standing, and the card says so rather than showing a record
  // that is no longer waiting on anyone. Every value is the record's own,
  // written as text; the proposed change and its reason are shown whole.

  // cardItemOpener is a work item's id on an entry card, opening the item's
  // card in this same pop-up. What opened the entry card stays what focus goes
  // back to, because the button being clicked is about to be redrawn away.
  function cardItemOpener(id) {
    var button = opener("data-item", id, el("button", "item-open item-id", id));
    button.addEventListener("click", function () { showCard(id, openers.card); });
    return button;
  }

  // entryFields lays the entry's record out under plain labels: first what is
  // waiting and who it is waiting on, in the terminal's words, then the record the
  // kind names, whole. A work item the entry is about is an opener on its id.
  function entryFields(entry) {
    var fields = document.getElementById("card-fields");
    clear(fields);
    var add = function (label, value, className) { fields.appendChild(field(label, value, className)); };
    var addItem = function (label, id) {
      var row = el("div", "card-field card-field-id");
      row.appendChild(el("dt", null, label));
      var body = el("dd");
      body.appendChild(cardItemOpener(id));
      row.appendChild(body);
      fields.appendChild(row);
    };
    add("What", entry.what, "card-field-prose");
    add("Waiting on", entry.whose, "card-field-prose");
    add("Kind", entry.kind);
    add("Mover", moverLabel(entry.mover));
    switch (entry.kind) {
      case "amendment":
        var proposal = entry.amendment;
        if (!proposal) {
          break;
        }
        add("Document", proposal.artifact, "card-field-id");
        add("Document kind", proposal.kind);
        add("Owner", proposal.owner);
        add("Proposed by", proposal.role + (proposal.agent ? " (agent " + proposal.agent + ")" : ""));
        add("In run", proposal.run_id, "card-field-id");
        if (proposal.work_item_id) {
          addItem("Working on", proposal.work_item_id);
        }
        add("Proposed change", proposal.change, "card-field-prose");
        add("Why", proposal.why, "card-field-prose");
        add("Raised", named(proposal.raised_at) ? dayAndClock(proposal.raised_at) : "");
        add("Id", proposal.id, "card-field-id");
        break;
      case "owed-step":
        add("Run", entry.id, "card-field-id");
        if (entry.work_item_id) {
          addItem("Work item", entry.work_item_id);
        }
        if (entry.owed_step) {
          add("Ended", entry.owed_step.status);
          add("Phase", entry.owed_step.phase || "none recorded");
        }
        break;
      case "conversation-carried-item":
        addItem("Work item", entry.work_item_id || entry.id);
        add("Executor", entry.executor, "card-field-id");
        add("Role", entry.mover === "unnamed-role" ? "none the harness recognizes from the marker" : entry.mover);
        break;
      case "publication":
        var publication = entry.publication;
        add("Run", entry.id, "card-field-id");
        if (entry.work_item_id) {
          addItem("Work item", entry.work_item_id);
        }
        if (!publication) {
          break;
        }
        add("Target branch", publication.target_branch || "not recorded", "card-field-id");
        add("Branch", publication.branch, "card-field-id");
        var request = publication.pull_request;
        add("Pull request", request ? "#" + request.number + " " + request.url + (request.state ? " (" + request.state + ")" : "") + (request.merge_queued ? ", merge queued" : "") : "none recorded");
        if (publication.merge_drop) {
          add("Merge dropped", dayAndClock(publication.merge_drop.at) + ": " + publication.merge_drop.reason, "card-field-prose");
        }
        break;
      case "degraded-service":
        var service = entry.service;
        if (!service) {
          break;
        }
        add("Service", service.service);
        add("State", service.state);
        add("Reason", service.reason, "card-field-prose");
        add("Died", named(service.died_at) ? dayAndClock(service.died_at) : "");
        add("Failures", service.failures === undefined ? "" : String(service.failures));
        add("Log", service.log, "card-field-id");
        break;
      case "failing-task":
        var failing = entry.failing_task;
        add("Task", entry.id);
        if (!failing) {
          break;
        }
        add("Role", failing.role);
        add("Cause", failing.cause);
        add("Failures in a row", String(failing.failures));
        add("First failed", dayAndClock(failing.first_at));
        add("Latest", dayAndClock(failing.latest_at));
        add("What stopped it", failing.problem, "card-field-prose");
        break;
      case "hold":
        var hold = entry.operator_hold || entry.intake_hold || entry.capacity_hold;
        add("Switch", entry.id);
        if (!hold) {
          break;
        }
        if (entry.capacity_hold) {
          add("Since", dayAndClock(hold.since));
          add("Resets", named(hold.resets_at) ? dayAndClock(hold.resets_at) : "no reset named");
          add("Refusals", count(hold.refusals || 0, "turn") + (hold.parked_runs ? ", " + count(hold.parked_runs, "run") + " parked" : ""));
          add("Agents", (hold.agents || []).join(", "));
          add("Models", (hold.models || []).join(", "));
          add("Alternates", (hold.alternates || []).join(", "));
        } else {
          add("Held since", dayAndClock(hold.held_at));
          if (entry.intake_hold) {
            add("Held by", hold.held_by);
            add("Reason", hold.reason, "card-field-prose");
          }
        }
        break;
      case "directive":
        var directive = entry.directive;
        if (!directive) {
          break;
        }
        add("Directive", directive.id, "card-field-id");
        add("Directive kind", directive.kind);
        add("Received", "by " + directive.received_by + " at " + dayAndClock(directive.received_at));
        add("Text", directive.text, "card-field-prose");
        add("Unresolved", directive.unresolved, "card-field-prose");
        add("Artifact", directive.artifact, "card-field-id");
        add("Scope", (directive.scope || []).join(", "), "card-field-id");
        break;
      case "outage":
        var outage = entry.outage;
        if (!outage) {
          break;
        }
        add("Cause", outage.cause);
        add("Provider", (outage.provider || "") + (outage.account_alias ? " (account " + outage.account_alias + ")" : ""));
        add("Since", dayAndClock(outage.since));
        add("Last seen", dayAndClock(outage.last_seen));
        add("Refusals", count(outage.refusals || 0, "turn"));
        add("Detail", outage.detail, "card-field-prose");
        add("Waiting", outage.waiting, "card-field-prose");
        break;
      case "stall":
        var stall = entry.stall;
        if (!stall) {
          break;
        }
        add("Reason", stall.reason);
        add("Says", stall.says, "card-field-prose");
        add("Clears", stall.clears, "card-field-prose");
        add("Since", named(stall.since) ? dayAndClock(stall.since) : "");
        add("Problem", stall.problem, "card-field-prose");
        break;
      case "report":
        var pile = entry.reports;
        if (!pile) {
          break;
        }
        add("Collected", String(pile.collected));
        add("Unhandled", String(pile.unhandled));
        add("Oldest", named(pile.oldest) ? dayAndClock(pile.oldest) + ", " + age(pile.oldest_age || 0) + " ago" : "");
        add("Worst", pile.worst);
        break;
      case "held-work":
        if (entry.held_work) {
          add("Awaiting", entry.held_work.awaiting === "carry-out" ? "carry-out of a decision already recorded" : "the development manager's decision");
          add("Items", count(entry.held_work.count, "admitted item"));
        }
        break;
      default:
        break;
    }
  }

  // renderEntry draws the open entry card from the standing in hand. Its empty
  // state is the entry no longer being on the line — settled since the card
  // was opened — and its error state is the line not being readable now.
  function renderEntry() {
    if (!openEntry) {
      return;
    }
    var standing = model.standing;
    var heading = document.getElementById("card-heading");
    var note = document.getElementById("card-note");
    if (!standing) {
      heading.textContent = "Needs a human";
      note.textContent = "";
      clear(document.getElementById("card-fields"));
      section("card", model.standingError ? "error" : "loading", model.standingError, whatToDoAboutTheStanding());
      return;
    }
    if (standing.needs_human_problem) {
      heading.textContent = "Needs a human";
      note.textContent = "";
      clear(document.getElementById("card-fields"));
      section("card", "error", standing.needs_human_problem, whatToDoAboutTheStanding());
      return;
    }
    var found = standing.needs_human.filter(function (entry) { return entryKey(entry) === openEntry; });
    if (found.length === 0) {
      heading.textContent = "Needs a human";
      note.textContent = openEntry;
      clear(document.getElementById("card-fields"));
      section("card", "empty", "Nothing under " + openEntry + " is waiting on a person any more: it was settled since the page last read where the harness stands, at " + clock(standing.observed_at) + ".");
      return;
    }
    var entry = found[0];
    heading.textContent = kindTitle(entry.kind);
    note.textContent = entryKey(entry) + " · read " + clock(standing.observed_at);
    entryFields(entry);
    section("card", "ready");
  }

  function showEntry(key, from) {
    openCard = null;
    openEntry = key;
    openers.card = from || null;
    renderEntry();
    open(cardPopup);
    document.getElementById("card-close").focus();
  }

  // ---- the lane report

  // The card on one program manager instance's current lane report, opened
  // from its row. It is read from /api/program-managers/<agent> when it is
  // opened and not before, and once: the instance as the standing carries it,
  // and the report's own text beside it — the summary, what remains, and the
  // blockers, each with who it waits on and what it cites, split into the ones
  // the record bears out and the ones it does not. Every value is written as
  // text.

  var reportPopup = document.getElementById("report");
  var openReport = null;

  function reportOpener(agent, text) {
    var button = opener("data-report", agent, el("button", "item-open manager-report", text));
    button.addEventListener("click", function () { showReport(agent, { element: button, kind: "data-report", key: agent }); });
    return button;
  }

  // citedRecords is what each kind of record a blocker's citation resolved to
  // is, in words: an open ask of the instance's own.
  var citedRecords = {
    "report": "a report of its own the Lead Product Manager has not handled",
    "amendment": "an amendment of its own nobody has decided",
    "exchange": "an exchange of its own still open",
    "restart-request": "a restart request of its own nothing has answered"
  };

  function listField(label, entries, none, className) {
    var row = el("div", "card-field" + (className ? " " + className : ""));
    row.appendChild(el("dt", null, label));
    var body = el("dd");
    if (entries.length === 0) {
      body.className = "card-none";
      body.textContent = none;
    } else {
      var list = el("ul", "card-run-facts");
      entries.forEach(function (text) { list.appendChild(el("li", null, text)); });
      body.appendChild(list);
    }
    row.appendChild(body);
    return row;
  }

  function renderReport(answer) {
    var instance = answer.instance;
    var text = answer.report;
    document.getElementById("report-heading").textContent = instance.agent;
    document.getElementById("report-note").textContent = (instance.lane ? "lane " + instance.lane : "no lane configured") + " · read " + clock(answer.observed_at);
    var fields = document.getElementById("report-fields");
    clear(fields);
    var status = el("div", "card-field");
    status.appendChild(el("dt", null, "Status"));
    var word = el("dd", "manager-line " + managerClass(instance.status));
    word.appendChild(managerBadge(instance.status));
    var why = managerWhy(instance);
    if (why) {
      word.appendChild(el("span", "manager-why", why));
    }
    status.appendChild(word);
    fields.appendChild(status);
    fields.appendChild(field("Lane", instance.lane));
    if (answer.problem) {
      var problemRow = el("div", "card-field");
      problemRow.appendChild(el("dt", null, "Could not be read"));
      problemRow.appendChild(el("dd", "problem", answer.problem));
      fields.appendChild(problemRow);
    }
    if (text) {
      fields.appendChild(field("Summary", text.summary, "card-field-prose"));
      fields.appendChild(listField("Remaining", text.remaining || [], "nothing remains, the report says"));
    } else {
      fields.appendChild(field("Summary", named(instance.report_written_at) ? "the report could not be read" : "it has written no report yet", "card-field-prose"));
    }
    fields.appendChild(listField("Blockers", (instance.blockers || []).map(function (blocker) {
      return blocker.what + " — " + moverLabel(blocker.waiting_on) + " move; cites " + blocker.cites + ", " + (citedRecords[blocker.record] || blocker.record);
    }), text ? "none the record bears out" : "none: there is no report to name one"));
    fields.appendChild(listField("Not blockers", (instance.claims || []).map(function (claim) {
      return claim.what + " — " + moverLabel(claim.waiting_on) + " move; cites " + claim.cites + ", and blocks nothing: " + claim.reason;
    }), text ? "none: the record bears out every blocker it names" : "none: there is no report to name one"));
    var written = "";
    if (text) {
      written = dayAndClock(text.written_at) + ", version " + text.version + ", by " + (text.pass ? "pass " + text.pass : "an operator's turn") + " in " + text.conversation_id + " turn " + text.turn;
    } else if (named(instance.report_written_at)) {
      written = dayAndClock(instance.report_written_at);
    }
    fields.appendChild(field("Written", written));
    fields.appendChild(field("Last pass", named(instance.last_completed_pass_at) ? "completed " + dayAndClock(instance.last_completed_pass_at) : "none has completed"));
    fields.appendChild(listField("Restart requests", (instance.restart_requests || []).map(function (request) {
      return request.part + ": " + request.reason + " — asked " + dayAndClock(request.requested_at) + ", unanswered; nothing acts on a request until the supervisor's periodic pass lands (" + request.id + ")";
    }), "none open"));
    fields.appendChild(field("Report file", instance.report_path, "card-field-id"));
    section("report", "ready");
  }

  // showReport opens the card on one instance's report and asks for it. A 404
  // is the read model knowing no instance by the name — one taken out of the
  // configuration since the page last read the standing — which is the card's
  // empty state; a 401 sends the page back to asking for the token; anything
  // else is the card's error state, with the reason.
  function showReport(agent, from) {
    openReport = agent;
    openers.report = from || null;
    document.getElementById("report-heading").textContent = agent;
    document.getElementById("report-note").textContent = "";
    clear(document.getElementById("report-fields"));
    section("report", "loading");
    open(reportPopup);
    document.getElementById("report-close").focus();
    read("/api/program-managers/" + encodeURIComponent(agent), token(), function (answer) {
      if (openReport !== agent) {
        return;
      }
      renderReport(answer);
    }, function (reason, status) {
      if (openReport !== agent) {
        return;
      }
      if (status === 404) {
        section("report", "empty", "No program manager instance is recorded under " + agent + ": it may have been taken out of the configuration since the page last read where the harness stands.");
        return;
      }
      section("report", "error", reason, "The card asks once, when it is opened; close it and open it again to ask again. yoyo status --json carries the same instance under standing.program_managers, and its report is the file the row names.");
    });
  }

  function closeReport() {
    openReport = null;
    close(reportPopup, "report");
  }

  document.getElementById("grouping-close").addEventListener("click", closeGrouping);
  document.getElementById("grouping-backdrop").addEventListener("click", closeGrouping);
  document.getElementById("card-close").addEventListener("click", closeCard);
  document.getElementById("card-backdrop").addEventListener("click", closeCard);
  document.getElementById("report-close").addEventListener("click", closeReport);
  document.getElementById("report-backdrop").addEventListener("click", closeReport);
  // Escape closes the pop-up on top: a lane report where one is open, else the
  // card where one is open, on an item or an entry, else the grouping.
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") {
      return;
    }
    if (openReport !== null) {
      closeReport();
    } else if (openCard !== null || openEntry !== null) {
      closeCard();
    } else if (openGrouping !== null) {
      closeGrouping();
    }
  });

  // ---- what to do -----------------------------------------------------------

  function whatToDoAboutTheStanding() {
    return "The dashboard asks again every ten seconds. If this stays, yoyo status in the checkout says the same thing with more room, and yoyo doctor says what cannot be read.";
  }

  function whatToDoAboutTheQueue() {
    return "The admitted work is read from the tracker and the runs from the state root; yoyo doctor says whether bd answers in this checkout and what cannot be read, and yoyo status prints the same refusals.";
  }

  function whatToDoAboutTheThroughput() {
    return "The dashboard asks again every minute. yoyo status reads the same run records at the terminal and names what could not be read.";
  }

  function whatToDoAboutTheSpend() {
    return "The dashboard asks again every minute. yoyo status --spend 30 prices the same records at the terminal and names what could not be read.";
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
    renderSpend();
    renderLive();
    renderPipeline();
    renderThroughput();
    renderCapacity();
    renderManagers();
    // An open grouping, and an open entry card, are drawn again from the
    // reading just taken, so each stays as live as what it was opened from.
    renderGrouping();
    renderEntry();
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
  // reason there is none, with the status where there was one. A 401 is the
  // token being wrong — a mistyped one and a restarted dashboard look the same
  // — and sends the page back to asking for it rather than being reported as a
  // failure of the read model.
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
            onFailure((body && body.error) || (response.status + " " + response.statusText), response.status);
            return null;
          }
          return body;
        }, function () {
          onFailure(response.status + " " + response.statusText + ", and the answer was not JSON", response.status);
          return null;
        });
      })
      .then(function (body) {
        if (body) {
          onBody(body);
        }
      })
      .catch(function (error) {
        onFailure("the dashboard could not be reached (" + error.message + "); the yoyo dashboard process may have stopped, and starting it again prints a new token unless services.dashboard.token names a stored one");
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

  function refreshSpend(current) {
    read("/api/spend", current, function (spend) {
      model.spend = spend;
      model.spendError = "";
      render();
    }, function (reason) {
      model.spendError = reason;
      render();
    });
  }

  function start(current) {
    model = { standing: null, standingError: "", throughput: null, throughputError: "", spend: null, spendError: "" };
    // A page starting over — a token just entered — opens with nothing over it.
    openGrouping = null;
    openCard = null;
    openEntry = null;
    openReport = null;
    openers = { grouping: null, card: null, report: null };
    openersByKey = {};
    setHidden(groupingPopup, true);
    setHidden(cardPopup, true);
    setHidden(reportPopup, true);
    render();
    refreshStanding(current);
    refreshThroughput(current);
    refreshSpend(current);
    timers.push(window.setInterval(function () { refreshStanding(current); }, pollStanding));
    timers.push(window.setInterval(function () { refreshThroughput(current); }, pollThroughput));
    timers.push(window.setInterval(function () { refreshSpend(current); }, pollSpend));
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
