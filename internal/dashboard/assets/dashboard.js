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

  function render(standing) {
    observedAt.textContent = standing.observed_at || "—";
    observedAt.setAttribute("datetime", standing.observed_at || "");
    var parts = [
      count((standing.running || []).length, "developer run"),
      count((standing.working || []).length, "conversation") + " with a turn in flight",
      count((standing.not_startable || []).length, "admitted item") + " nothing will pull",
      count((standing.needs_human || []).length, "thing") + " waiting on a person"
    ];
    summary.textContent = parts.join(" · ");
    // A line whose source could not be read says so beside the counts rather
    // than being counted as empty; the model's honesty is the page's.
    while (problems.firstChild) {
      problems.removeChild(problems.firstChild);
    }
    ["running_problem", "working_problem", "not_startable_problem", "needs_human_problem"].forEach(function (key) {
      if (standing[key]) {
        var item = document.createElement("li");
        item.textContent = standing[key];
        problems.appendChild(item);
      }
    });
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
