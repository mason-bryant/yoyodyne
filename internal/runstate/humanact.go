package runstate

// A person's recorded act, and the only thing that ever passes a human gate.
//
// Everything else the harness keeps is a record of something the harness did.
// This is the one record of something it cannot do: a named person saying they
// took a step, written by them at the command line. It exists because the
// alternative encoding — an item somebody closes — is one machinery can satisfy,
// and did, which is how a parity soak the operator had reserved for themselves
// was passed by a run closing the item that reserved it.
//
// So the record is deliberately about the gate and about nothing else. It names
// no run and no work item, because a gate is not an event in a run: an act
// recorded against a run would be discharged again the next time the same
// question came round, and an act recorded against a work item would go back to
// being a fact about an item's lifecycle, which is what closure already is. What
// it names is the gate, the person, and what they say they did.
//
// Recording is exclusive. A gate already discharged is refused rather than
// overwritten: the first act is the one that passed the gate, and a second write
// would replace whose signature is on it with whoever typed last.
//
// Nothing in the pipeline writes one. No registered action produces one, no
// closure implies one, and the store offers no verb that derives one — which is
// what "satisfiable only by a recorded human act" amounts to once it is code
// rather than a rule somebody remembers. `internal/cli` is the door, and a
// conformance test holds the store to having only that one.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
)

// HumanActSchemaVersion is 1 and has never changed.
const HumanActSchemaVersion = 1

const (
	// MaxHumanActPersonBytes bounds who recorded the act, and
	// MaxHumanActStatementBytes bounds what they say they did. Both are generous
	// for the name and the sentence each actually is, and small enough that
	// nothing can push a document into the record.
	MaxHumanActPersonBytes    = 200
	MaxHumanActStatementBytes = 2 << 10
)

// humanActSuffix keeps a recorded act apart from the runs and the workflow
// instances it sits beside in the same directory.
const humanActSuffix = ".human-act.json"

// HumanAct is one person's record that they took the step a gate reserved for
// them.
type HumanAct struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Gate is the gate this act passes, by the name the declaration gives it.
	Gate string `json:"gate"`
	// Person is who took the step. It is required, and it is the whole reason
	// this record is worth more than a flag: a gate passed by nobody in
	// particular is a gate passed by whatever wrote the flag.
	Person string `json:"person"`
	// Statement is what they say they did, in their own words. It is required
	// too, because an act with no account of it is a signature on a blank page.
	Statement  string    `json:"statement"`
	RecordedAt time.Time `json:"recorded_at"`
}

