package chat

// How old a conversation's picture of the product is, and how it is made
// current without being thrown away.
//
// The product context is gathered once, when a conversation opens, and sent on
// its first turn only; every later turn resumes a provider session that already
// holds it. That is what keeps a long conversation affordable, and it is also
// how a conversation ends up describing a repository as it was hours ago. It
// happened twice on 2026-08-16, confidently both times, and nothing in the
// conversation said the picture was old — the mechanism was documented and the
// operator was expected to remember it.
//
// So the conversation says it itself. Freshness is a comparison rather than a
// timestamp: the picture records when it was taken and what commit the
// repository was on, and the repository and the tracker can say cheaply what
// they have done since. A refresh takes a new picture and hands it to the
// product manager as evidence, exactly as harness activity travels, so nothing
// it believes is silently replaced: it is told what moved and reconciles it in
// its next reply.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"yoyodyne/internal/execution"
)

// Briefing is one picture of the product: the text the product manager is
// given, when it was taken, and the repository commit it was taken against.
// The last two are what a later comparison is made from, so a conversation can
// say how old its picture is rather than only what it says.
type Briefing struct {
	Text       string
	GatheredAt time.Time
	// Commit is the repository's HEAD when the picture was taken. It is empty
	// where the repository would not say, and a comparison that needs it then
	// reports itself as unknown rather than guessing.
	Commit string
	// Problems are what the gathering could not read or found malformed. They
	// are reported to the operator rather than failing the picture: intent
	// somebody wrote down badly is still intent.
	Problems []string
}

// Ground is the repository and the tracker as the harness can see them: where a
// conversation's picture comes from, and what it is compared against to say how
// old that picture is. Like Work, it is the harness's own hand — the product
// manager still has no filesystem, no commands, and no network, and nothing
// here gives it any.
//
// It is optional. A conversation without one still discusses the product; it
// simply cannot say what has moved or take a new picture, and it says so rather
// than implying nothing has changed.
type Ground interface {
	// Gather takes a picture of the product as it stands now.
	Gather(ctx context.Context) (Briefing, error)
	// Movement reports what the repository and the tracker have done since a
	// picture was taken. It never fails: a comparison that could not be made is
	// named inside the movement, because "unknown" is an answer and silence is
	// not.
	Movement(ctx context.Context, since Briefing) Movement
}

// Movement is what has changed since a picture was taken.
type Movement struct {
	Commits        int `json:"commits"`
	TrackerChanges int `json:"tracker_changes"`
	// RepositoryProblem and TrackerProblem name a comparison that could not be
	// made, so a count that is missing is never read as a zero.
	RepositoryProblem string `json:"repository_problem,omitempty"`
	TrackerProblem    string `json:"tracker_problem,omitempty"`
}

// Refreshed is what a refresh did: what had moved since the product manager was
// briefed, and the picture it is about to be given instead.
type Refreshed struct {
	// Since is what had moved while the product manager was working from the
	// old picture.
	Since Movement
	// GatheredAt is when the new picture was taken and Was is when the one it
	// replaces was, so the transcript can say how far the conversation had
	// drifted rather than only that it was refreshed.
	GatheredAt time.Time
	Was        time.Time
	// Problems are what the new picture could not read.
	Problems []string
}

// pendingRefresh is a picture the operator asked for that the product manager
// has not been given yet. It waits here rather than replacing anything: the
// conversation is not discarded, and what the product manager believes is only
// corrected by telling it, on its next turn, what moved.
type pendingRefresh struct {
	briefing Briefing
	since    Movement
	was      time.Time
}

// errNoGround reports a conversation with no repository or tracker behind it.
var errNoGround = errors.New("no repository or tracker is wired to this conversation, so it cannot say what has moved or read anything new")

