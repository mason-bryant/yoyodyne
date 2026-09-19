// The shell's own script: fetch the read model, and move the page between its
// states on what comes back. It is served from this origin because the policy
// allows script from nowhere else, and it holds no token: the cookie the sign-in
// set rides along with every same-origin fetch, kept from this script by
// HttpOnly.
//
// Nothing the read model says is written into the page as markup. Every value
// goes in through textContent, so a work-item title that happens to contain a
// tag is shown as the characters it is.
(function () {
  "use strict";

  var pollEvery = 10000;
  var main = document.querySelector("main");
  var observedAt = document.getElementById("observed-at");
  var problem = document.getElementById("problem");
  var summary = document.getElementById("summary");
  var problems = document.getElementById("problems");

  function show(state) {
    main.dataset.state = state;
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

  function refresh() {
    fetch("/api/standing", { headers: { Accept: "application/json" }, cache: "no-store" })
      .then(function (response) {
        if (response.status === 401) {
          // The token this browser holds is no longer the one the server has,
          // which is what a restarted dashboard looks like. The page asks for
          // it again rather than polling a refusal.
          window.location.reload();
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

  refresh();
  window.setInterval(refresh, pollEvery);
})();
