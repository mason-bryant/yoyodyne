// Package evaluation is what the product manager makes of an idea the operator
// brought it.
//
// An operator says "what if we did X". That is not a work item, not a goal, and
// not a design, and the honest answer to it is usually neither yes nor no: it is
// what the evidence says, what the product is already committed to, what is
// still unknown, and a recommendation with reasoning somebody can disagree with.
// Before this existed that answer was prose in a conversation, which means it
// was gone as soon as the conversation was — and the decision it led to had no
// record of what it was decided from.
//
// So an evaluation is written down. It is emphatically advisory: recording one
// admits no work, changes no document, and approves nothing. What it can do is
// recommend, and every route out of it is a route that already exists and is
// already governed — work through the proposal path the operator decides, a
// change to a document through that document's owner and the approval workflow.
// Nothing here is a shortcut around either, which is the point: research that
// could quietly turn an idea into approved work would be a way to approve work
// by asking a model to look something up.
//
// What the record keeps is what somebody would need to judge the recommendation
// later rather than take it on trust: the sources, when they were retrieved, the
// claims the evidence supports, what was inferred rather than read, what remains
// uncertain, what argues the other way, and the reasoning itself.
package evaluation

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

// SchemaVersion is versioned independently of run and conversation state, for
// the reason a proposed amendment's is: an evaluation outlives the conversation
// that produced it, and it is written once rather than revised.
const SchemaVersion = 1

// Fence opens the one block a reply may record an evaluation in. It is a
// distinct language tag for the same reason every other channel's is: a
// recommendation can never be confused with JSON the conversation happens to be
// discussing.
const Fence = "```yoyodyne-evaluation"

// MaxBlockBytes bounds the untrusted payload the block is decoded from, and
// MaxTextBytes one field of prose within it. An evaluation is an argument
// somebody reads, so the fields are generous for paragraphs and far too small to
// carry a document.
const (
	MaxBlockBytes = 32 << 10
	MaxTextBytes  = 4 << 10
)

// The bounds on the lists an evaluation carries. Each is long enough for a real
// evaluation of a real idea and short enough that the record stays something a
// person reads rather than a transcript of everything the model thought.
const (
	MaxClaims        = 10
	MaxStatements    = 10
	MaxCitations     = 20
	maxClaimBytes    = 1 << 10
	maxIdeaBytes     = 2 << 10
	maxCitationBytes = 512
)

// Recommendation is what the product manager advises the operator to do with the
// idea. There are four because there are four real answers: two that settle it,
// one that says not now, and one that says the way to find out is to try it
// small. A recommendation is advice and nothing more — none of these four does
// anything on its own.
type Recommendation string

const (
	// RecommendAdopt says the idea is worth doing, which is the start of the
	// governed path to doing it rather than any part of doing it.
	RecommendAdopt Recommendation = "adopt"
	// RecommendReject says it is not worth doing, and the record is what stops
	// the same idea being reconsidered from scratch every few weeks.
	RecommendReject Recommendation = "reject"
	// RecommendDefer says it may be worth doing and not now, which is a different
	// answer from either and the one most often flattened into a soft yes.
	RecommendDefer Recommendation = "defer"
	// RecommendExperiment says the evidence does not settle it and something
	// bounded would. It is the honest answer to a genuinely open question, and it
	// is a recommendation rather than an experiment: what would run, and whether
	// it runs at all, is work that goes through the queue like all other work.
	RecommendExperiment Recommendation = "experiment"
)

func recommendations() []Recommendation {
	return []Recommendation{RecommendAdopt, RecommendReject, RecommendDefer, RecommendExperiment}
}

func (r Recommendation) Valid() bool {
	for _, known := range recommendations() {
		if r == known {
			return true
		}
	}
	return false
}

// Headline is what this recommendation says, in the harness's own words, so a
// listing does not leave four different answers all reading as "evaluated".
func (r Recommendation) Headline() string {
	switch r {
	case RecommendAdopt:
		return "worth doing; nothing is admitted or approved by saying so"
	case RecommendReject:
		return "not worth doing"
	case RecommendDefer:
		return "may be worth doing, and not now"
	case RecommendExperiment:
		return "not settled by the evidence; something bounded would settle it"
	default:
		return "a recommendation the harness does not recognize"
	}
}

