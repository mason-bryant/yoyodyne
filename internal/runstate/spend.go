package runstate

// Every provider spend, one durable line at the moment it is known.
//
// What the harness spends was already recorded, and only ever inside the record
// of the thing that spent it: a run's event log carries the cost of the
// invocations that run made, and a conversation turn's carries its own. That is
// enough to price one run and nothing else. Asking what a day cost, or what a
// persona costs, or whether one model is worth what it charges over another,
// meant opening every run record there has ever been and knowing in advance
// which of them to open.
//
// So a spend says itself, in the shape every other log here is written in: an
// append-only log per product, one line per priced provider invocation, carrying
// who spent it, how much, on whose account and under which configuration, and
// what it was spent on. A line is written once and never revised, because what
// an invocation cost is a moment that happened rather than a state that moves.
//
// Nothing here aggregates. Adding the lines up is the operator's, and any later
// read model builds on the same lines rather than on a rollup this decided for
// them in advance -- which is also what makes the log evidence for routing a
// model, where the question is what each of them charged rather than what they
// charged together.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SpendSchemaVersion is 1 and has never changed.
const SpendSchemaVersion = 1

// maxEncodedSpendBytes bounds one encoded line, including the trailing newline.
// The writer and the reader share it, so a line that was written is always one
// that can be read back.
const maxEncodedSpendBytes = 8 << 10

// MaxSpendUnknownBytes bounds what a line may say about why an amount is not
// known. It is the provider's or the harness's own words about one invocation,
// so it is bounded the way a failure detail is rather than the way a document
// would be.
const MaxSpendUnknownBytes = 1 << 10

// SpendPhase names the part of the work one invocation served. The three a run
// splits into are the same three a run's own event log is split by, so a line
// and the run it came from say the same thing about where the money went; the
// two beside them are the invocations that belong to no run at all.
type SpendPhase string

const (
	// SpendPhaseDevelopment is the developer's first attempt at a change,
	// including any invocation reissued into it after the provider refused or
	// killed one.
	SpendPhaseDevelopment SpendPhase = "development"
	// SpendPhaseReview is a reviewer invocation, whichever way its verdict went.
	SpendPhaseReview SpendPhase = "review"
	// SpendPhaseRepair is every developer attempt after the first, whatever sent
	// the work back: a failing check, a refused path, or a reviewer's findings.
	SpendPhaseRepair SpendPhase = "repair"
	// SpendPhaseConversation is one turn of a management conversation.
	SpendPhaseConversation SpendPhase = "conversation"
	// SpendPhaseExchange is one round of an inter-role ask, which is an
	// invocation with neither a run nor a conversation behind it.
	SpendPhaseExchange SpendPhase = "exchange"
)

// SpendPhases lists every phase there is, in the order a refusal names them.
func SpendPhases() []SpendPhase {
	return []SpendPhase{
		SpendPhaseDevelopment,
		SpendPhaseReview,
		SpendPhaseRepair,
		SpendPhaseConversation,
		SpendPhaseExchange,
	}
}

func (p SpendPhase) Valid() bool {
	for _, candidate := range SpendPhases() {
		if p == candidate {
			return true
		}
	}
	return false
}

// SpendClassification says whether the amount on a line is a number anybody
// knows. It is carried rather than inferred from the amount, because the two
// things a zero could mean -- an invocation that was free and an invocation
// nobody was told the price of -- are opposite facts, and a total that adds the
// second in as nothing is wrong by however much was really spent.
type SpendClassification string

const (
	// SpendKnown is an amount the provider reported for the invocation.
	SpendKnown SpendClassification = "known"
	// SpendUnknown is an invocation the provider never reported a cost for. The
	// amount on such a line is zero and means nothing; what the line says is
	// beside it, in words.
	SpendUnknown SpendClassification = "unknown"
)

func (c SpendClassification) Valid() bool {
	return c == SpendKnown || c == SpendUnknown
}

