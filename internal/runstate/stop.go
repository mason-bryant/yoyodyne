package runstate

// The operator's request that one run stop.
//
// Cancelling a run this process started is a context away, and that is all
// stopping ever needed while the operator started every run themselves. It is
// not enough once work runs without them: the run they want stopped is usually
// in another process, and this one has no handle on it. Killing that process is
// the verb they would otherwise be left with, and it is the wrong one — it
// leaves a claimed item, a worktree, and a branch that nothing accounts for.
//
// So a stop is asked for rather than performed. It is written beside the run,
// for the reason a released wait is: the process working on the run holds that
// run's state under a lease and is the only thing entitled to write it, so the
// operator states the fact in a file of their own and the run reads it. The run
// honors it at its next provider-call boundary, exactly where it reads the
// operator's hold, and ends itself the way a cancelled run ends — terminal,
// artifacts preserved, the item noted — so what it leaves behind is settled by
// the same reconciliation that settles everything else.
//
// What it deliberately cannot do is interrupt a provider invocation already
// streaming. That generation is already paid for, and throwing it away would
// leave the run needing the same work again, which is the cost that made killing
// processes the wrong verb in the first place. A stop therefore takes effect at
// the next boundary rather than instantly, and whoever asked for it is told that
// it has not landed yet rather than told it has.

import (
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

// StopSchemaVersion is 1 and has never changed.
const StopSchemaVersion = 1

// MaxStopReasonBytes bounds the reason an operator gives. It is the same order
// as a selection's, and for the same reason: enough to say why, never enough to
// crowd out the run it is about.
const MaxStopReasonBytes = 4 << 10

// StopRequest is one operator statement that a run should stop. It names the
// work item as well as the run because that is what the operator typed, and a
// record nobody can trace back to the item it was about is a record nobody can
// read afterwards.
type StopRequest struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	RunID         string           `json:"run_id"`
	WorkItemID    string           `json:"work_item_id"`
	RequestedAt   time.Time        `json:"requested_at"`
	// Reason is what the operator said, and is optional: an operator who stops
	// something in a hurry owes nobody an explanation, and a stop with no reason
	// is still a stop.
	Reason string `json:"reason,omitempty"`
}

func (r StopRequest) Validate() error {
	var problems []error
	if r.SchemaVersion != StopSchemaVersion {
		problems = append(problems, fmt.Errorf("stop schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !runIDPattern.MatchString(r.RunID) {
		problems = append(problems, errors.New("run id is invalid"))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if r.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested at is required"))
	}
	if len(r.Reason) > MaxStopReasonBytes {
		problems = append(problems, fmt.Errorf("stop reason is %d bytes, which exceeds the %d byte bound", len(r.Reason), MaxStopReasonBytes))
	}
	return errors.Join(problems...)
}

// RecordStop makes one stop request durable. Asking twice is deliberately not an
// error: an operator who stops a run that has not given up yet means the same
// thing the second time, and refusing them would make the verb depend on timing
// they cannot see.
func (s *Store) RecordStop(request StopRequest) error {
	if request.ProductID != s.productID {
		return fmt.Errorf("stop product %q does not match store product %q", request.ProductID, s.productID)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.stopPath(request.RunID)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".stop-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary stop request: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary stop request: %w", err)
	}
	if err := writeJSONFile(temporary, "stop request", request); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary stop request: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace stop request: %w", err)
	}
	return syncDirectory(s.root)
}

// StopRequested reports whether the operator has asked one run to stop. No
// record is the ordinary answer and means carry on, which is why it is reported
// as an absence rather than as a failure to look. A record that cannot be read is
// neither: it is reported as an error, because a stop nobody can read must never
// be spent through as though it were absent.
//
// It is never cleared. A request names one run, and a run identifier is used
// once, so a record left behind can only ever describe the run it stopped —
// which is exactly the evidence somebody reading that run afterwards wants.
func (s *Store) StopRequested(runID string) (StopRequest, bool, error) {
	path, err := s.stopPath(runID)
	if err != nil {
		return StopRequest{}, false, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return StopRequest{}, false, nil
	}
	if err != nil {
		return StopRequest{}, false, fmt.Errorf("open stop request: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var request StopRequest
	if err := decoder.Decode(&request); err != nil {
		return StopRequest{}, false, fmt.Errorf("decode stop request %s: %w", runID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return StopRequest{}, false, fmt.Errorf("decode stop request %s: %w", runID, err)
	}
	if request.RunID != runID {
		return StopRequest{}, false, fmt.Errorf("stop request file %s belongs to run %s", runID, request.RunID)
	}
	if err := request.Validate(); err != nil {
		return StopRequest{}, false, err
	}
	return request, true, nil
}

// stopPath names where one run's stop request is recorded. It sits beside the
// run's own state rather than inside it because the working process holds that
// state's lease and the operator does not.
func (s *Store) stopPath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".stop.json"), nil
}