func namedRecommendations() string {
	names := make([]string, 0, len(recommendations()))
	for _, value := range recommendations() {
		names = append(names, string(value))
	}
	return strings.Join(names, ", ")
}

// Claim is one material thing the evaluation asserts, together with what
// supports it. The support is required on a fact for the reason the whole record
// exists: a fact with no source is an assertion, and the difference between the
// two is what a reader is here to check.
type Claim struct {
	Claim string `json:"claim"`
	// Source names where this came from — a source the harness retrieved from, a
	// document in the repository, or the work tracker. It is free text because
	// what supports a claim is not always a URL, and it is required because a
	// claim nobody can trace back is an opinion wearing a fact's clothes.
	Source string `json:"source"`
}

func (c Claim) Validate() error {
	var problems []error
	problems = append(problems, boundText("claim", c.Claim, maxClaimBytes))
	problems = append(problems, boundText("source", c.Source, maxCitationBytes))
	return errors.Join(problems...)
}

// Citation is one source the evaluation rests on, as the product manager cites
// it. It is the agent's own claim about what it read, which is why the record
// keeps the harness's research beside it: what the harness actually retrieved,
// and when, is not something the agent asserts.
type Citation struct {
	// Reference is the URL or identifier of what was read.
	Reference string `json:"reference"`
	// Source names which configured source it came from, where it came from one.
	// It is optional: an evaluation may cite a repository document, and no
	// research source produced that.
	Source string `json:"source,omitempty"`
	// Note is what this source was used for, in a line.
	Note string `json:"note,omitempty"`
}

func (c Citation) Validate() error {
	var problems []error
	problems = append(problems, boundText("reference", c.Reference, maxCitationBytes))
	if source := strings.TrimSpace(c.Source); len(source) > maxCitationBytes {
		problems = append(problems, fmt.Errorf("source is %d bytes, limit is %d", len(source), maxCitationBytes))
	}
	if note := strings.TrimSpace(c.Note); len(note) > maxClaimBytes {
		problems = append(problems, fmt.Errorf("note is %d bytes, limit is %d", len(note), maxClaimBytes))
	}
	return errors.Join(problems...)
}

