package report

import (
	"context"
	"fmt"
	"strings"
)

// Builds answers how far the target branch has moved past one harness build:
// how many changes it holds that the build did not. It is the question a reader
// has to ask of a report before admitting work from it, because a report is a
// claim about the build that filed it, and a defect a report describes may have
// been fixed on the main line after that build and before anybody read it.
//
// The answer is Git's, taken where the repository is in hand, so the pile can be
// read without one: a listing given nothing to ask still names each report's
// build, and only the count is missing.
type Builds interface {
	Behind(ctx context.Context, build string) (int, error)
}

// Lag is what one build was found to be against the target branch: how many
// changes behind its tip, or why that could not be counted.
type Lag struct {
	Behind  int    `json:"behind"`
	Problem string `json:"problem,omitempty"`
}

// maxLagProblemBytes keeps why a build could not be counted to one readable
// line of a listing.
const maxLagProblemBytes = 240

// Gauge measures builds against the target branch for one listing, asking about
// each distinct build once however many reports share it. A pile is mostly
// reports from a handful of builds, and a listing is read in one sitting, so the
// answers are kept for exactly as long as the listing is being written.
//
// A nil gauge measures nothing, which is what a listing with no repository to
// ask gets: every report still names its build, and none says how far behind it
// is, rather than one saying it is current.
type Gauge struct {
	ctx    context.Context
	builds Builds
	lags   map[string]Lag
}

// NewGauge asks builds about each build a listing renders. A nil Builds is a
// nil gauge.
func NewGauge(ctx context.Context, builds Builds) *Gauge {
	if builds == nil {
		return nil
	}
	return &Gauge{ctx: ctx, builds: builds, lags: map[string]Lag{}}
}

// Lag is how far one build is behind the target branch, and whether anything
// was asked at all. A build nobody recorded is never asked about: there is no
// comparison to make, and saying so is the report's to do rather than Git's.
func (g *Gauge) Lag(build string) (Lag, bool) {
	if g == nil || build == "" {
		return Lag{}, false
	}
	if lag, seen := g.lags[build]; seen {
		return lag, true
	}
	var lag Lag
	behind, err := g.builds.Behind(g.ctx, build)
	if err != nil {
		lag.Problem = strings.Join(strings.Fields(err.Error()), " ")
		if len(lag.Problem) > maxLagProblemBytes {
			lag.Problem = lag.Problem[:maxLagProblemBytes] + "…"
		}
	} else {
		lag.Behind = behind
	}
	g.lags[build] = lag
	return lag, true
}

// Measure asks about every build a set of reports carries and returns what was
// found, keyed by build. It is for a listing that carries the answers as data
// rather than as prose — a script reading the pile needs the count beside the
// build, not a sentence about it.
func (g *Gauge) Measure(reports []Report) map[string]Lag {
	if g == nil {
		return nil
	}
	for _, reported := range reports {
		g.Lag(reported.Build)
	}
	measured := make(map[string]Lag, len(g.lags))
	for build, lag := range g.lags {
		measured[build] = lag
	}
	return measured
}

// Problem says, once for a whole listing, that some builds could not be counted
// and why the first of them could not. Each report so affected says only that
// its build was not counted, because the reason is nearly always one reason — a
// repository that does not hold the harness's history, or a Git that would not
// answer — and printing it under every report would bury the reports.
func (g *Gauge) Problem() string {
	if g == nil {
		return ""
	}
	var first string
	failed := 0
	for _, lag := range g.lags {
		if lag.Problem == "" {
			continue
		}
		failed++
		if first == "" || lag.Problem < first {
			first = lag.Problem
		}
	}
	if failed == 0 {
		return ""
	}
	return fmt.Sprintf("%d build(s) could not be counted against the target branch, so those reports do not say whether a fix has landed since: %s", failed, first)
}

// provenance names the invocation a report came out of and the build it
// executed, and — where the gauge could count it — how far behind the target
// branch's tip that build is. The count is what lets a reader check whether a
// fix has already landed before admitting work from the report; a report with
// no build says so rather than being read as current.
func (r Report) provenance(gauge *Gauge) string {
	if r.Build == "" {
		return r.RunID + ", no build recorded"
	}
	described := r.RunID + ", build " + shortBuild(r.Build)
	lag, asked := gauge.Lag(r.Build)
	switch {
	case !asked:
		return described
	case lag.Problem != "":
		return described + ", not counted against the target branch"
	case lag.Behind == 0:
		return described + ", the target branch's tip"
	default:
		return fmt.Sprintf("%s, %d change(s) behind the target branch", described, lag.Behind)
	}
}

// shortBuild is the length a revision is printed at everywhere else the harness
// names a build, which is enough to hand back to Git.
func shortBuild(build string) string {
	if len(build) > 12 {
		return build[:12]
	}
	return build
}
