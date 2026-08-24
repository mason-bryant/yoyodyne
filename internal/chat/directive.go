package chat

// Directives, as the operator gives and settles them from inside the
// conversation they are already in.
//
// Everything here runs on the operator's own instruction and never on the
// product manager's, exactly as steering does. What is different from steering
// is where it lands: a redirection is written on one work item and read by the
// next attempt at that item, and a directive is written for the product and read
// by every run of every item, in this process and in any other. That is the
// whole reason both exist — one changes what a piece of work should do, and the
// other changes what the product is, which is not a thing that can be recorded
// beside a single bead.
//
// Recording a directive that pauses work is an enforcement rather than a note,
// and this says so. It does not cancel anything: a run in flight keeps its
// claim, its branch, and its worktree, and stops at its next gate, which is the
// point before its change could be judged or promoted against intent that is
// being rewritten. What it leaves behind is settled by the same reconciliation
// stopping already uses, so a paused run is left resumable rather than
// abandoned.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Directives is the harness's durable record of what the operator has told it,
// as a conversation reads and writes it. Like Work it is the harness's own hand:
// the product manager cannot reach any of it, because what the operator has
// directed is not something an agent revises on its own account.
type Directives interface {
	// Record makes one directive durable, where every run reads it.
	Record(ctx context.Context, request DirectiveRequest) (directive.Directive, error)
	// List reports every recorded directive, resolved and unresolved alike, in
	// the order they were received.
	List(ctx context.Context) ([]directive.Directive, error)
	// Find reports the one directive a reference names, so something about to be
	// recorded against a directive can be held to naming one that exists. The
	// reference may be any prefix that names exactly one directive.
	Find(ctx context.Context, reference string) (directive.Directive, error)
	// Resolve settles one, which is what releases the work it paused. The
	// reference may be any prefix that names exactly one directive.
	Resolve(ctx context.Context, reference, resolution string) (directive.Directive, error)
	// CarryOut records what came of a directive that paused nothing. It is the
	// disposition of the commonest kind of directive there is, and the only thing
	// that ever says one was acted on rather than filed: it refuses the pausing
	// kinds, so admitting work can never lift a pause that is holding work up.
	CarryOut(ctx context.Context, reference, outcome string) (directive.Directive, error)
}

// DirectiveRequest is what the operator asked to have recorded. The harness
// supplies everything else about a directive — its identity, when it was
// received, which product it belongs to — because none of that is the operator's
// to assert.
type DirectiveRequest struct {
	Kind       directive.Kind
	Text       string
	Artifact   string
	Unresolved string
	Scope      []string
}

// DirectiveRecorded is what recording a directive achieved. It is deliberately
// more than the record: a directive that pauses work has done something to work
// that was already under way, and an operator who is only shown the record would
// have to go and find out what.
type DirectiveRecorded struct {
	Directive directive.Directive
	// Paused names the work items the directive stopped, and Noted names those
	// the pause was actually written onto. They differ when the tracker refused a
	// note, which is reported rather than hidden: the pause holds either way,
	// because it is enforced from the directive rather than from the note.
	Paused []string
	Noted  []string
	// Problems are what could not be done while recording it. None of them
	// unrecords the directive, which is already durable and already enforced.
	Problems []string
	// Settlements are what settling left behind found, run for the same reason
	// stopping runs it: work nothing is acting on any more is settled rather than
	// abandoned, and a run this directive paused is reported as resumable.
	Settlements []Settlement
}

// DirectiveResolved is what settling a directive achieved.
type DirectiveResolved struct {
	Directive directive.Directive
}

// errNoDirectives reports a conversation with no durable directive record behind
// it. Such a conversation still discusses the product; it simply cannot record
// or settle a directive, and says so rather than appearing to enforce one.
var errNoDirectives = errors.New("no durable directive record is wired to this conversation, so it can discuss directives but cannot record or enforce one")

// ReadDirectives reports what the operator has directed. It is read-only:
// asking what has been directed never changes what is in force.
func (s *Session) ReadDirectives(ctx context.Context) ([]directive.Directive, error) {
	if s.options.Directives == nil {
		return nil, errNoDirectives
	}
	recorded, err := s.options.Directives.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read what the operator has directed: %w", err)
	}
	return recorded, nil
}

