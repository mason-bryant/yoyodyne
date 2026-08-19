package orchestrator

// What one work item may be given, read from the configuration.
//
// The counters themselves are durable state and belong to the product; what
// bounds them is a decision an operator writes down. Assembling the two here
// keeps every caller reading one set of caps: a triage action that refuses and
// the listing that says how close an item is to being refused must not be
// working from different numbers.

import (
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TriageCaps is the ceiling on what triage may spend on one work item across
// every run of it.
//
// Two of the three actions — another repair, another whole run — buy review
// rounds, and `triage.review_rounds_cap` is what an operator states about those:
// the total one item may accumulate before triage may no longer hand it back.
// The third buys no round at all, so it takes the size of the integration
// retries a single run is already permitted, which is the same judgement about
// the same thing one level up: how many times a promotion that did not land may
// be tried again before it is a person's problem.
//
// The other two are triage's own and are not configured, because they are not a
// judgement about a project's pace: they are the workflow itself, which is that
// triage acts alone once per item on each of them and a second is a person's
// decision rather than a bigger budget. Nothing an operator could set here would
// mean anything else, and the rounds cap cannot stand in for them — an item
// whose runs stop before any reviewer verdict spends no round, so without these
// it could be handed back and re-run without bound.
func TriageCaps(execution config.Execution, triage config.Triage) runstate.TriageCaps {
	return runstate.TriageCaps{
		ReviewRounds: triage.ReviewRoundsCap,
		RepairGrants: triageActsAlone,
		Reruns:       triageActsAlone,
		MergeRearms:  execution.IntegrationRetriesBeforeReconciliation,
	}
}

// triageActsAlone is how many times triage takes one of its own decisions about
// one work item without a person: once. Past it the decision is not a bigger
// budget but an escalation, which is what the development manager's workflow
// says and what the item's durable counters are what make true.
const triageActsAlone = 1

// TriageRepairGrantRounds is how many review rounds triage hands an item when it
// decides the work is worth another go, before the round cap truncates it.
//
// `triage.repair_grant_attempts` states it in repair attempts, and that is the
// same count: every repair attempt is judged, so each attempt an item is granted
// is one more verdict it will produce. The conversion is here rather than at the
// call site so nothing has to rediscover that the two units are one.
func TriageRepairGrantRounds(triage config.Triage) int {
	return triage.RepairGrantAttempts
}