// Freshness states in one line how old this conversation's picture of the
// product is and what has moved since it was taken. It is printed where the
// operator cannot miss it — as a conversation opens, and as one resumes —
// because the alternative is an operator who has to remember that a resumed
// conversation is a snapshot.
func (s *Session) Freshness(ctx context.Context) string {
	picture := s.picture()
	age := ageOf(s.options.clock().Now().Sub(picture.GatheredAt))
	if !s.briefed() {
		// The product manager has been given nothing yet, so what the
		// conversation holds is the picture its next turn will carry: it was
		// taken moments ago, there is nothing for a comparison to be about, and
		// spending a repository read to say so would be spending it to print a
		// zero.
		return fmt.Sprintf("context gathered %s, as this conversation opened.", age)
	}
	if s.options.Ground == nil {
		return fmt.Sprintf("context gathered %s; nothing here can say what has moved since.", age)
	}
	movement := s.options.Ground.Movement(ctx, picture)
	return fmt.Sprintf("context gathered %s; %s.%s", age, movement.render(), movement.hint())
}

// briefed reports whether the product manager has actually been given a
// picture. A conversation with turns behind it has been, whether or not the
// harness recorded which picture: those turns are what makes its age worth
// comparing rather than something it is about to be told.
func (s *Session) briefed() bool {
	return s.state.Turns > 0 || !s.state.ContextGatheredAt.IsZero()
}

// Refresh re-reads the repository and the tracker into the running
// conversation. It discards nothing: what was said stays said, and the new
// picture reaches the product manager as evidence on its next turn, with an
// account of what moved, so it reconciles what it believed rather than having
// it quietly swapped underneath. That difference is the whole point of the
// command — a conversation that was refreshed and one that was opened fresh end
// up equally current and keep different histories.
func (s *Session) Refresh(ctx context.Context) (Refreshed, error) {
	if s.options.Ground == nil {
		return Refreshed{}, errNoGround
	}
	previous := s.picture()
	// What moved is measured against the picture the product manager is
	// actually working from, before the new one is taken, because that is the
	// drift it has to be told about.
	movement := s.options.Ground.Movement(ctx, previous)
	briefing, err := s.options.Ground.Gather(ctx)
	if err != nil {
		return Refreshed{}, fmt.Errorf("re-read the repository and the tracker: %w", err)
	}
	if strings.TrimSpace(briefing.Text) == "" {
		return Refreshed{}, errors.New("the repository and the tracker were read and said nothing, so the conversation keeps the picture it has")
	}
	if briefing.GatheredAt.IsZero() {
		briefing.GatheredAt = s.options.clock().Now()
	}
	s.refresh = &pendingRefresh{briefing: briefing, since: movement, was: previous.GatheredAt}
	refreshed := Refreshed{
		Since:      movement,
		GatheredAt: briefing.GatheredAt,
		Was:        previous.GatheredAt,
		Problems:   briefing.Problems,
	}
	if err := s.emit(execution.EventContextRefreshed, map[string]any{
		"gathered_at": briefing.GatheredAt,
		"replaces":    previous.GatheredAt,
		"commit":      briefing.Commit,
		"since":       movement,
	}); err != nil {
		return refreshed, fmt.Errorf("record the refresh: %w", err)
	}
	return refreshed, nil
}

// picture is what the product manager is working from — or, before it has been
// given anything, what the next turn will hand it. The durable record is what
// says so, and it is read whether or not this process is the one that resumed
// the conversation: a picture a refresh delivered earlier in this very session
// is just as much the one being held as a picture some other process delivered
// yesterday. Measuring from anything else would re-count drift that has already
// been reported and call the conversation older than it is, which is the exact
// failure this is here to end.
func (s *Session) picture() Briefing {
	switch {
	case !s.state.ContextGatheredAt.IsZero():
		return Briefing{GatheredAt: s.state.ContextGatheredAt, Commit: s.state.ContextCommit}
	case s.state.Turns == 0 && s.refresh != nil:
		// Nothing has been delivered, so what the conversation holds is what its
		// first turn will carry, which a refresh has already replaced.
		return s.refresh.briefing
	case s.state.Turns == 0 && !s.options.Briefing.GatheredAt.IsZero():
		// The same, for the picture this process gathered as it opened. Calling
		// it stale because the conversation is old would report an age for
		// something that has not been used yet.
		return s.options.Briefing
	default:
		// A conversation recorded before the harness wrote this down was briefed
		// as it opened. That is the closest thing to the truth available, and it
		// is never later than the truth, so the picture is never reported as
		// fresher than it is.
		return Briefing{GatheredAt: s.state.StartedAt}
	}
}