// Entry is one evaluation exactly as the product manager wrote it. Everything
// else on the durable record — which conversation, which turn, what the harness
// actually retrieved and when — is what the harness knows and the agent does not
// get to assert.
type Entry struct {
	// Idea is the operator's proposal, in the product manager's own words, so the
	// record says what was evaluated rather than pointing at a conversation
	// somebody would have to reread.
	Idea string `json:"idea"`
	// Recommendation is the advice, and Reasoning is why. Both are required: a
	// recommendation with no reasoning asks the operator to take advice on trust,
	// which is the one thing a record like this exists to make unnecessary.
	Recommendation Recommendation `json:"recommendation"`
	Reasoning      string         `json:"reasoning"`
	// Alignment is how the idea sits against the brief, the goals, and what the
	// product has already committed to. It is required because it is the half of
	// the judgement that is the product manager's own: whether a thing is a good
	// idea in general is not the question they were asked.
	Alignment string `json:"alignment"`
	// Facts are what the evidence states, each with what supports it. Inferences
	// are what the product manager concluded from those facts rather than read
	// anywhere, and Uncertainties are what it does not know. They are three lists
	// rather than one paragraph because a paragraph is where the difference
	// between them goes to die.
	Facts         []Claim  `json:"facts,omitempty"`
	Inferences    []string `json:"inferences,omitempty"`
	Uncertainties []string `json:"uncertainties,omitempty"`
	// Counterevidence is what argues against the recommendation. It is asked for
	// separately because a recommendation that surveyed only what supports it is
	// the failure mode of this whole exercise.
	Counterevidence []string `json:"counterevidence,omitempty"`
	// Sources are what the evaluation rests on, as cited.
	Sources []Citation `json:"sources,omitempty"`
	// EvidenceGap is what adequate evidence would have needed and could not be
	// obtained. It is empty where the evidence was adequate, and stating it is how
	// the product manager reports that it could not find out rather than filling
	// the gap with a confident sentence.
	EvidenceGap string `json:"evidence_gap,omitempty"`
	// FollowUp is what the product manager would do next if the operator agrees:
	// work to propose, a change to argue for, an experiment to bound. It is prose
	// and it is advisory — nothing here creates a work item or changes a document,
	// and both of those have their own paths with their own approvals.
	FollowUp string `json:"follow_up,omitempty"`
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	problems = append(problems, boundText("idea", e.Idea, maxIdeaBytes))
	if !e.Recommendation.Valid() {
		problems = append(problems, fmt.Errorf("recommendation %q is not one the harness recognizes; the recommendations are: %s",
			e.Recommendation, namedRecommendations()))
	}
	problems = append(problems, boundText("reasoning", e.Reasoning, MaxTextBytes))
	problems = append(problems, boundText("alignment", e.Alignment, MaxTextBytes))
	if len(e.Facts) > MaxClaims {
		problems = append(problems, fmt.Errorf("facts has %d entries, limit is %d", len(e.Facts), MaxClaims))
	}
	for i, fact := range e.Facts {
		if err := fact.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("facts[%d]: %w", i, err))
		}
	}
	problems = append(problems, boundStatements("inferences", e.Inferences))
	problems = append(problems, boundStatements("uncertainties", e.Uncertainties))
	problems = append(problems, boundStatements("counterevidence", e.Counterevidence))
	if len(e.Sources) > MaxCitations {
		problems = append(problems, fmt.Errorf("sources has %d entries, limit is %d", len(e.Sources), MaxCitations))
	}
	for i, citation := range e.Sources {
		if err := citation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("sources[%d]: %w", i, err))
		}
	}
	if gap := strings.TrimSpace(e.EvidenceGap); len(gap) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("evidence_gap is %d bytes, limit is %d", len(gap), MaxTextBytes))
	}
	if follow := strings.TrimSpace(e.FollowUp); len(follow) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("follow_up is %d bytes, limit is %d", len(follow), MaxTextBytes))
	}
	// A fact is a claim about the world with something behind it, so an evaluation
	// that states facts and cites nothing is refused. It is the one cross-field
	// rule here, and it is the rule the whole record is for: the alternative is a
	// durable document that looks sourced and is not.
	if len(e.Facts) > 0 && len(e.Sources) == 0 {
		problems = append(problems, errors.New("facts are stated and no sources are cited; a fact nobody can trace back is an inference"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid evaluation: %w", err)
	}
	return nil
}

func boundStatements(field string, statements []string) error {
	if len(statements) > MaxStatements {
		return fmt.Errorf("%s has %d entries, limit is %d", field, len(statements), MaxStatements)
	}
	var problems []error
	for i, statement := range statements {
		if err := boundText(fmt.Sprintf("%s[%d]", field, i), statement, maxClaimBytes); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

func boundText(field, value string, limit int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(trimmed) > limit {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), limit)
	}
	return nil
}

// document is the payload shape of the fenced block. One reply records one
// evaluation: an evaluation is the answer to one idea, and a reply carrying
// several would be a batch of recommendations nobody read separately.
type document struct {
	Evaluation Entry `json:"evaluation"`
}

// Extract splits a reply into what the product manager said and the evaluation
// it recorded. An evaluation comes only from the fenced block: prose that reads
// like a recommendation is a recommendation nobody can find afterwards, which is
// the state this channel exists to end.
func Extract(reply string) (string, *Entry, error) {
	block, err := fenced.Split(reply, Fence, "evaluation")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	entry, err := Decode(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, entry, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what is about to become
// a durable record of what somebody was advised has to be exactly what the
// product manager wrote.
func Decode(payload string) (*Entry, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode evaluation: the evaluation block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("decode evaluation: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded document
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode evaluation: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode evaluation: unexpected trailing content after the evaluation")
	}
	if err := decoded.Evaluation.Validate(); err != nil {
		return nil, err
	}
	return &decoded.Evaluation, nil
}

// Evaluation is one durable recommendation: what the product manager wrote, what
// the harness knows about where it came from, and the research the harness
// actually performed for it.
//
// The research travels with the record rather than beside it because the two
// answer different questions. The citations say what the product manager says it
// read; the findings say what was actually retrieved, from which source, and at
// what moment. A record with only the first is a record that cannot be audited,
// and one with only the second cannot be read.
type Evaluation struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Role is the contract this was written under and Agent the configured agent
	// that filled it, recorded apart for the reason a report records both.
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// ConversationID and Turn say where this was said, so an evaluation leads back
	// to the exchange that produced it once that exchange is long over.
	ConversationID string           `json:"conversation_id"`
	Turn           int              `json:"turn"`
	ProductID      domain.ProductID `json:"product_id"`
	RepositoryID   string           `json:"repository_id,omitempty"`
	Entry          Entry            `json:"entry"`
	// Research is what the harness retrieved while this evaluation was being
	// reached, with each source's own retrieval time. It is the harness's record
	// rather than the agent's account of it.
	Research   []research.Finding `json:"research,omitempty"`
	RecordedAt time.Time          `json:"recorded_at"`
}

var idPattern = regexp.MustCompile(`^evaluation-[a-f0-9]{32}$`)

func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate evaluation id: %w", err)
	}
	return "evaluation-" + hex.EncodeToString(raw), nil
}