// RecordDirective records a directive and reconciles it against the work the
// harness already has in flight.
//
// A directive that pauses work is enforced from the record rather than from
// anything this does afterwards: every run reads the durable directives before
// it starts, before it resumes, and before it puts a change through the gate, so
// the pause holds across processes and survives this one exiting. What happens
// here is the part that would otherwise be invisible — the affected work is
// named, the pause is written where the work is tracked, and what a paused run
// leaves behind is settled the way stopping already settles it.
func (s *Session) RecordDirective(ctx context.Context, request DirectiveRequest) (DirectiveRecorded, error) {
	if s.options.Directives == nil {
		return DirectiveRecorded{}, errNoDirectives
	}
	if err := validateDirectiveRequest(request); err != nil {
		return DirectiveRecorded{}, err
	}
	recorded, err := s.options.Directives.Record(ctx, request)
	if err != nil {
		return DirectiveRecorded{}, fmt.Errorf("record the directive: %w", err)
	}
	result := DirectiveRecorded{Directive: recorded}
	if err := s.emit(execution.EventDirectiveRecorded, map[string]any{
		"directive_id": recorded.ID,
		"kind":         string(recorded.Kind),
		"pauses":       recorded.Pauses(),
		"scope":        recorded.Scope,
	}); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("record the directive in this conversation's log: %v", err))
	}
	s.notice("the operator recorded directive %s: %s", recorded.ID, singleLine(recorded.Text, maxSurveyTitleBytes))
	if !recorded.Pauses() {
		// An operational directive is in effect and stops nothing, so there is no
		// work to reconcile it against.
		return result, nil
	}
	s.reconcileDirective(ctx, recorded, &result)
	return result, nil
}

// reconcileDirective is the part of recording that reaches work already under
// way. Nothing here enforces the pause — the durable record does that — so
// every failure in it is reported and none of it is fatal: a note the tracker
// refused still leaves the work paused, and saying otherwise would be worse than
// saying nothing.
func (s *Session) reconcileDirective(ctx context.Context, recorded directive.Directive, result *DirectiveRecorded) {
	affected, err := s.affectedByDirective(ctx, recorded)
	if err != nil {
		result.Problems = append(result.Problems, err.Error())
	}
	result.Paused = affected
	if s.options.Work == nil {
		return
	}
	// The pause is written where the work is tracked, naming the directive and
	// what is unresolved about it, because an operator looking at a claimed item
	// that has gone quiet has to be able to read why from the item itself.
	for _, workItemID := range affected {
		if err := s.options.Work.Direct(ctx, workItemID, s.directivePauseNote(recorded)); err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("record the pause on %s where the work is tracked: %v", workItemID, err))
			continue
		}
		result.Noted = append(result.Noted, workItemID)
	}
	// Settling is the same reconciliation stopping runs, and it is run for the
	// same reason: work nothing is acting on any more is settled rather than left
	// dangling. A run this directive paused is left exactly as it is and reported
	// as resumable, which is the difference between pausing work and cancelling it.
	settlements, err := s.options.Work.Settle(ctx)
	if err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("settle what the paused work leaves behind: %v", err))
	}
	result.Settlements = settlements
}

// affectedByDirective names the work this directive stops. A scoped directive
// affects what it named; an unscoped one affects everything, so what is worth
// naming is the work that is actually moving — the runs in flight and the items
// the tracker holds as claimed. Listing every open item would bury exactly the
// work an operator has to know about.
func (s *Session) affectedByDirective(ctx context.Context, recorded directive.Directive) ([]string, error) {
	if len(recorded.Scope) > 0 {
		return append([]string(nil), recorded.Scope...), nil
	}
	if s.options.Work == nil {
		return nil, nil
	}
	survey, err := s.options.Work.Survey(ctx)
	if err != nil {
		return nil, fmt.Errorf("find the work this directive affects: %w", err)
	}
	var affected []string
	for _, run := range survey.InFlight {
		affected = appendUniqueItem(affected, run.WorkItemID)
	}
	for _, item := range survey.Claimed {
		affected = appendUniqueItem(affected, item.ID)
	}
	return affected, nil
}