func (a HumanAct) Validate() error {
	var problems []error
	if a.SchemaVersion != HumanActSchemaVersion {
		problems = append(problems, fmt.Errorf("human act schema version %d is not supported", a.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(a.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if problem := humangate.NameProblem(a.Gate); problem != nil {
		problems = append(problems, problem)
	}
	if strings.TrimSpace(a.Person) == "" {
		problems = append(problems, errors.New("the act names nobody; a gate is passed by a person and the record says which"))
	} else if len(a.Person) > MaxHumanActPersonBytes {
		problems = append(problems, fmt.Errorf("who recorded the act is %d bytes against a limit of %d", len(a.Person), MaxHumanActPersonBytes))
	}
	if strings.TrimSpace(a.Statement) == "" {
		problems = append(problems, errors.New("the act says nothing about what was done; an act nobody described is one nobody can check afterwards"))
	} else if len(a.Statement) > MaxHumanActStatementBytes {
		problems = append(problems, fmt.Errorf("what was done is %d bytes against a limit of %d", len(a.Statement), MaxHumanActStatementBytes))
	}
	if !utf8.ValidString(a.Person) || !utf8.ValidString(a.Statement) {
		problems = append(problems, errors.New("the act is not valid UTF-8"))
	}
	if a.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded at is required"))
	}
	return errors.Join(problems...)
}

// RecordHumanAct makes one person's act durable, refusing a gate that is already
// discharged.
//
// The refusal is not tidiness. The record is who passed the gate, so a second
// write would replace one person's account with another's, and the gate would
// afterwards read as having been passed by whoever typed most recently. An
// operator who meant to correct one has to say so out loud, which is a
// conversation rather than a command.
func (s *Store) RecordHumanAct(act HumanAct) error {
	if act.ProductID != s.productID {
		return fmt.Errorf("human act product %q does not match store product %q", act.ProductID, s.productID)
	}
	if err := act.Validate(); err != nil {
		return err
	}
	path, err := s.humanActPath(act.Gate)
	if err != nil {
		return err
	}
	if existing, recorded, err := s.HumanAct(act.Gate); err != nil {
		return err
	} else if recorded {
		return fmt.Errorf("the gate %q was already passed by %s at %s; the act that passed it is the one on the record, and replacing it would change whose it was",
			act.Gate, existing.Person, existing.RecordedAt.UTC().Format(time.RFC3339))
	}
	if err := createJSONFile(s.root, path, "human act", act); err != nil {
		return err
	}
	return syncDirectory(s.root)
}

// HumanAct is the act that passed one gate, and whether one has been recorded at
// all. No record is the ordinary answer and means the gate still holds, which is
// why it is reported as an absence rather than as a failure to look. A record
// that cannot be read is neither: it is an error, because a gate nobody can read
// the act for must never be treated as passed.
func (s *Store) HumanAct(gate string) (HumanAct, bool, error) {
	path, err := s.humanActPath(gate)
	if err != nil {
		return HumanAct{}, false, err
	}
	act, err := readHumanAct(path)
	if errors.Is(err, os.ErrNotExist) {
		return HumanAct{}, false, nil
	}
	if err != nil {
		return HumanAct{}, false, err
	}
	if act.Gate != gate {
		return HumanAct{}, false, fmt.Errorf("the recorded act filed under %q is about the gate %q", gate, act.Gate)
	}
	return act, true, nil
}

// HumanActRecorded reports whether the act that passes one gate is on the
// record. It is the whole of what something deciding whether to proceed needs,
// and deliberately the whole of what it is offered: who acted and what they said
// belong to whoever is reading the gate rather than to whatever is held by it.
func (s *Store) HumanActRecorded(gate string) (bool, error) {
	_, recorded, err := s.HumanAct(gate)
	return recorded, err
}

// HumanActs is every act recorded for this product, by gate name. It is what a
// queue and a status reading ask once rather than asking per gate, so that one
// reading of the backlog sees one set of discharged gates.
func (s *Store) HumanActs() ([]HumanAct, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the recorded human acts: %w", err)
	}
	var acts []HumanAct
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), humanActSuffix) {
			continue
		}
		act, err := readHumanAct(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		acts = append(acts, act)
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].Gate < acts[j].Gate })
	return acts, nil
}

// DischargedGates is the gates a person has passed, by name, in sorted order. It
// is the form every reader of the backlog wants: what a gate says the person has
// to do belongs to whoever declared it, and what this answers is only which of
// them have been done.
func (s *Store) DischargedGates() ([]string, error) {
	acts, err := s.HumanActs()
	if err != nil {
		return nil, err
	}
	discharged := make([]string, 0, len(acts))
	for _, act := range acts {
		discharged = append(discharged, act.Gate)
	}
	return discharged, nil
}

func readHumanAct(path string) (HumanAct, error) {
	file, err := os.Open(path)
	if err != nil {
		// The not-exist case is the ordinary one and is passed through unwrapped so
		// a caller can still recognize it.
		if errors.Is(err, os.ErrNotExist) {
			return HumanAct{}, err
		}
		return HumanAct{}, fmt.Errorf("open human act: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var act HumanAct
	if err := decoder.Decode(&act); err != nil {
		return HumanAct{}, fmt.Errorf("decode human act %s: %w", filepath.Base(path), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return HumanAct{}, fmt.Errorf("decode human act %s: %w", filepath.Base(path), err)
	}
	if err := act.Validate(); err != nil {
		return HumanAct{}, fmt.Errorf("read human act %s: %w", filepath.Base(path), err)
	}
	return act, nil
}

// humanActPath names where one gate's act is recorded. The gate name is held to
// the shape every gate name has, which is also what keeps it a file name in this
// directory rather than a path reaching out of it.
func (s *Store) humanActPath(gate string) (string, error) {
	if problem := humangate.NameProblem(gate); problem != nil {
		return "", problem
	}
	return filepath.Join(s.root, gate+humanActSuffix), nil
}