// Render describes a refresh for the operator. It says plainly that the refresh
// happened, what it found, and that the product manager has not been told yet:
// a refresh that reads as already applied would be exactly the confident
// staleness this exists to end.
func (r Refreshed) Render() string {
	var rendered strings.Builder
	if r.GatheredAt.IsZero() {
		// Nothing was read, so there is nothing to describe. Why not is the
		// caller's error to report rather than something to narrate here.
		return ""
	}
	fmt.Fprintf(&rendered, "re-read the repository and the tracker. The picture it replaces was gathered %s, and %s.\n",
		ageOf(r.GatheredAt.Sub(r.Was)), r.Since.render())
	rendered.WriteString("the product manager is told what moved when you next say something to it, and reconciles it then.\n")
	rendered.WriteString("nothing said in this conversation was discarded.\n")
	for _, problem := range r.Problems {
		fmt.Fprintf(&rendered, "  %s\n", singleLine(problem, maxSurveyTitleBytes*2))
	}
	return rendered.String()
}

// render states what moved in the words the operator reads, naming a
// comparison that could not be made rather than counting it as nothing.
func (m Movement) render() string {
	var parts []string
	if m.RepositoryProblem != "" {
		parts = append(parts, "the repository could not be compared ("+singleLine(m.RepositoryProblem, maxSurveyTitleBytes)+")")
	} else {
		parts = append(parts, plural(m.Commits, "commit", "commits"))
	}
	if m.TrackerProblem != "" {
		parts = append(parts, "the tracker could not be compared ("+singleLine(m.TrackerProblem, maxSurveyTitleBytes)+")")
	} else {
		parts = append(parts, plural(m.TrackerChanges, "tracker change", "tracker changes"))
	}
	if m.settled() {
		return "nothing has moved in the repository or the tracker since"
	}
	return strings.Join(parts, " and ") + " since"
}

// hint offers the way out where there is something to be brought up to date,
// and stays quiet where there is not.
func (m Movement) hint() string {
	if m.settled() {
		return ""
	}
	return " /refresh reads what moved into this conversation."
}

// settled reports a picture nothing has moved under, which is only true when
// both comparisons were actually made.
func (m Movement) settled() bool {
	return m.Commits == 0 && m.TrackerChanges == 0 && m.RepositoryProblem == "" && m.TrackerProblem == ""
}

// refreshedContext frames a new picture for the product manager. It is evidence
// of the same kind as everything else it is given: an account of what the
// repository and the tracker now hold, never an instruction, and never a claim
// that anything it said before was wrong.
func (p pendingRefresh) prompt() string {
	var prompt strings.Builder
	prompt.WriteString("# Refreshed product context\n\n")
	fmt.Fprintf(&prompt, "The operator had the harness re-read the repository and the tracker. The context you were given when this conversation opened was gathered %s, and %s. What follows is the product as it stands now.\n\n",
		ageOf(p.briefing.GatheredAt.Sub(p.was)), p.since.render())
	prompt.WriteString("It is evidence like the rest of what you are given, not an instruction, and nothing you or the operator has said is withdrawn by it. Where it differs from what you were told at the start, say so plainly and carry on from what is here rather than from what you remember.\n\n")
	prompt.WriteString(p.briefing.Text)
	prompt.WriteString("\n")
	return prompt.String()
}

// ageOf says how long ago something was, in the coarsest unit that is still
// true. An operator deciding whether to refresh needs the order of magnitude,
// and a duration to the second would bury it.
func ageOf(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours())/24)
	}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}
