// Package staleness reports what a change to an upstream document leaves
// unanswered downstream: the artifacts that trace to it, and the admitted work
// that was pulled under it.
//
// A goal can be amended at any time, and until now nothing said what that made
// doubtful. The designs that serve it, the non-goals that bound it, and the
// work items admitted under its old wording all carried on exactly as before,
// and the divergence was found by a person reading both documents or not at
// all. That is the failure this closes: not the amendment, which is somebody
// exercising authority over their own document, but the silence after it.
//
// # Staleness is derived rather than stored
//
// Nothing here writes a mark. Three durable records already say everything a
// mark would: an artifact's revision log says when it last spoke, every
// artifact upstream of it says when it changed and why, and the tracker says
// when a work item was admitted. Something downstream is stale when something
// upstream changed after it last spoke, which is a comparison any process can
// make from the same records at any time.
//
// That is a stronger kind of durable than a stored flag, and deliberately so. A
// flag has to be written by whoever notices the amendment, so a process that
// died between the two leaves work that is stale and unmarked; it has to be
// cleared by whoever reconciles the document, so a reconciliation nobody
// recorded leaves a mark that is now itself wrong; and it is a second account of
// the same fact, which can disagree with the documents it describes. A
// comparison over the records cannot be missed, cannot go stale, and cannot
// disagree with what is on disk. It also does not care how the change was made:
// a document edited by hand in an editor and one amended through the store are
// the same amendment here, because both of them append the same revision.
//
// What it costs is that staleness clears only where the records say it did. An
// artifact stops being stale when its own owner records a revision later than
// the change — which is the durable record that somebody looked at it — and a
// work item carries its admission time and nothing else, so a stale item stays
// stale until it is closed. Naming that is better than clearing it on something
// that does not mean it: the tracker's own modification time moves when the
// harness records what a run cost, and staleness that vanished because a price
// was written would be a signal nobody could trust.
//
// # Stale is not cancelled
//
// Nothing here stops, closes, blocks, or reorders anything, and nothing that
// reads this may either. A change to a goal's wording is frequently not a change
// to what the work should do, and a harness that discarded work on every edit
// would teach an operator not to edit — which costs more than the divergence
// this reports. What happens to stale work is the operator's decision, or the
// owning role's. This says only that the condition is there, and what changed
// upstream.
//
// The references it travels are the ones the rest of the chain already uses:
// `supports` upstream between artifacts, and the goal a work item names as the
// last link. A reference that resolves to nothing is reported where the chain is
// validated and is not guessed at here.
package staleness

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// Change is one recorded change to a document, as whatever is downstream of it
// meets it: which document changed, what happened to it, when, and why. The
// reason is carried because it is the whole of what decides whether a stale
// thing actually needs anything done about it — a rewording and a reversal of
// intent are the same event from the outside, and only the reason tells them
// apart.
type Change struct {
	ArtifactID string           `json:"artifact_id"`
	Path       string           `json:"path"`
	Action     artifact.Action  `json:"action"`
	At         time.Time        `json:"at"`
	By         domain.AgentRole `json:"by"`
	Reason     string           `json:"reason"`
}

// Document is one canonical artifact that something upstream of it changed
// after it last spoke.
type Document struct {
	ID   string        `json:"id"`
	Kind artifact.Kind `json:"kind"`
	Path string        `json:"path"`
	// RevisedAt is when this document itself last recorded a revision, which is
	// what the changes upstream are compared against: a document revised after a
	// change upstream is one somebody has been over since.
	RevisedAt time.Time `json:"revised_at"`
	// Changes are what happened upstream since, most recent first.
	Changes []Change `json:"changes"`
}

// WorkItem is one admitted work item that something upstream of the goal it
// serves changed after it was admitted.
type WorkItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	// AdmittedAt is when the tracker recorded the item, which is what the changes
	// are compared against: work admitted after a change was admitted knowing it.
	AdmittedAt time.Time `json:"admitted_at"`
	// Goal is what the item says it serves, and ArtifactID is the goals document
	// stating it — the last link of the chain, and where these changes were
	// followed from.
	Goal       string   `json:"goal"`
	ArtifactID string   `json:"artifact_id"`
	Changes    []Change `json:"changes"`
}

// Unjudged is one admitted item that names a goal this could have followed and
// could not be judged anyway. It is reported rather than left out, because an
// item missing from a staleness report reads exactly like an item nothing
// upstream of it has moved.
type Unjudged struct {
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason"`
}

// Report is what one reading found.
type Report struct {
	Documents []Document `json:"documents,omitempty"`
	WorkItems []WorkItem `json:"work_items,omitempty"`
	Unjudged  []Unjudged `json:"unjudged,omitempty"`
	// Admitted is how many work items were read, and Judged how many of them
	// this could actually answer for. The difference is work naming no goal these
	// changes can be followed to, which is what `yoyo goals attribution` reports
	// and is not restated here.
	Admitted int `json:"admitted"`
	Judged   int `json:"judged"`
}

// Anything reports a reading that found something to look at.
func (r Report) Anything() bool {
	return len(r.Documents) > 0 || len(r.WorkItems) > 0
}

