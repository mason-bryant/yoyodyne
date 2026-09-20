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
//
// Saying so turned out not to be enough. On 2026-09-18 the product manager
// advised the operator to add a section to CLAUDE.md that the file at HEAD had
// opened with for a month: its picture was roughly 500 landings old, the
// freshness line had said as much every time the conversation resumed, and the
// advice was built on the picture anyway. A line the operator has to act on is
// a line somebody eventually reads past. So the comparison is now the trigger
// as well as the statement: before every reply the harness measures how far
// the picture has fallen behind the target branch, in landings rather than
// hours, and past a configured threshold it re-reads the repository itself
// before the turn is answered. Where the re-read cannot be made — the tracker
// or the repository will not answer — the reply carries the age in its own
// text, so advice given over a stale picture is at least labelled as such. The
// threshold selects when; no setting turns either off.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
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
	// ShippedDocumentationBytes is what the shipped documentation the picture
	// was assembled from adds up to on disk. It is recorded with the picture
	// because the set grows with every behaviour the product acquires and has a
	// ceiling at which carrying it whole is a product decision again; a size
	// written down on every pass is what makes that growth visible before the
	// gate on it fails. It is zero where the project names no documentation.
	ShippedDocumentationBytes int
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

// pendingRefresh is a new picture the product manager has not been given yet,
// whether the operator asked for it or the harness took it because the old one
// had fallen too far behind. It waits here rather than replacing anything: the
// conversation is not discarded, and what the product manager believes is only
// corrected by telling it, on its next turn, what moved.
type pendingRefresh struct {
	briefing Briefing
	since    Movement
	was      time.Time
	// trigger says who took the picture, because the role is told so: a refresh
	// the operator asked for and one the harness made because the picture was
	// stale are framed differently, and the threshold the second names is what
	// tells the role why it is being re-briefed unasked.
	trigger   refreshTrigger
	threshold int
}

// refreshTrigger is what caused a refresh, recorded with it so the log and the
// role can both say whether the operator asked or the harness decided.
type refreshTrigger string

const (
	refreshByOperator refreshTrigger = "operator"
	refreshByHarness  refreshTrigger = "harness"
)

// errNoGround reports a conversation with no repository or tracker behind it.
var errNoGround = errors.New("no repository or tracker is wired to this conversation, so it cannot say what has moved or read anything new")

// DefaultRefreshAfterLandings is how many landings on the target branch a
// picture may fall behind before the harness re-reads it, where a project
// states nothing, and MaxRefreshAfterLandings bounds what a project may set.
// Both are the configuration's, because that is where a project's number is
// refused; they are named here because this is where the number is spent.
const (
	DefaultRefreshAfterLandings = config.DefaultRefreshAfterLandings
	MaxRefreshAfterLandings     = config.MaxRefreshAfterLandings
)

// The outcomes a measurement can have, recorded on the conversation with each
// reply and reported to the operator with it.
const (
	// PictureCurrent says the picture is within the threshold and the turn was
	// answered from it as it stood.
	PictureCurrent = "current"
	// PictureRefreshed says the picture was past the threshold and the harness
	// re-read the repository and the tracker before the turn was answered — or
	// the operator had already asked for a refresh that this turn delivers.
	PictureRefreshed = "refreshed"
	// PictureStated says the picture was past the threshold, the re-read could
	// not be made, and the reply says in its own text how many landings old the
	// picture it was answered from is.
	PictureStated = "stated"
	// PictureUnmeasured says the repository would not say how far behind the
	// picture is, so the reply says that instead. It is not treated as current:
	// an age nothing could measure is the same confident staleness this exists
	// to end, in a smaller place.
	PictureUnmeasured = "unmeasured"
)

