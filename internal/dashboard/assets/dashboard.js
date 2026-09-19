// The shell's own script: hold the token, fetch the read model with it, and
// move the page between its states on what comes back. It is served from this
// origin because the policy allows script from nowhere else.
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
// tag is shown as the characters it is.
(function () {
  "use strict";

  var pollEvery = 10000;
  var storageKey = "yoyo-dashboard-token";
  var main = document.querySelector("main");
  var observedAt = document.getElementById("observed-at");
  var problem = document.getElementById("problem");
  var summary = document.getElementById("summary");
  var problems = document.getElementById("problems");
  var signin = document.getElementById("signin");
  var signinNote = document.getElementById("signin-note");
  var tokenField = document.getElementById("token");
  var timer = null;

  function show(state) {
    main.dataset.state = state;
  }

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

  function askForToken(note) {
    if (timer !== null) {
      window.clearInterval(timer);
      timer = null;
    }
    signinNote.textContent = note || "";
    tokenField.value = "";
    show("signin");
    tokenField.focus();
  }

  function count(number, noun) {
    return number + " " + noun + (number === 1 ? "" : "s");
  }

  // lines is the four lines as the summary counts them: the list each one
  // counts, the problem field that replaces the count when the list could not
  // be read, and the words the count is said in.
  var lines = [
    { list: "running", problem: "running_problem", noun: "developer run", suffix: "" },
    { list: "working", problem: "working_problem", noun: "conversation", suffix: " with a turn in flight" },
    { list: "not_startable", problem: "not_startable_problem", noun: "admitted item", suffix: " nothing will pull" },
    { list: "needs_human", problem: "needs_human_problem", noun: "thing", suffix: " waiting on a person" }
  ];

  function render(standing) {
    observedAt.textContent = standing.observed_at || "—";
    observedAt.setAttribute("datetime", standing.observed_at || "");
    // A line whose source could not be read is not counted, because a zero
    // assembled from nothing reads as an empty line. It is said as unreadable
    // in the count's place, exactly as the terminal says it, and the reason is
    // listed under the counts; the model's honesty is the page's.
    var parts = [];
    while (problems.firstChild) {
      problems.removeChild(problems.firstChild);
    }
    lines.forEach(function (line) {
      if (standing[line.problem]) {
        parts.push(line.noun + "s could not be read");
        var item = document.createElement("li");
        item.textContent = standing[line.problem];
        problems.appendChild(item);
        return;
      }
      parts.push(count((standing[line.list] || []).length, line.noun) + line.suffix);
    });
    summary.textContent = parts.join(" · ");
    show("ready");
  }

  function failed(message) {
    problem.textContent = message;
    show("error");
  }

  function refresh(current) {
    fetch("/api/standing", {
      headers: { Accept: "application/json", Authorization: "Bearer " + current },
      cache: "no-store"
    })
      .then(function (response) {
        if (response.status === 401) {
          // The token this tab holds is not the one the server has, which is
          // what a mistyped token and a restarted dashboard both look like.
          forget();
          askForToken("that is not the token this dashboard printed when it started");
          return null;
        }
        return response.json().then(function (body) {
          if (!response.ok) {
            failed((body && body.error) || (response.status + " " + response.statusText));
            return null;
          }
          return body;
        });
      })
      .then(function (standing) {
        if (standing) {
          render(standing);
        }
      })
      .catch(function (error) {
        failed("the dashboard could not be reached: " + error.message);
      });
  }

  function start(current) {
    show("loading");
    refresh(current);
    timer = window.setInterval(function () {
      refresh(current);
    }, pollEvery);
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