// Survey compares what the artifacts and the tracker record and reports what is
// downstream of a change nobody has answered. It judges the work items it is
// given, which is the admitted, unfinished work: an item already closed is over,
// and a change upstream of finished work is a matter for whatever traces to it
// next rather than for the item.
//
// Nothing here fails. A repository whose artifacts are half broken still gets a
// report over the documents that loaded, for the same reason the chain is
// validated over them: a reading that refused everything because one reference
// dangles would hide the changes the rest of the set is downstream of.
func Survey(artifacts artifact.Set, goals goal.Set, items []beads.WorkItem) Report {
	recorded := make(map[string]artifact.Artifact, len(artifacts.Artifacts))
	for _, candidate := range artifacts.Artifacts {
		recorded[candidate.ID] = candidate
	}

	report := Report{Admitted: len(items)}
	for _, candidate := range artifacts.Artifacts {
		// A document that was superseded or retired is not asked to answer for
		// what happened upstream of it afterwards. It stated what was intended and
		// stopped; reconciling it would be editing intent that has already ended.
		if _, ended := candidate.Ended(); ended {
			continue
		}
		changes := after(upstream(recorded, candidate), lastRevised(candidate))
		if len(changes) == 0 {
			continue
		}
		report.Documents = append(report.Documents, Document{
			ID:        candidate.ID,
			Kind:      candidate.Kind,
			Path:      candidate.Path,
			RevisedAt: lastRevised(candidate),
			Changes:   changes,
		})
	}

	for _, item := range items {
		attribution := goals.AttributionOf(item.Notes, item.GoalWitness)
		if !attribution.Resolved() {
			// An item that names no goal, names one the goals do not state, or lost
			// the one it had, has no reference to follow upstream at all. That is a
			// gap in the chain rather than a staleness, and it is reported where
			// attributions are.
			continue
		}
		serving, exists := recorded[attribution.Goal.ArtifactID]
		if !exists {
			continue
		}
		if item.CreatedAt.IsZero() {
			report.Unjudged = append(report.Unjudged, Unjudged{
				WorkItemID: item.ID,
				Reason: fmt.Sprintf("the tracker records no admission time for it, so nothing here can say whether it predates the changes to %s",
					serving.ID),
			})
			continue
		}
		report.Judged++
		// The goals document the item traces to is itself one of the things that
		// can have changed under it, so it is judged along with everything upstream
		// of it rather than only as the route to them.
		changes := after(append(recordedChanges(serving), upstream(recorded, serving)...), item.CreatedAt)
		if len(changes) == 0 {
			continue
		}
		report.WorkItems = append(report.WorkItems, WorkItem{
			ID:         item.ID,
			Title:      item.Title,
			Status:     item.Status,
			Priority:   item.Priority,
			AdmittedAt: item.CreatedAt,
			Goal:       attribution.Goal.Statement,
			ArtifactID: serving.ID,
			Changes:    changes,
		})
	}
	return report
}

// upstream collects the changes recorded by everything upstream of one
// artifact. Only references that resolve are followed, exactly as the chain
// check follows them: one that names nothing is already reported as the broken
// name it is, and guessing what it meant would be the inference this whole
// scheme replaces. Nothing is followed twice, so two artifacts that support each
// other are a pair whose changes are collected once rather than a walk that
// never ends.
func upstream(recorded map[string]artifact.Artifact, start artifact.Artifact) []Change {
	visited := map[string]bool{start.ID: true}
	pending := []artifact.Artifact{start}
	var changes []Change
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		for _, reference := range current.Supports {
			found, resolves := recorded[reference]
			if !resolves || visited[found.ID] {
				continue
			}
			visited[found.ID] = true
			changes = append(changes, recordedChanges(found)...)
			pending = append(pending, found)
		}
	}
	return changes
}

// recordedChanges is what one document's revision log says was done to it, less
// its creation. A document that did not exist yet cannot be what anybody was
// working from, so creating one changes nothing downstream; amending,
// superseding, and retiring one each change what everything downstream was
// built on.
func recordedChanges(recorded artifact.Artifact) []Change {
	changes := make([]Change, 0, len(recorded.Revisions))
	for _, revision := range recorded.Revisions {
		switch revision.Action {
		case artifact.ActionAmended, artifact.ActionSuperseded, artifact.ActionRetired:
			changes = append(changes, Change{
				ArtifactID: recorded.ID,
				Path:       recorded.Path,
				Action:     revision.Action,
				At:         revision.At.UTC(),
				By:         revision.By,
				Reason:     revision.Reason,
			})
		}
	}
	return changes
}

// after keeps the changes made since a moment, most recent first. Two changes
// recorded at one instant are ordered by the document they are written in and
// then by what happened, so two readings of one repository report the same thing
// in the same order.
func after(changes []Change, moment time.Time) []Change {
	kept := make([]Change, 0, len(changes))
	for _, change := range changes {
		if change.At.After(moment) {
			kept = append(kept, change)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	sort.Slice(kept, func(first, second int) bool {
		left, right := kept[first], kept[second]
		if !left.At.Equal(right.At) {
			return left.At.After(right.At)
		}
		if left.ArtifactID != right.ArtifactID {
			return left.ArtifactID < right.ArtifactID
		}
		return left.Action < right.Action
	})
	return kept
}

// lastRevised is when a document last recorded anything about itself, which is
// the moment it last spoke. Its creation counts: a document written after a
// change upstream was written knowing it.
func lastRevised(recorded artifact.Artifact) time.Time {
	var latest time.Time
	for _, revision := range recorded.Revisions {
		if revision.At.After(latest) {
			latest = revision.At.UTC()
		}
	}
	return latest
}