// PictureAge is what the harness found out about the picture a reply was
// answered from, and what it did about it. It is written to the conversation's
// log before every reply — so the record says how old the picture was at each
// one — and handed back on the reply, so the operator is told the same thing.
type PictureAge struct {
	// GatheredAt and Commit identify the picture that was measured: when it was
	// taken and the commit it was taken against.
	GatheredAt time.Time `json:"gathered_at"`
	Commit     string    `json:"commit,omitempty"`
	// Landings is how many commits the target branch has taken on since the
	// picture was taken. It is the age in the unit that matters — what the
	// repository holds that the picture does not — rather than in hours, which
	// say nothing about a branch that moved fifty times in a morning or once in
	// a week. TrackerChanges is the tracker's half of the same comparison,
	// recorded beside it and not what the threshold is measured against.
	Landings       int `json:"landings"`
	TrackerChanges int `json:"tracker_changes"`
	// RepositoryProblem and TrackerProblem name a comparison that could not be
	// made, so a count that is missing is never read as a zero.
	RepositoryProblem string `json:"repository_problem,omitempty"`
	TrackerProblem    string `json:"tracker_problem,omitempty"`
	// Threshold is the number of landings past which the harness re-reads, as
	// configured for this conversation.
	Threshold int `json:"threshold"`
	// Outcome is one of the Picture constants above.
	Outcome string `json:"outcome"`
	// RefreshedBy says who took the new picture on a refreshed outcome: the
	// harness, because the old one was past the threshold, or the operator, who
	// asked for one and is being told nothing they were not told when they did.
	RefreshedBy string `json:"refreshed_by,omitempty"`
	// RefreshProblem is why a re-read the picture's age called for could not be
	// made, on a stated outcome.
	RefreshProblem string `json:"refresh_problem,omitempty"`
}

// stale reports a picture that has fallen past the threshold.
func (p PictureAge) stale() bool {
	return p.RepositoryProblem == "" && p.Landings > p.Threshold
}

// measurePicture says how old the picture the next reply will be answered from
// is, in landings on the target branch, records the answer on the conversation,
// and re-reads the repository and the tracker where the picture has fallen past
// the threshold. It is measured before every reply, the first included: a
// first turn carries the picture taken as the conversation opened, which is
// usually minutes old and is not always — a conversation opened and left is
// one whose first reply is still owed the measurement. It is nothing at all
// only where there is nothing to measure with: a conversation with no ground
// behind it cannot compare, and its freshness line already says so.
//
// A refresh the operator asked for and the turn is about to deliver is the
// refreshed outcome without a second comparison: the movement it was measured
// against is the one the operator was shown, and measuring again would report
// the drift twice.
func (s *Session) measurePicture(ctx context.Context) (*PictureAge, error) {
	if s.options.Ground == nil {
		return nil, nil
	}
	picture := s.picture()
	age := &PictureAge{
		GatheredAt: picture.GatheredAt,
		Commit:     picture.Commit,
		Threshold:  s.options.refreshAfterLandings(),
	}
	if s.refresh != nil {
		age.note(s.refresh.since)
		age.Outcome = PictureRefreshed
		age.RefreshedBy = string(s.refresh.trigger)
		return age, s.recordPictureAge(age)
	}
	movement := s.options.Ground.Movement(ctx, picture)
	age.note(movement)
	switch {
	case movement.RepositoryProblem != "":
		age.Outcome = PictureUnmeasured
	case !age.stale():
		age.Outcome = PictureCurrent
	default:
		// The comparison already made is the one the refresh is measured against,
		// so what the role is told moved and what the log says moved are one
		// reading rather than two taken moments apart.
		_, err := s.refreshFrom(ctx, refreshByHarness, movement)
		switch {
		case err != nil && s.refresh == nil:
			// Nothing was taken, so the turn is answered from the old picture and
			// says so.
			age.Outcome = PictureStated
			age.RefreshProblem = singleLine(err.Error(), maxTrackerFailureBytes)
		case err != nil:
			// The picture was taken and the record would not take the refresh. It
			// waits for the next attempt exactly as an operator's unrecorded refresh
			// does, and the message fails here rather than being answered from a
			// picture the log cannot say was delivered.
			age.Outcome = PictureRefreshed
			age.RefreshedBy = string(refreshByHarness)
			return age, err
		default:
			age.Outcome = PictureRefreshed
			age.RefreshedBy = string(refreshByHarness)
		}
	}
	return age, s.recordPictureAge(age)
}

