#!/usr/bin/env node
// Renders the dashboard page from the fixtures, without a browser, and writes
// what each scenario leaves in the DOM.
//
// The page's script is the one thing that turns the read model into the five
// sections, and a Go test cannot run it. This runs it under Node against a small
// document model — enough of the DOM for the script's own needs and nothing
// more — with fetch answered from the fixtures under ./fixtures, and serializes
// the page each scenario ends in. TestThePageRendersEverySectionInEveryState in
// page_test.go runs this and holds the output to the renders under ./renders,
// which are the evidence a reviewer is handed for each section in each state;
// -update-renders rewrites them.
//
// The document model is deliberately small. The script uses getElementById,
// createElement, textContent, appendChild, removeChild, firstChild, className,
// setAttribute, removeAttribute, addEventListener (on an element and on the
// document), focus, and value, and this implements those and no more, so a
// new DOM call in the script fails here loudly rather than passing on a shim
// that quietly did nothing. It is not a browser: layout, style, and the policy
// are checked elsewhere, and what this checks is that the right words land in
// the right places.
//
// A scenario can also act on the page once it is drawn: open a grouping or an
// item's card by clicking the element that carries its key, and press Escape.
// That is how the pop-ups reach each of their states, from the same fixtures.
//
// Usage: node render.js --out <directory>
// Writes <directory>/<scenario>.html for every scenario below — the document
// as the page's script left it, with the one page state and the one state per
// panel the stylesheet would show, the hidden ones dropped, and a pop-up kept
// only while it is open — and a <directory>/matrix.json saying which state
// each section and each pop-up reached in each.

"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");

const here = __dirname;
const assets = path.join(here, "..", "assets");

// ---- a small document model ---------------------------------------------------

const voidTags = new Set(["meta", "link", "input", "br", "hr", "img"]);

