package runstate

import (
	"slices"
	"strings"
)

// StopClass names which gate stopped a run, as the pipeline knew it at the
// moment it stopped. It is the one field a reader can answer "what stopped this"
// from without inferring it, and inferring it is what went wrong before it
// existed: a check failure, a refused path, and a reviewer's findings are all
// evidence a run carries while it is still repairing, so every one of them can be
// on the record of a run the provider killed, and a reader who took the first of
// them it found as the reason read that run as a failed change.
//
// It is written where the run is stopped and by nothing that reads the record
// afterwards. A record written before the field existed carries none, and a
// reader says it names none rather than guessing one.
type StopClass string

const (
	// StopChecks is the checking gate: a configured check that kept failing or
	// could not run, a protected path the change kept touching, or a change nobody
	// recorded running anything against.
	StopChecks StopClass = "checks"
	// StopReview is the independent review: findings the repair budget could not
	// resolve, a reviewer that could not answer the verdict contract, or an
	// approval whose independence could not be shown.
	StopReview StopClass = "review"
	// StopIntegration is the promotion onto the target branch: a target that kept
	// moving, a replay that conflicts, or a local and remote target that went
	// different ways.
	StopIntegration StopClass = "integration"
	// StopPublish is the publication of a promotion to the forge, which a run
	// reports rather than fails on when the local promotion landed.
	StopPublish StopClass = "publish"
	// StopCleanup is the removal of the run's worktree and branch after its work
	// integrated, which leaves the run succeeded with an artifact standing.
	StopCleanup StopClass = "cleanup"
	// StopRecording is the run's completion record: the outcome, the closure, or
	// the terminal state of a run whose work was done could not be written down.
	StopRecording StopClass = "recording"
	// StopProvider is the provider ending the run without judging the work: it
	// died past the relaunch budget, failed an invocation, was stopped on time with
	// nothing to continue from, or refused the run for a wait the harness will not
	// take.
	StopProvider StopClass = "provider"
	// StopEnvironmental is the environment refusing the round rather than the work
	// failing, which is the stop the round's budgets are given back for.
	StopEnvironmental StopClass = "environmental"
	// StopCancelled is the operator asking the run to stop, or the context the run
	// was given being cancelled.
	StopCancelled StopClass = "cancelled"
	// StopHarness is the harness's own step failing around the work: a state it
	// could not save, a tracker it could not write before the work was done, a
	// scratch directory it could not cut. It is the class a stop gets when none of
	// the gates above stopped it, and it is named rather than left empty because
	// an empty class reads as a record written before the field existed.
	StopHarness StopClass = "harness"
)

// stopClasses is the vocabulary stated as a list, closed for the reason every
// list in state.go is: a class nothing recognizes is refused at the save rather
// than printed at the front of a reason nobody can act on.
// TestTheDurableSchemaStoresEveryStopClassThePipelineRecords holds the pipeline
// to it.
var stopClasses = []StopClass{
	StopChecks, StopReview, StopIntegration, StopPublish, StopCleanup,
	StopRecording, StopProvider, StopEnvironmental, StopCancelled, StopHarness,
}

// StopClasses is the stop vocabulary as a caller outside this package reads it,
// answered with a copy for the reason the other vocabularies are.
func StopClasses() []StopClass { return slices.Clone(stopClasses) }

// Valid reports a class the durable schema stores.
func (c StopClass) Valid() bool { return slices.Contains(stopClasses, c) }

// StopReason is a recorded reason with the class that stopped the run as its
// first word, which is how every surface prints the reason. A record naming no
// class prints its reason as it was written, and a class with no reason beside it
// prints on its own, so neither half is lost for want of the other.
func StopReason(class StopClass, reason string) string {
	reason = strings.TrimSpace(reason)
	switch {
	case class == "":
		return reason
	case reason == "":
		return string(class)
	default:
		return string(class) + ": " + reason
	}
}