// note copies what moved onto the measurement.
func (p *PictureAge) note(movement Movement) {
	p.Landings = movement.Commits
	p.TrackerChanges = movement.TrackerChanges
	p.RepositoryProblem = movement.RepositoryProblem
	p.TrackerProblem = movement.TrackerProblem
}

// recordPictureAge writes the measurement to the conversation's log. It is
// written before the turn is taken rather than after, so a reply the provider
// never finished still has the age of the picture it was being answered from
// on the record beside it.
func (s *Session) recordPictureAge(age *PictureAge) error {
	if err := s.emit(execution.EventContextMeasured, age); err != nil {
		return fmt.Errorf("record how old the conversation's picture is: %w", err)
	}
	return nil
}

// statement is the sentence a reply carries in its own text where it was
// answered from a picture the harness could not bring current. It is written
// by the harness rather than asked of the role, because the role saying so is
// what the prompt asks for and this is what the record has to be able to show
// whether or not it did. Nothing is said where the picture was current or was
// refreshed: a reply built on a picture the harness just took has nothing to
// disclaim.
func (p PictureAge) statement() string {
	switch p.Outcome {
	case PictureStated:
		return fmt.Sprintf("(This reply rests on a picture of the repository that is %s behind the target branch, past the %d this project allows, and the harness could not re-read it before answering: %s. Hold what it says about the repository against that, or /refresh and ask again.)",
			plural(p.Landings, "landing", "landings"), p.Threshold, p.RefreshProblem)
	case PictureUnmeasured:
		return fmt.Sprintf("(This reply rests on a picture of the repository gathered %s, and the harness could not measure how far behind the target branch it is: %s. Hold what it says about the repository against that, or /refresh and ask again.)",
			p.GatheredAt.UTC().Format(time.RFC3339), singleLine(p.RepositoryProblem, maxSurveyTitleBytes))
	default:
		return ""
	}
}

// prompt is what the role is told about a picture the harness could not bring
// current, delivered with the turn. It is the one place the role is asked to
// state the age itself: the harness's own statement above goes on the reply
// whatever the role does, and this is what lets the role's advice say which
// claims rest on the old picture and which it read past it.
func (p PictureAge) prompt() string {
	switch p.Outcome {
	case PictureStated:
		return fmt.Sprintf("# Your picture of the repository is stale\n\nThe product context you are working from was gathered %s and the target branch has taken on %s since, past the %d this project allows before the harness re-reads it. The harness tried to re-read the repository and the tracker before this turn and could not: %s. Advice about the repository in this reply rests on that picture unless you read the path it rests on first; say plainly in your reply how many landings old the picture is, and read a path before advising about it where you can.\n\n",
			p.GatheredAt.UTC().Format(time.RFC3339), plural(p.Landings, "landing", "landings"), p.Threshold, p.RefreshProblem)
	case PictureUnmeasured:
		return fmt.Sprintf("# The age of your picture of the repository is unknown\n\nThe product context you are working from was gathered %s, and the harness could not measure how far behind the target branch it has fallen: %s. Treat it as old rather than as current: say in your reply that the picture's age is unknown, and read a path before advising about it where you can.\n\n",
			p.GatheredAt.UTC().Format(time.RFC3339), singleLine(p.RepositoryProblem, maxSurveyTitleBytes))
	default:
		return ""
	}
}

