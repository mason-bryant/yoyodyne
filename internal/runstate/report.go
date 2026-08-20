package runstate

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

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// maxEncodedReportBytes bounds one encoded report, including the trailing
// newline. The writer and the reader share it, so a report that was written is
// always one that can be read back.
const maxEncodedReportBytes = 64 << 10

// ReportStore is where what agents noticed is collected, in the same
// operating-system state root as runs and conversations and beside them rather
// than among them. It is one append-only log per product because a report
// outlives the run or conversation that produced it: a run's state is settled
// and cleaned up, and what it reported is still the operator's to read.
type ReportStore struct {
	root      string
	productID domain.ProductID
	// reading is how strictly lines are decoded, for the reason the run store
	// carries one: a reader of other processes' output takes a tolerant view.
	reading
}

func NewReportStore(root string, productID domain.ProductID) (*ReportStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ReportStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *ReportStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the collected reports
// actually are.
func (s *ReportStore) Path() string { return filepath.Join(s.root, "reports.jsonl") }

// Append records one report durably. It is an append rather than a rewrite for
// the same reason the event logs are: a report is written once and never
// revised, and two processes reporting at the same time must not overwrite each
// other's record.
func (s *ReportStore) Append(reported report.Report) error {
	if err := s.validate(reported); err != nil {
		return err
	}
	encoded, err := encodeReport(reported)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedReportBytes {
		return fmt.Errorf("encoded report is %d bytes, limit is %d", len(encoded), maxEncodedReportBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect report log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open report log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append report: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append report: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync report log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close report log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every collected report in the order it was recorded. A log that
// does not exist yet is a product nothing has reported about, which is not a
// failure to read.
func (s *ReportStore) List() ([]report.Report, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open report log: %w", err)
	}
	defer file.Close()

	var reports []report.Report
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedReportBytes)
	number := 0
	for scanner.Scan() {
		number++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeReport([]byte(line))
		if err == nil {
			err = s.validate(decoded)
		}
		if err != nil {
			// A reader stops at a line it cannot read rather than carrying on past
			// it: the position it keeps counts records, so a line read past is one
			// lost as soon as a line behind it is posted.
			if s.stopAtLine("the report log", number, err) {
				break
			}
			return nil, fmt.Errorf("decode report log: %w", err)
		}
		reports = append(reports, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read report log: %w", err)
	}
	return reports, nil
}

func decodeReport(data []byte) (report.Report, error) {
	var decoded report.Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return report.Report{}, err
	}
	if err := decoded.Validate(); err != nil {
		return report.Report{}, err
	}
	return decoded, nil
}

func encodeReport(reported report.Report) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(reported); err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *ReportStore) validate(reported report.Report) error {
	if reported.ProductID != s.productID {
		return fmt.Errorf("report product %q does not match store product %q", reported.ProductID, s.productID)
	}
	return reported.Validate()
}