func appendUniqueItem(items []string, item string) []string {
	if strings.TrimSpace(item) == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

// ResolveDirective settles a directive, which is what releases the work it
// paused. The release is the durable record changing rather than anything done
// to a run: the next consultation of the directives, in whichever process makes
// it, finds nothing pausing the work and carries on.
func (s *Session) ResolveDirective(ctx context.Context, reference, resolution string) (DirectiveResolved, error) {
	if s.options.Directives == nil {
		return DirectiveResolved{}, errNoDirectives
	}
	trimmed := strings.TrimSpace(resolution)
	if trimmed == "" {
		return DirectiveResolved{}, errors.New("say how the directive was settled; work resumes on the answer rather than on the act of answering")
	}
	if len(trimmed) > MaxOperatorMessageBytes {
		return DirectiveResolved{}, fmt.Errorf("resolution is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	resolved, err := s.options.Directives.Resolve(ctx, strings.TrimSpace(reference), trimmed)
	if err != nil {
		return DirectiveResolved{}, fmt.Errorf("resolve the directive: %w", err)
	}
	if err := s.emit(execution.EventDirectiveResolved, map[string]any{
		"directive_id": resolved.ID,
		"resolution":   trimmed,
	}); err != nil {
		return DirectiveResolved{Directive: resolved}, fmt.Errorf("record the resolution in this conversation's log: %w", err)
	}
	s.notice("the operator resolved directive %s: %s", resolved.ID, singleLine(trimmed, maxSurveyTitleBytes))
	return DirectiveResolved{Directive: resolved}, nil
}

// validateDirectiveRequest checks what the operator typed before anything
// durable is written. The record has its own validation, which is what actually
// holds; this is here so a mistyped command is refused in the operator's own
// terms rather than as a failure from inside a store.
func validateDirectiveRequest(request DirectiveRequest) error {
	var problems []error
	if !request.Kind.Valid() {
		problems = append(problems, fmt.Errorf("%q is not a kind of directive", request.Kind))
	}
	if strings.TrimSpace(request.Text) == "" {
		problems = append(problems, errors.New("say what the directive is; a directive with nothing in it is not one"))
	}
	if len(request.Text) > MaxOperatorMessageBytes {
		problems = append(problems, fmt.Errorf("the directive is %d bytes, limit is %d", len(request.Text), MaxOperatorMessageBytes))
	}
	if request.Kind.Pauses() && strings.TrimSpace(request.Unresolved) == "" {
		problems = append(problems, fmt.Errorf("say what is unresolved about it; a %s directive pauses work, and a pause nobody can name a reason for is one nobody can lift", request.Kind))
	}
	if request.Kind == directive.KindArtifact && strings.TrimSpace(request.Artifact) == "" {
		problems = append(problems, errors.New("name the governed artifact it changes"))
	}
	for _, scoped := range request.Scope {
		if err := beads.ValidateIssueID(strings.TrimSpace(scoped)); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// directivePauseNote is what a paused work item records about why. It names the
// directive and what is unresolved, which are the two things somebody needs to
// lift the pause, and it says plainly that nothing was cancelled.
func (s *Session) directivePauseNote(recorded directive.Directive) string {
	var note strings.Builder
	fmt.Fprintf(&note, "Yoyodyne paused this work for directive %s, recorded by the operator from product-manager conversation %s after turn %d.\n\n",
		recorded.ID, s.state.ConversationID, s.state.Turns)
	fmt.Fprintf(&note, "The operator said: %s\n", recorded.Text)
	if recorded.Artifact != "" {
		fmt.Fprintf(&note, "Governed artifact it changes: %s\n", recorded.Artifact)
	}
	fmt.Fprintf(&note, "Unresolved: %s\n\n", recorded.Unresolved)
	note.WriteString("Nothing was cancelled. A run in flight for this item keeps its claim, branch, and worktree and stops at its next gate; nothing new starts on it. Resolving the directive is what lets it carry on.\n")
	return note.String()
}

// Render describes what recording a directive achieved. The directive itself
// comes first, then what it stopped, because an operator recording one that
// pauses work is about to want to know what they just paused.
func (d DirectiveRecorded) Render() string {
	if d.Directive.ID == "" {
		// Nothing was recorded, so there is nothing to describe. Why not is the
		// caller's error to report rather than something to narrate here.
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString(d.Directive.Render())
	if !d.Directive.Pauses() {
		rendered.WriteString("this directive is in effect from now, and nothing is paused by it.\n")
		return rendered.String()
	}
	if len(d.Paused) == 0 {
		rendered.WriteString("nothing is in flight or claimed for it to pause; work it affects will not start while it is unresolved.\n")
	} else {
		fmt.Fprintf(&rendered, "paused %d work item(s): %s\n", len(d.Paused), strings.Join(d.Paused, ", "))
		rendered.WriteString("nothing was cancelled: a run in flight keeps its claim, branch, and worktree and stops at its next gate.\n")
		if unnoted := d.unnoted(); len(unnoted) > 0 {
			// The pause holds from the record rather than from the note, so an item
			// the tracker would not take a note for is still paused. Saying which is
			// what stops somebody reading the item and concluding otherwise.
			fmt.Fprintf(&rendered, "the pause could not be written onto %s, which is still paused; the item itself will not say why.\n",
				strings.Join(unnoted, ", "))
		}
	}
	fmt.Fprintf(&rendered, "/resolve %s <how it was settled> releases that work.\n", d.Directive.ID)
	rendered.WriteString(renderSettlements(d.Settlements))
	for _, problem := range d.Problems {
		fmt.Fprintf(&rendered, "  %s\n", singleLine(problem, maxSurveyTitleBytes*2))
	}
	return rendered.String()
}

// unnoted names the paused items the pause could not be written onto.
func (d DirectiveRecorded) unnoted() []string {
	var missing []string
	for _, paused := range d.Paused {
		noted := false
		for _, item := range d.Noted {
			if item == paused {
				noted = true
				break
			}
		}
		if !noted {
			missing = append(missing, paused)
		}
	}
	return missing
}

// Render describes a settled directive and what settling it released.
func (d DirectiveResolved) Render() string {
	if d.Directive.ID == "" {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString(d.Directive.Render())
	rendered.WriteString("the work this paused can carry on. A run it stopped continues from where it stopped the next time the item is started.\n")
	return rendered.String()
}

// renderDirectives lists what the operator has directed, the open ones first,
// because those are the ones still holding work up or still owed an outcome.
//
// The two groups are open and settled rather than unresolved and resolved: only
// a pausing directive is ever resolved, and an operational one settles by being
// carried out, so labelling the groups by the pausing kinds' word would file
// every carried-out directive under something that did not happen to it.
func renderDirectives(recorded []directive.Directive) string {
	if len(recorded) == 0 {
		return "no directives are recorded for this product.\n"
	}
	var open, settled []directive.Directive
	for _, candidate := range recorded {
		if candidate.Resolved() {
			settled = append(settled, candidate)
			continue
		}
		open = append(open, candidate)
	}
	var rendered strings.Builder
	for _, group := range []struct {
		label string
		items []directive.Directive
	}{{"open", open}, {"settled", settled}} {
		if len(group.items) == 0 {
			fmt.Fprintf(&rendered, "%s: none\n", group.label)
			continue
		}
		fmt.Fprintf(&rendered, "%s (%d):\n", group.label, len(group.items))
		for _, item := range group.items {
			rendered.WriteString(item.Render())
		}
	}
	return rendered.String()
}

// parseDirectiveCommand reads the one line the operator typed into a request.
// The kind comes first because it is what decides whether work stops, and the
// harness must never infer that: a directive that paused every run because a
// classifier guessed would be worse than one that paused nothing.
//
// The two pausing kinds take what is unresolved before what was said, separated
// by a bar, because a pause that cannot name what it is waiting for is one
// nobody can lift and the command should refuse rather than record it.
func parseDirectiveCommand(argument string) (DirectiveRequest, error) {
	trimmed := strings.TrimSpace(argument)
	if trimmed == "" {
		return DirectiveRequest{}, errors.New("say what the directive is; /help shows the shapes it takes")
	}
	word, rest, _ := strings.Cut(trimmed, " ")
	switch strings.ToLower(word) {
	case string(directive.KindAmbiguous), "ask":
		unresolved, text, found := strings.Cut(rest, "|")
		if !found {
			return DirectiveRequest{}, errors.New("an ambiguous directive is /directive ambiguous <what is unresolved> | <what the operator said>")
		}
		return DirectiveRequest{
			Kind:       directive.KindAmbiguous,
			Text:       strings.TrimSpace(text),
			Unresolved: strings.TrimSpace(unresolved),
		}, nil
	case string(directive.KindArtifact):
		artifact, remainder, found := strings.Cut(rest, " ")
		if !found {
			return DirectiveRequest{}, errors.New("an artifact directive is /directive artifact <artifact> <what is unresolved> | <what changes>")
		}
		unresolved, text, found := strings.Cut(remainder, "|")
		if !found {
			return DirectiveRequest{}, errors.New("an artifact directive is /directive artifact <artifact> <what is unresolved> | <what changes>")
		}
		return DirectiveRequest{
			Kind:       directive.KindArtifact,
			Artifact:   strings.TrimSpace(artifact),
			Text:       strings.TrimSpace(text),
			Unresolved: strings.TrimSpace(unresolved),
		}, nil
	default:
		// Anything that does not name a pausing kind is operational, which is what
		// most directives are: it takes effect immediately and stops nothing.
		return DirectiveRequest{Kind: directive.KindOperational, Text: trimmed}, nil
	}
}
