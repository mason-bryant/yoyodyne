package runstate

// The durable record of a review of an accumulated change. A per-work-item
// review is recorded on the run whose change it judged; a branch review has no
// such run — the work it covers was integrated by runs that are already settled
// and cleaned up — so it is recorded here, as its own append-only log, with the
// same session and model evidence a run carries. What it must never do is write
// into a run: a verdict on the accumulated change cannot retroactively decide
// anything about a run that has already ended, and a record that could reach one
// would make it look as though it had.

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

	"yoyodyne/internal/domain"
)

// maxEncodedBranchReviewBytes bounds one encoded record, including the trailing
// newline. It is comfortably above the bound on a verdict itself, so a review
// the harness accepted is always a review it can write down. The writer and the
// reader share it, so a record that was written is always one that can be read
// back.
const maxEncodedBranchReviewBytes = 256 << 10

// BranchReviewSchemaVersion is the version of the durable branch review record.
const BranchReviewSchemaVersion = 1

// BranchReview is one independent review of a branch against a base commit: the
// change that was judged, the verdict, and the provider identity that produced
// it. A review that reached no verdict is recorded too, under Failure: the
// evidence that a review was attempted and did not answer is worth as much as a
// verdict to whoever is deciding whether the branch has been reviewed at all.
type BranchReview struct {
	SchemaVersion int              `json:"schema_version"`
	ReviewID      string           `json:"review_id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id,omitempty"`
	Branch        string           `json:"branch"`
	BaseRef       string           `json:"base_ref,omitempty"`
	BaseCommit    string           `json:"base_commit"`
	HeadCommit    string           `json:"head_commit"`
	// Commits is how many of the branch's commits were described to the
	// reviewer, and CommitsOmitted how many the bounds dropped. Truncated says
	// the reviewer was shown an incomplete change, whichever half was cut, which
	// is the fact that decides whether an approval was even available to it.
	Commits        int       `json:"commits"`
	CommitsOmitted int       `json:"commits_omitted,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
	ReviewedAt     time.Time `json:"reviewed_at"`
	SessionID      string    `json:"session_id,omitempty"`
	Model          string    `json:"model,omitempty"`
	ResolvedModel  string    `json:"resolved_model,omitempty"`
	Decision       string    `json:"decision,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
	Failure        string    `json:"failure,omitempty"`
}

// Validate rejects a record that could not describe a review that happened.
func (b BranchReview) Validate() error {
	var problems []error
	if b.SchemaVersion != BranchReviewSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", BranchReviewSchemaVersion))
	}
	if !runIDPattern.MatchString(b.ReviewID) {
		problems = append(problems, errors.New("review id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(b.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !validLocalBranch(b.Branch) {
		problems = append(problems, errors.New("branch must be a local branch name"))
	}
	if !commitPattern.MatchString(b.BaseCommit) {
		problems = append(problems, errors.New("base commit is invalid"))
	}
	if !commitPattern.MatchString(b.HeadCommit) {
		problems = append(problems, errors.New("head commit is invalid"))
	}
	if b.BaseCommit == b.HeadCommit {
		problems = append(problems, errors.New("base and head commits are the same, which is no accumulated change"))
	}
	if b.Commits < 1 {
		problems = append(problems, errors.New("a reviewed branch carries at least one commit"))
	}
	if b.CommitsOmitted < 0 {
		problems = append(problems, errors.New("omitted commit count cannot be negative"))
	}
	if b.ReviewedAt.IsZero() {
		problems = append(problems, errors.New("reviewed at is required"))
	}
	switch b.Decision {
	case ReviewApprove, ReviewRepair:
		if strings.TrimSpace(b.Summary) == "" {
			problems = append(problems, errors.New("a decided review requires a summary"))
		}
		if b.Decision == ReviewRepair && len(b.Findings) == 0 {
			problems = append(problems, errors.New("a repair verdict requires at least one finding"))
		}
	case "":
		// A review with no decision is one that did not answer, and it has to say
		// what stopped it: an empty record either way would be indistinguishable
		// from a branch nobody reviewed.
		if strings.TrimSpace(b.Failure) == "" {
			problems = append(problems, errors.New("a review with no decision must record what stopped it"))
		}
	default:
		problems = append(problems, fmt.Errorf("decision %q must be %q or %q", b.Decision, ReviewApprove, ReviewRepair))
	}
	for i, finding := range b.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid branch review: %w", errors.Join(problems...))
	}
	return nil
}

// Approved reports the one thing a reader of this record acts on: whether an
// independent reviewer approved the accumulated change. Everything else — a
// repair verdict, a review that failed, a review of a change too large to show
// in full — is not an approval, and each of them says so through the same
// answer, so nothing downstream has to enumerate the ways a review can fall
// short.
func (b BranchReview) Approved() bool {
	return b.Decision == ReviewApprove
}

// BranchReviewStore is where branch reviews are collected, beside the runs and
// the reports rather than among them. It is one append-only log per product, for
// the same reason the report log is: a branch review outlives every run whose
// work it judged, and it is never revised once written.
type BranchReviewStore struct {
	root      string
	productID domain.ProductID
}

func NewBranchReviewStore(root string, productID domain.ProductID) (*BranchReviewStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &BranchReviewStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *BranchReviewStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded reviews
// actually are.
func (s *BranchReviewStore) Path() string { return filepath.Join(s.root, "branch-reviews.jsonl") }

// Append records one branch review durably.
func (s *BranchReviewStore) Append(reviewed BranchReview) error {
	if err := s.validate(reviewed); err != nil {
		return err
	}
	encoded, err := encodeBranchReview(reviewed)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedBranchReviewBytes {
		return fmt.Errorf("encoded branch review is %d bytes, limit is %d", len(encoded), maxEncodedBranchReviewBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create branch review directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect branch review log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open branch review log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append branch review: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append branch review: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync branch review log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close branch review log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded branch review in the order it was recorded. A log
// that does not exist yet is a product whose branches have never been reviewed,
// which is not a failure to read.
func (s *BranchReviewStore) List() ([]BranchReview, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open branch review log: %w", err)
	}
	defer file.Close()

	var reviews []BranchReview
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedBranchReviewBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeBranchReview([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode branch review log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode branch review log: %w", err)
		}
		reviews = append(reviews, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read branch review log: %w", err)
	}
	return reviews, nil
}

func decodeBranchReview(data []byte) (BranchReview, error) {
	var decoded BranchReview
	if err := json.Unmarshal(data, &decoded); err != nil {
		return BranchReview{}, err
	}
	return decoded, nil
}

func encodeBranchReview(reviewed BranchReview) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(reviewed); err != nil {
		return nil, fmt.Errorf("encode branch review: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *BranchReviewStore) validate(reviewed BranchReview) error {
	if reviewed.ProductID != s.productID {
		return fmt.Errorf("branch review product %q does not match store product %q", reviewed.ProductID, s.productID)
	}
	return reviewed.Validate()
}