function escapeText(text) {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttribute(text) {
  return escapeText(text).replace(/"/g, "&quot;");
}

class Node {
  constructor(document) {
    this.ownerDocument = document;
    this.parentNode = null;
    this.childNodes = [];
  }
  get firstChild() {
    return this.childNodes.length ? this.childNodes[0] : null;
  }
  appendChild(child) {
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    child.parentNode = this;
    this.childNodes.push(child);
    return child;
  }
  removeChild(child) {
    const at = this.childNodes.indexOf(child);
    if (at === -1) {
      throw new Error("removeChild: not a child");
    }
    this.childNodes.splice(at, 1);
    child.parentNode = null;
    return child;
  }
}

class Text extends Node {
  constructor(document, data) {
    super(document);
    this.data = data;
  }
  get textContent() {
    return this.data;
  }
  serialize() {
    return escapeText(this.data);
  }
}

class Element extends Node {
  constructor(document, tagName) {
    super(document);
    this.tagName = tagName.toLowerCase();
    this.attributes = new Map();
    this.listeners = {};
    this.value = "";
    this.focused = false;
  }
  get id() {
    return this.attributes.get("id") || "";
  }
  get className() {
    return this.attributes.get("class") || "";
  }
  set className(value) {
    this.attributes.set("class", String(value));
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === "id") {
      this.ownerDocument.index();
    }
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  removeAttribute(name) {
    this.attributes.delete(name);
  }
  get textContent() {
    return this.childNodes.map((child) => child.textContent).join("");
  }
  set textContent(value) {
    this.childNodes.forEach((child) => { child.parentNode = null; });
    this.childNodes = [];
    if (value !== "") {
      this.appendChild(new Text(this.ownerDocument, String(value)));
    }
  }
  addEventListener(name, listener) {
    (this.listeners[name] = this.listeners[name] || []).push(listener);
  }
  dispatch(name, event) {
    (this.listeners[name] || []).forEach((listener) => listener(event));
  }
  focus() {
    this.focused = true;
  }
  serialize(indent) {
    const pad = indent || "";
    const attributes = [...this.attributes.entries()]
      .map(([name, value]) => (value === "" ? ` ${name}` : ` ${name}="${escapeAttribute(value)}"`))
      .join("");
    if (voidTags.has(this.tagName)) {
      return `${pad}<${this.tagName}${attributes}>`;
    }
    const inline = this.childNodes.every((child) => child instanceof Text);
    if (inline) {
      return `${pad}<${this.tagName}${attributes}>${this.childNodes.map((child) => child.serialize()).join("")}</${this.tagName}>`;
    }
    const inner = this.childNodes
      .map((child) => (child instanceof Text ? (child.data.trim() ? pad + "  " + escapeText(child.data.trim()) : null) : child.serialize(pad + "  ")))
      .filter((line) => line !== null)
      .join("\n");
    return `${pad}<${this.tagName}${attributes}>\n${inner}\n${pad}</${this.tagName}>`;
  }
}

class Document {
  constructor() {
    this.root = null;
    this.byId = new Map();
    this.listeners = {};
  }
  createElement(tagName) {
    return new Element(this, tagName);
  }
  addEventListener(name, listener) {
    (this.listeners[name] = this.listeners[name] || []).push(listener);
  }
  dispatch(name, event) {
    (this.listeners[name] || []).forEach((listener) => listener(event));
  }
  index() {
    this.byId = new Map();
    const walk = (node) => {
      if (node instanceof Element) {
        if (node.id) {
          this.byId.set(node.id, node);
        }
        node.childNodes.forEach(walk);
      }
    };
    if (this.root) {
      walk(this.root);
    }
  }
  getElementById(id) {
    this.index();
    return this.byId.get(id) || null;
  }
  find(predicate) {
    const found = [];
    const walk = (node) => {
      if (node instanceof Element) {
        if (predicate(node)) {
          found.push(node);
        }
        node.childNodes.forEach(walk);
      }
    };
    walk(this.root);
    return found;
  }
}

// parse is enough of an HTML parser for the shell: tags, attributes, text, the
// void elements, and comments, with no error recovery. The shell is ours and is
// well formed; anything this cannot parse is a mistake in the shell.
function parse(html) {
  const document = new Document();
  const root = new Element(document, "#document");
  document.root = root;
  const stack = [root];
  const tag = /<!--[\s\S]*?-->|<!doctype[^>]*>|<\/([a-zA-Z][\w-]*)\s*>|<([a-zA-Z][\w-]*)((?:\s+[\w-]+(?:="[^"]*")?)*)\s*(\/?)>/g;
  let last = 0;
  let match;
  while ((match = tag.exec(html)) !== null) {
    const text = html.slice(last, match.index);
    if (text) {
      stack[stack.length - 1].appendChild(new Text(document, text));
    }
    last = tag.lastIndex;
    const [whole, closing, opening, attributes, selfClosing] = match;
    if (whole.startsWith("<!")) {
      continue;
    }
    if (closing) {
      const top = stack.pop();
      if (top.tagName !== closing.toLowerCase()) {
        throw new Error(`unbalanced </${closing}> against <${top.tagName}>`);
      }
      continue;
    }
    const element = new Element(document, opening);
    const attribute = /([\w-]+)(?:="([^"]*)")?/g;
    let pair;
    while ((pair = attribute.exec(attributes || "")) !== null) {
      element.attributes.set(pair[1], pair[2] === undefined ? "" : pair[2].replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&"));
    }
    stack[stack.length - 1].appendChild(element);
    if (!voidTags.has(element.tagName) && !selfClosing) {
      stack.push(element);
    }
  }
  if (stack.length !== 1) {
    throw new Error(`unclosed <${stack[stack.length - 1].tagName}>`);
  }
  document.index();
  return document;
}

// ---- the scenarios ----------------------------------------------------------------

function fixture(name) {
  return JSON.parse(fs.readFileSync(path.join(here, "fixtures", name + ".json"), "utf8"));
}

// An answer is what fetch resolves with for one path: a body and a status, or
// "pending" for a fetch that never answers, or "unreachable" for one that rejects.
function ok(body) {
  return { status: 200, ok: true, body };
}

function refused(status, error) {
  return { status, ok: false, body: { error }, statusText: status === 503 ? "Service Unavailable" : "Unauthorized" };
}

const pending = { pending: true };
const unreachable = { unreachable: true };

// items answers /api/items/<id> for the ids named, from the item fixtures.
function items(...ids) {
  const answers = {};
  ids.forEach((id) => { answers["/api/items/" + id] = ok(fixture("item-" + id)); });
  return answers;
}

// A scenario's `open` is what a reader clicks once the page is drawn, in
// order: a grouping by its key, or an item by its id; `escape` presses Escape
// afterwards. The pop-ups answer from the standing and the throughput already
// in hand, and the card from the item answers.
const scenarios = [
  { name: "signin", token: "", standing: pending, throughput: pending },
  { name: "loading", token: "t", standing: pending, throughput: pending },
  { name: "quiet", token: "t", standing: ok(fixture("standing-quiet")), throughput: ok(fixture("throughput-quiet")) },
  { name: "busy", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")) },
  { name: "held", token: "t", standing: ok(fixture("standing-held")), throughput: ok(fixture("throughput-busy")) },
  { name: "degraded", token: "t", standing: ok(fixture("standing-degraded")), throughput: ok(fixture("throughput-degraded")) },
  { name: "unreadable", token: "t", standing: ok(fixture("standing-unreadable")), throughput: ok(fixture("throughput-unreadable")) },
  { name: "throughput-pending", token: "t", standing: ok(fixture("standing-busy")), throughput: pending },
  { name: "throughput-refused", token: "t", standing: ok(fixture("standing-busy")), throughput: refused(503, "the state root could not be resolved") },
  { name: "refused", token: "t", standing: refused(503, "the state root could not be resolved: open /Users/somebody/Library/Application Support/Yoyodyne/state: permission denied"), throughput: pending },
  { name: "unreachable", token: "t", standing: unreachable, throughput: unreachable },
  { name: "wrong-token", token: "t", standing: refused(401, "this dashboard requires the token it printed when it started, as a bearer token"), throughput: pending },
  // The second poll fails after a first that succeeded: the page keeps what it
  // had and says it is stale.
  { name: "stale", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), then: { "/api/standing": unreachable } },
  { name: "throughput-stale", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), then: { "/api/throughput": refused(503, "the state root could not be resolved") } },
  // The card, opened from Running now: an item in flight, read whole; one still
  // being read; one the tracker holds nothing under; one that could not be read.
  { name: "card", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: items("yoyodyne-ifd.141.3"), open: [{ item: "yoyodyne-ifd.141.3" }] },
  { name: "card-loading", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: { "/api/items/yoyodyne-ifd.201": pending }, open: [{ item: "yoyodyne-ifd.201" }] },
  { name: "card-missing", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: { "/api/items/yoyodyne-ifd.212": refused(404, "no work item is recorded under that id") }, open: [{ item: "yoyodyne-ifd.212" }] },
  { name: "card-refused", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: { "/api/items/yoyodyne-ifd.230": refused(503, "the work item could not be read: bd show failed with status failed and exit code 1: failed to open database: embeddeddolt: openat LOCK: operation not permitted") }, open: [{ item: "yoyodyne-ifd.230" }] },
  // The grouping pop-up: a pile listed by title; a stage with nothing in it; a
  // stage whose source could not be read; the landed stage still being priced;
  // and the card opened from a grouping's entry, over it — a stopped run with
  // its change preserved — and then both closed with Escape, twice.
  { name: "grouping", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), open: [{ grouping: "held" }] },
  { name: "grouping-pile", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), open: [{ grouping: "pile:held" }] },
  { name: "grouping-landed", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), open: [{ grouping: "landed:week" }] },
  { name: "grouping-empty", token: "t", standing: ok(fixture("standing-held")), throughput: ok(fixture("throughput-busy")), open: [{ grouping: "startable" }] },
  { name: "grouping-error", token: "t", standing: ok(fixture("standing-degraded")), throughput: ok(fixture("throughput-degraded")), open: [{ grouping: "admitted" }] },
  { name: "grouping-loading", token: "t", standing: ok(fixture("standing-busy")), throughput: pending, open: [{ grouping: "landed:today" }] },
  { name: "grouping-card", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: items("yoyodyne-ifd.153"), open: [{ grouping: "pile:held" }, { item: "yoyodyne-ifd.153" }] },
  { name: "closed", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), items: items("yoyodyne-ifd.153"), open: [{ grouping: "pile:held" }, { item: "yoyodyne-ifd.153" }], escape: 2 }
];