// Validate reports every contract violation in the recorded evaluation at once.
func (e Evaluation) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(e.ID) {
		problems = append(problems, errors.New("id is invalid"))
	}
	if err := domain.ValidateIdentifier("role", string(e.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(e.ConversationID) == "" {
		problems = append(problems, errors.New("conversation id is required"))
	}
	if e.Turn < 0 {
		problems = append(problems, errors.New("turn cannot be negative"))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if e.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	problems = append(problems, e.Entry.Validate())
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid evaluation %q: %w", e.ID, err)
	}
	return nil
}

// Attribution is what the harness knows about where an evaluation came from,
// which is everything about it except the argument itself.
type Attribution struct {
	Role           domain.AgentRole
	Agent          string
	ConversationID string
	Turn           int
	ProductID      domain.ProductID
	RepositoryID   string
}

// Record turns one written evaluation into a durable one, attaching the research
// the harness performed and the moment it was recorded.
func Record(entry Entry, attribution Attribution, findings []research.Finding, now time.Time) (Evaluation, error) {
	id, err := NewID()
	if err != nil {
		return Evaluation{}, err
	}
	recorded := Evaluation{
		SchemaVersion:  SchemaVersion,
		ID:             id,
		Role:           attribution.Role,
		Agent:          strings.TrimSpace(attribution.Agent),
		ConversationID: attribution.ConversationID,
		Turn:           attribution.Turn,
		ProductID:      attribution.ProductID,
		RepositoryID:   strings.TrimSpace(attribution.RepositoryID),
		Entry:          entry,
		Research:       findings,
		RecordedAt:     now.UTC(),
	}
	if err := recorded.Validate(); err != nil {
		return Evaluation{}, err
	}
	return recorded, nil
}

// Render describes one evaluation for whoever is reading it. Everything but the
// harness's own lines came from a provider, so provider text is indented under
// the evaluation's identifier and never printed at the margin.
func (e Evaluation) Render() string {
	var rendered strings.Builder
	author := string(e.Role)
	if e.Agent != "" && e.Agent != string(e.Role) {
		author = e.Agent + " (" + string(e.Role) + ")"
	}
	fmt.Fprintf(&rendered, "  [%s] %s — %s\n", e.ID, e.Entry.Recommendation, e.Entry.Recommendation.Headline())
	fmt.Fprintf(&rendered, "      by the %s in %s, turn %d, recorded %s\n",
		author, e.ConversationID, e.Turn, e.RecordedAt.UTC().Format(time.RFC3339))
	// Said on every evaluation rather than only where it might be misread. The
	// whole risk this record carries is being read as a decision, and a caveat
	// that appears only sometimes is one a reader learns to skip.
	rendered.WriteString("      advisory: nothing was admitted, approved, or changed by recording this\n")
	writeField(&rendered, "idea", e.Entry.Idea)
	writeField(&rendered, "alignment", e.Entry.Alignment)
	writeField(&rendered, "reasoning", e.Entry.Reasoning)
	for _, fact := range e.Entry.Facts {
		writeField(&rendered, "fact", fact.Claim+" ["+fact.Source+"]")
	}
	writeList(&rendered, "inferred", e.Entry.Inferences)
	writeList(&rendered, "uncertain", e.Entry.Uncertainties)
	writeList(&rendered, "against", e.Entry.Counterevidence)
	for _, citation := range e.Entry.Sources {
		writeField(&rendered, "source", citation.describe())
	}
	if gap := strings.TrimSpace(e.Entry.EvidenceGap); gap != "" {
		writeField(&rendered, "evidence gap", gap)
	}
	if follow := strings.TrimSpace(e.Entry.FollowUp); follow != "" {
		writeField(&rendered, "follow-up", follow)
	}
	for _, finding := range e.Research {
		writeField(&rendered, "retrieved", finding.Describe())
	}
	return rendered.String()
}

func (c Citation) describe() string {
	described := strings.TrimSpace(c.Reference)
	if source := strings.TrimSpace(c.Source); source != "" {
		described += " (" + source + ")"
	}
	if note := strings.TrimSpace(c.Note); note != "" {
		described += ": " + note
	}
	return described
}

func writeField(rendered *strings.Builder, label, value string) {
	for _, line := range indent(label + ": " + value) {
		rendered.WriteString(line)
	}
}

func writeList(rendered *strings.Builder, label string, values []string) {
	for _, value := range values {
		writeField(rendered, label, value)
	}
}

func indent(text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	indented := make([]string, 0, len(lines))
	for _, line := range lines {
		indented = append(indented, "      "+strings.TrimSpace(line)+"\n")
	}
	return indented
}

// Contract is the section the product manager's immutable contract carries about
// evaluating an idea. It is here rather than in that contract's own text because
// the block, the bounds, and the vocabulary are this package's, and a second
// statement of them in another file is two statements that drift.
const Contract = `# Evaluating an idea the operator brings you

An operator will put an idea to you rather than a work item: "what if we did X",
"is Y worth it", "should we move to Z". That is a question, not an instruction,
and answering it is yours.

Work through it rather than answering from the top of your head. Ask what you
genuinely need to know before you can judge it — what problem it is meant to
solve, who it is for, what would count as it working — and ask that as an
ordinary question, one or two at a time, rather than interrogating the operator.
Where evidence would change your recommendation, gather it with the research
block above. Then weigh it against the brief, the goals, and what this product
has already committed to, because whether the idea is good in the abstract is
not what you were asked.

Then record what you concluded, so the reasoning outlives this conversation. End
your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-evaluation
{"evaluation":{
  "idea":"what the operator proposed, in your words",
  "recommendation":"adopt|reject|defer|experiment",
  "alignment":"how this sits with the brief, the goals, and what the product is already committed to",
  "reasoning":"why you recommend that",
  "facts":[{"claim":"something the evidence states","source":"where you read it"}],
  "inferences":["what you concluded from those facts rather than read anywhere"],
  "uncertainties":["what you do not know"],
  "counterevidence":["what argues against your recommendation"],
  "sources":[{"reference":"url or identifier","source":"which research source","note":"what you used it for"}],
  "evidence_gap":"what adequate evidence would have needed and you could not get",
  "follow_up":"what you would do next if the operator agrees"
}}
` + "```" + `

"idea", "recommendation", "alignment", and "reasoning" are required and the rest
are not; leave out a list you have nothing for rather than filling it. Record one
evaluation per reply, and only when you have actually reached one — an idea you
are still clarifying is a question to ask, not an evaluation to write down.

The three lists are three different things and keeping them apart is most of what
this is worth. A fact is something your evidence states and it carries where you
read it; an inference is your own conclusion from those facts; an uncertainty is
something you do not know. Do not promote an inference to a fact by citing the
page that made you think of it. Where you could not get adequate evidence, say so
in "evidence_gap" and let the recommendation reflect that, rather than answering
confidently anyway.

Recording an evaluation is advisory and it is the whole of what this block does.
It admits no work, changes no document, and approves nothing, and you never
describe it as though it had. If the answer is work, propose it in the same reply
through the proposal block and let the operator decide it. If the answer is a
change to the brief or the goals, that is the operator's to make and you say so.
If it is a change to a design or a decision record, that is the architect's, and
it goes to them through the governed path rather than through this.`