// Spend is one priced provider invocation: what it cost, who spent it, and what
// it was spent on. It is written when the invocation ends, which is the moment
// its cost is known, and never revised.
type Spend struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	At            time.Time        `json:"at"`
	// Role is the contract the spender worked under and Agent is the configured
	// agent that filled it. Both are recorded because a project may configure
	// more than one agent for a role, and "what the developer costs" is then a
	// different question from "what this developer costs" -- which is exactly the
	// question a routing decision is.
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	Phase SpendPhase       `json:"phase"`
	// Classification says whether AmountUSD is a number. AmountUSD is always
	// encoded, including on an unknown line: a key left out reads to whatever
	// consumes the log as an amount of nothing, which is the one thing an unknown
	// spend must never be mistaken for.
	Classification SpendClassification `json:"classification"`
	AmountUSD      float64             `json:"amount_usd"`
	// Unknown says why nobody knows what this invocation cost, and is empty on a
	// line that names an amount.
	Unknown string `json:"unknown,omitempty"`
	// The account the invocation ran on and the configuration in force when it
	// did. Both are required: what a spend is attributable to is an account and a
	// set of effective values, and a line that named neither would be a number
	// with nowhere to put it.
	AccountAlias   string `json:"account_alias"`
	ConfigRevision string `json:"config_revision"`
	// Exactly one of these four names what the invocation belongs to, and it is
	// the same identifier that invocation's event log is named by. A run also
	// names the work item it served, which is what an item's spend is read by.
	//
	// A branch review has one of its own rather than borrowing run_id. It is not
	// a run and has no run behind it, so a line that put its identifier there
	// would hand anything joining these lines to run records a run id naming no
	// run — and the join is exactly what a later read model over this log is.
	RunID          string `json:"run_id,omitempty"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ExchangeID     string `json:"exchange_id,omitempty"`
	BranchReviewID string `json:"branch_review_id,omitempty"`
	// What served the invocation. The backend and the requested model are what
	// the harness asked for; the resolved model is what the provider reported
	// actually serving it, which is the only durable evidence where the requested
	// selector was a floating alias.
	Backend       domain.Backend `json:"backend"`
	Model         string         `json:"model,omitempty"`
	ResolvedModel string         `json:"resolved_model,omitempty"`
	// SessionID is the provider session the invocation ran in, where it reported
	// one. It is evidence about the invocation and never the record of it: what
	// this line says survives the session being gone.
	SessionID string `json:"session_id,omitempty"`
}

// Known reports a line carrying an amount somebody can add up.
func (s Spend) Known() bool { return s.Classification == SpendKnown }

// Validate reports every contract violation in the line at once.
func (s Spend) Validate() error {
	var problems []error
	if s.SchemaVersion != SpendSchemaVersion {
		problems = append(problems, fmt.Errorf("spend schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if s.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if !s.Role.Valid() {
		problems = append(problems, fmt.Errorf("role %q is not one of the harness's roles", s.Role))
	}
	if s.Agent != "" {
		if err := domain.ValidateIdentifier("agent name", s.Agent); err != nil {
			problems = append(problems, err)
		}
	}
	if !s.Phase.Valid() {
		problems = append(problems, fmt.Errorf("phase %q must be one of %s", s.Phase, joinSpendPhases()))
	}
	if !s.Backend.Valid() {
		problems = append(problems, fmt.Errorf("backend %q is not a backend the harness runs", s.Backend))
	}
	// The account and the configuration are the attribution, so a line missing
	// either is refused rather than stored as a spend nobody can attribute. Their
	// shapes are checked against the same patterns a run's are, because a line
	// naming an account nothing configured reads as evidence.
	if !accountAliasPattern.MatchString(s.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if !configRevisionPattern.MatchString(s.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	problems = append(problems, s.subjectProblem(), s.amountProblem())
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid spend: %w", err)
	}
	return nil
}

// subjectProblem reports a line that does not name exactly one thing it was
// spent on. Naming none is a spend nothing can ever be read back to, and naming
// two is a spend that would be counted twice by whoever reads it by either.
func (s Spend) subjectProblem() error {
	named := 0
	for _, id := range []string{s.RunID, s.ConversationID, s.ExchangeID, s.BranchReviewID} {
		if strings.TrimSpace(id) != "" {
			named++
		}
	}
	if named != 1 {
		return errors.New("a spend names exactly one of run_id, conversation_id, exchange_id, and branch_review_id")
	}
	// A work item belongs to a run and to nothing else: an invocation with no run
	// behind it served no assigned work, and saying it did would put money on an
	// item nothing was ever run for.
	if strings.TrimSpace(s.WorkItemID) != "" && strings.TrimSpace(s.RunID) == "" {
		return errors.New("only a run's spend names a work item")
	}
	return nil
}

// amountProblem reports an amount that disagrees with its classification. The
// two halves have to say one thing: an unknown amount that carried a number
// would be added up, and a known amount with no reason to doubt it that also
// carried one would leave a reader deciding which to believe.
func (s Spend) amountProblem() error {
	if !s.Classification.Valid() {
		return fmt.Errorf("classification %q must be %q or %q", s.Classification, SpendKnown, SpendUnknown)
	}
	switch s.Classification {
	case SpendKnown:
		if s.AmountUSD < 0 {
			return errors.New("a known amount is not negative")
		}
		if strings.TrimSpace(s.Unknown) != "" {
			return errors.New("a known amount does not also say why it is unknown")
		}
		return nil
	default:
		if s.AmountUSD != 0 {
			return errors.New("an unknown amount is not a number, so it is recorded as zero and read by its classification")
		}
		switch unknown := strings.TrimSpace(s.Unknown); {
		case unknown == "":
			return errors.New("an unknown amount says why nobody knows it")
		case len(unknown) > MaxSpendUnknownBytes:
			return fmt.Errorf("unknown is %d bytes, limit is %d", len(unknown), MaxSpendUnknownBytes)
		}
		return nil
	}
}

func joinSpendPhases() string {
	names := make([]string, 0, len(SpendPhases()))
	for _, phase := range SpendPhases() {
		names = append(names, string(phase))
	}
	return strings.Join(names, ", ")
}

// SpendStore is one product's cost log, in the same operating-system state root
// as runs and conversations and beside them rather than inside either. That
// placement is what the log is for: a spend outlives the run or conversation
// that made it, and a run whose state has been cleaned up still spent what it
// spent.
type SpendStore struct {
	root      string
	productID domain.ProductID
}

func NewSpendStore(root string, productID domain.ProductID) (*SpendStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SpendStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *SpendStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded spends
// actually are.
func (s *SpendStore) Path() string { return filepath.Join(s.root, "spend.jsonl") }

// Append records one spend durably. It is an append rather than a rewrite
// because a line is written once and never revised, and because several
// processes spend at once: two runs and a conversation all record here, and none
// of them may overwrite another's line.
func (s *SpendStore) Append(line Spend) error {
	if err := s.validate(line); err != nil {
		return err
	}
	encoded, err := encodeSpend(line)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedSpendBytes {
		return fmt.Errorf("encoded spend is %d bytes, limit is %d", len(encoded), maxEncodedSpendBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create spend directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect spend log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open spend log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append spend: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append spend: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync spend log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close spend log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded spend in the order it was recorded. A log that
// does not exist yet is a product nothing has been spent on, which is not a
// failure to read.
//
// It returns the lines and nothing derived from them. What they add up to is the
// operator's question and belongs to whoever asks it.
func (s *SpendStore) List() ([]Spend, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open spend log: %w", err)
	}
	defer file.Close()

	var lines []Spend
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedSpendBytes)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var decoded Spend
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, fmt.Errorf("decode spend log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode spend log: %w", err)
		}
		lines = append(lines, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read spend log: %w", err)
	}
	return lines, nil
}

func encodeSpend(line Spend) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(line); err != nil {
		return nil, fmt.Errorf("encode spend: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *SpendStore) validate(line Spend) error {
	if line.ProductID != s.productID {
		return fmt.Errorf("spend product %q does not match store product %q", line.ProductID, s.productID)
	}
	return line.Validate()
}