function settle() {
  return new Promise((resolve) => setImmediate(() => setImmediate(resolve)));
}

async function run(scenario) {
  const shell = fs.readFileSync(path.join(assets, "shell.html"), "utf8").replace(/\{\{\.Product\}\}/g, "yoyodyne");
  const document = parse(shell);
  const script = fs.readFileSync(path.join(assets, "dashboard.js"), "utf8");

  const storage = new Map();
  if (scenario.token) {
    storage.set("yoyo-dashboard-token", scenario.token);
  }
  const intervals = [];
  let answers = Object.assign({ "/api/standing": scenario.standing, "/api/throughput": scenario.throughput }, scenario.items || {});
  const requests = [];

  const fetch = (url, options) => {
    requests.push({ url, authorization: options && options.headers && options.headers.Authorization });
    const answer = answers[url];
    if (!answer) {
      return Promise.reject(new Error("no fixture answers " + url));
    }
    if (answer.pending) {
      return new Promise(() => {});
    }
    if (answer.unreachable) {
      return Promise.reject(new TypeError("Failed to fetch"));
    }
    return Promise.resolve({
      status: answer.status,
      ok: answer.ok,
      statusText: answer.statusText || "OK",
      json: () => Promise.resolve(JSON.parse(JSON.stringify(answer.body)))
    });
  };

  const window = {
    sessionStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, String(value)),
      removeItem: (key) => storage.delete(key)
    },
    setInterval: (callback) => intervals.push(callback) && intervals.length,
    clearInterval: (id) => { intervals[id - 1] = null; }
  };
  const context = vm.createContext({ document, window, fetch, console });
  new vm.Script(script, { filename: "dashboard.js" }).runInContext(context);
  await settle();

  if (scenario.then) {
    answers = Object.assign({}, answers, scenario.then);
    intervals.filter(Boolean).forEach((callback) => callback());
    await settle();
  }

  // What a reader clicks: the one element carrying the key, found the way a
  // reader finds it — by what it opens, not where it is. An opener that is
  // not on the page is a failure of the page, and is said so.
  const openerFor = (attribute, value) => {
    const found = document.find((element) => element.tagName === "button" && element.getAttribute(attribute) === value);
    if (found.length === 0) {
      throw new Error(`${scenario.name}: nothing on the page opens ${attribute}=${value}`);
    }
    return found[0];
  };
  const opened = [];
  for (const step of scenario.open || []) {
    const opener = step.item ? openerFor("data-item", step.item) : openerFor("data-grouping", step.grouping);
    opened.push(opener);
    opener.dispatch("click", {});
    await settle();
  }
  for (let presses = scenario.escape || 0; presses > 0; presses -= 1) {
    document.dispatch("keydown", { key: "Escape" });
    await settle();
  }
  // A pop-up closed with Escape gives focus back to what opened it.
  if (scenario.escape && opened.length > 0 && !opened[0].focused) {
    throw new Error(`${scenario.name}: focus did not return to the opener after Escape`);
  }

  const page = document.getElementById("page");
  const sections = ["band", "live", "pipeline", "throughput", "capacity"];
  const popups = ["grouping", "card"];
  const matrix = { page: page.getAttribute("data-state"), sections: {}, popups: {} };
  sections.forEach((id) => {
    matrix.sections[id] = document.getElementById(id).getAttribute("data-state");
  });
  // A pop-up that is hidden is closed, whatever state it was last drawn in.
  popups.forEach((id) => {
    const popup = document.getElementById(id);
    matrix.popups[id] = popup.getAttribute("hidden") !== null ? "closed" : popup.getAttribute("data-state");
  });
  // No token ever left the page except as a bearer to this origin.
  requests.forEach((request) => {
    if (!request.url.startsWith("/api/") || (scenario.token && request.authorization !== "Bearer " + scenario.token)) {
      throw new Error(`${scenario.name}: a request went to ${request.url} with ${request.authorization}`);
    }
  });
  // Nothing set an inline style or wrote markup: every element's attributes
  // are the shell's or a class, data-state, hidden, datetime, an id, or the
  // key of what an opener opens.
  document.find(() => true).forEach((element) => {
    for (const name of element.attributes.keys()) {
      if (name === "style" || name.startsWith("on")) {
        throw new Error(`${scenario.name}: <${element.tagName}> carries ${name}, which the policy refuses`);
      }
    }
  });

  // What is written is what a viewer sees: the page's states and each panel's
  // states are all in the document, and the stylesheet shows exactly one of
  // each, so the render keeps the one that is shown and drops the rest. A
  // render that carried every hidden state would say the same thing at three
  // times the length, and be read by nobody.
  const classes = (element) => element.className.split(/\s+/).filter(Boolean);
  const prune = (parent, shown, prefix) => {
    parent.childNodes.slice().forEach((child) => {
      if (!(child instanceof Element)) {
        return;
      }
      const states = classes(child).filter((name) => name.startsWith(prefix));
      if (states.length > 0 && !states.includes(prefix + shown)) {
        parent.removeChild(child);
      }
    });
  };
  sections.forEach((id) => {
    prune(document.getElementById(id), matrix.sections[id], "section-");
  });
  // A closed pop-up is dropped from the render, and an open one keeps the one
  // state it shows, which is under its card rather than at its root.
  popups.forEach((id) => {
    const popup = document.getElementById(id);
    if (matrix.popups[id] === "closed") {
      popup.parentNode.removeChild(popup);
      return;
    }
    popup.childNodes.forEach((child) => {
      if (child instanceof Element && classes(child).includes("popup-card")) {
        prune(child, matrix.popups[id], "section-");
      }
    });
  });
  prune(page, matrix.page, "state-");

  const html = document.root.childNodes
    .map((child) => (child instanceof Text ? child.data.trim() : child.serialize("")))
    .filter((line) => line !== "")
    .join("\n")
    .replace('href="/assets/dashboard.css"', 'href="../../assets/dashboard.css"')
    .replace('src="/assets/dashboard.js"', 'src="about:blank" data-note="the script ran once to produce this render; it is not loaded again here"');
  return { matrix, html: "<!doctype html>\n" + html + "\n" };
}

async function main() {
  const at = process.argv.indexOf("--out");
  if (at === -1 || !process.argv[at + 1]) {
    console.error("usage: node render.js --out <directory>");
    process.exit(2);
  }
  const out = process.argv[at + 1];
  fs.mkdirSync(out, { recursive: true });
  const matrix = {};
  for (const scenario of scenarios) {
    const rendered = await run(scenario);
    fs.writeFileSync(path.join(out, scenario.name + ".html"), rendered.html);
    matrix[scenario.name] = rendered.matrix;
  }
  fs.writeFileSync(path.join(out, "matrix.json"), JSON.stringify(matrix, null, 2) + "\n");
}

main().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