// Render describes the measurement for the operator reading what a reply was
// built from. It says nothing about a current picture, because a line under
// every reply saying the picture was fine is a line nobody reads by the time
// it says otherwise — and nothing about a refresh the operator asked for, which
// /refresh already described to them when they did.
func (p PictureAge) Render() string {
	switch p.Outcome {
	case PictureRefreshed:
		if p.RefreshedBy != string(refreshByHarness) {
			return ""
		}
		return fmt.Sprintf("[picture] %s behind the target branch, past the %d this project allows; the harness re-read the repository and the tracker before answering, and nothing said here was discarded.\n",
			plural(p.Landings, "landing", "landings"), p.Threshold)
	case PictureStated:
		return fmt.Sprintf("[picture] %s behind the target branch, past the %d this project allows, and the harness could not re-read it: %s. The reply says how old its picture is.\n",
			plural(p.Landings, "landing", "landings"), p.Threshold, p.RefreshProblem)
	case PictureUnmeasured:
		return fmt.Sprintf("[picture] how far behind the target branch this conversation's picture is could not be measured: %s. The reply says so.\n",
			singleLine(p.RepositoryProblem, maxSurveyTitleBytes))
	default:
		return ""
	}
}

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
	return fmt.Sprintf("context gathered %s; %s.%s", age, movement.render(), movement.hint(s.options.refreshAfterLandings()))
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
	// What moved is measured against the picture the product manager is
	// actually working from, before the new one is taken, because that is the
	// drift it has to be told about.
	return s.refreshFrom(ctx, refreshByOperator, s.options.Ground.Movement(ctx, s.picture()))
}

// refreshFrom takes the new picture, given the movement already measured
// against the one it replaces. The operator's command and the harness's own
// stale-picture refresh share it, so a refresh is one thing however it was
// triggered: the same read, the same pending delivery, and the same event, with
// the trigger recorded on it.
func (s *Session) refreshFrom(ctx context.Context, trigger refreshTrigger, movement Movement) (Refreshed, error) {
	previous := s.picture()
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
	s.refresh = &pendingRefresh{
		briefing:  briefing,
		since:     movement,
		was:       previous.GatheredAt,
		trigger:   trigger,
		threshold: s.options.refreshAfterLandings(),
	}
	refreshed := Refreshed{
		Since:      movement,
		GatheredAt: briefing.GatheredAt,
		Was:        previous.GatheredAt,
		Problems:   briefing.Problems,
	}
	if err := s.emit(execution.EventContextRefreshed, map[string]any{
		"gathered_at":                 briefing.GatheredAt,
		"replaces":                    previous.GatheredAt,
		"commit":                      briefing.Commit,
		"shipped_documentation_bytes": briefing.ShippedDocumentationBytes,
		"since":                       movement,
		"trigger":                     trigger,
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
		return Briefing{GatheredAt: s.state.ContextGatheredAt, Commit: s.state.ContextCommit, ShippedDocumentationBytes: s.state.ContextShippedDocumentationBytes}
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
	rendered.WriteString("the agent is told what moved when you next say something to it, and reconciles it then.\n")
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
// and stays quiet where there is not. Where the repository has moved past the
// threshold it says what the harness is about to do about it, so the operator
// reading the line is not left deciding something the next reply decides.
func (m Movement) hint(threshold int) string {
	switch {
	case m.settled():
		return ""
	case m.RepositoryProblem == "" && m.Commits > threshold:
		return fmt.Sprintf(" That is past the %d landings this project allows, so the next reply re-reads it first; /refresh reads it now.", threshold)
	default:
		return " /refresh reads what moved into this conversation."
	}
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
	switch p.trigger {
	case refreshByHarness:
		// The role is told the number as well as the fact, because the number is
		// what it would otherwise have had to state in its reply: a picture past
		// the threshold is one whose advice about the repository has to say how old
		// it is, and this is the harness answering that instead.
		fmt.Fprintf(&prompt, "The harness re-read the repository and the tracker before answering this turn, because the picture you were working from had fallen %s behind the target branch, past the %d this project allows. That picture was gathered %s, and %s. What follows is the product as it stands now.\n\n",
			plural(p.since.Commits, "landing", "landings"), p.threshold, ageOf(p.briefing.GatheredAt.Sub(p.was)), p.since.render())
	default:
		fmt.Fprintf(&prompt, "The operator had the harness re-read the repository and the tracker. The context you were given when this conversation opened was gathered %s, and %s. What follows is the product as it stands now.\n\n",
			ageOf(p.briefing.GatheredAt.Sub(p.was)), p.since.render())
	}
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
