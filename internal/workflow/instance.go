package workflow

import (
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

// maxEncodedInstanceBytes bounds one instance record, writer and reader sharing
// the bound so a record that was written is a record that can be read back.
const maxEncodedInstanceBytes = 1 << 20

// Instance is one run of one compiled graph, durably: which definition it is
// running, where it stands now, and every boundary it has crossed to get there.
//
// The record is the position rather than a copy of one held in memory. An
// executor reloads it before every transition and writes it before beginning the
// next, so a process that dies leaves behind the answer to where its successor
// starts — and that answer is a state boundary, never the middle of an action.
//
// It is pinned to a digest from creation to terminal, which is the whole of what
// "in-flight work is never silently migrated" amounts to here: the definition
// file may be edited, replaced, or deleted, and this record still names the one
// thing this instance is running. An executor holding any other graph is refused.
type Instance struct {
	// ID is what this instance is recorded and resumed under, and the name of the
	// file it is stored in.
	ID string `json:"id"`
	// WorkflowID is the workflow being run, by the name a binding selects it
	// under. It is here beside the digest because the pair is what says what an
	// instance is running and which version of it.
	WorkflowID string `json:"workflow_id"`
	// Schema is the format version the pinned definition was read under.
	Schema int `json:"schema"`
	// Digest is the content address of the definition this instance is pinned to.
	// It is written once, at creation, and never changes.
	Digest string `json:"digest"`
	// State is where the instance stands: the state whose action is performed
	// next, or the terminal it ended in.
	State string `json:"state"`
	// Terminal is whether State is a terminal, in which case the instance is over
	// and nothing further is performed.
	Terminal bool `json:"terminal"`
	// Checkpoints is every boundary this instance has crossed, oldest first,
	// beginning with the initial state it was created in. It is history rather
	// than a log: each entry is a position that was durable, so reading them in
	// order is reading exactly the path the instance took.
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// Checkpoint is one state boundary: where the instance arrived, what brought it
// there, and when.
type Checkpoint struct {
	// Sequence is this checkpoint's position in the history, counting from zero at
	// the initial state.
	Sequence int `json:"sequence"`
	// State is where the instance stood once this checkpoint was durable.
	State string `json:"state"`
	// Terminal is whether that state is a terminal.
	Terminal bool `json:"terminal"`
	// From is the state whose action produced the transition into this one, and is
	// empty on the initial checkpoint, which nothing transitioned into.
	From string `json:"from,omitempty"`
	// Outcome is what the action of From produced, and is empty for the same
	// reason From is.
	Outcome string `json:"outcome,omitempty"`
	// At is when this checkpoint was recorded.
	At time.Time `json:"at"`
}

// Done reports whether this instance has reached a terminal, which is the one
// question a caller asks before deciding whether to step it again.
func (i Instance) Done() bool { return i.Terminal }

// Position is the checkpoint the instance currently stands on: the last one
// recorded. A record with no checkpoints is a record that was never created
// here, so the caller is told so rather than handed a zero checkpoint.
func (i Instance) Position() (Checkpoint, bool) {
	if len(i.Checkpoints) == 0 {
		return Checkpoint{}, false
	}
	return i.Checkpoints[len(i.Checkpoints)-1], true
}

// Path is every state and terminal this instance has stood in, in the order it
// stood in them. It is what somebody asking "what did this instance do" reads,
// and a state it went round a loop into appears once per visit.
func (i Instance) Path() []string {
	walked := make([]string, 0, len(i.Checkpoints))
	for _, checkpoint := range i.Checkpoints {
		walked = append(walked, checkpoint.State)
	}
	return walked
}

// Validate refuses a record that could not have been written by an executor.
//
// It is checked on the way out as well as on the way in, because a record read
// back is a record something is about to act on: an instance whose history
// disagrees with its position would resume somewhere neither of them says.
func (i Instance) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("workflow instance id", i.ID); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(i.WorkflowID) == "" {
		problems = append(problems, errors.New("the instance names no workflow; a record has to say what it is running"))
	}
	if i.Schema != SchemaVersion {
		problems = append(problems, fmt.Errorf("the instance was written under schema version %d; this build reads version %d", i.Schema, SchemaVersion))
	}
	if !strings.HasPrefix(i.Digest, DigestPrefix) {
		problems = append(problems, fmt.Errorf("the instance is pinned to %q, which is not a workflow digest; an instance pinned to nothing is one any definition could claim", i.Digest))
	}
	if strings.TrimSpace(i.State) == "" {
		problems = append(problems, errors.New("the instance stands in no state; a record has to say where it is"))
	}
	if len(i.Checkpoints) == 0 {
		problems = append(problems, errors.New("the instance has no checkpoints; every instance is created standing on its initial state"))
		return errors.Join(problems...)
	}
	for index, checkpoint := range i.Checkpoints {
		if checkpoint.Sequence != index {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d is numbered %d; checkpoints are the boundaries crossed, in the order they were crossed", index, checkpoint.Sequence))
		}
		if strings.TrimSpace(checkpoint.State) == "" {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d stands in no state", index))
		}
		if index == 0 && (checkpoint.From != "" || checkpoint.Outcome != "") {
			problems = append(problems, fmt.Errorf("the initial checkpoint arrived from %q on %q; nothing transitioned into it", checkpoint.From, checkpoint.Outcome))
		}
		if index > 0 && (checkpoint.From == "" || checkpoint.Outcome == "") {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d does not say what state and what outcome brought the instance there", index))
		}
		if index > 0 && checkpoint.From != i.Checkpoints[index-1].State {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d arrived from %q and the one before it stood in %q; the history skips a boundary", index, checkpoint.From, i.Checkpoints[index-1].State))
		}
	}
	// The position and the history are two statements of one fact, and a record
	// whose two disagree is one nothing can safely resume.
	last := i.Checkpoints[len(i.Checkpoints)-1]
	if last.State != i.State || last.Terminal != i.Terminal {
		problems = append(problems, fmt.Errorf("the instance stands in %q (terminal %t) and its last checkpoint stands in %q (terminal %t)", i.State, i.Terminal, last.State, last.Terminal))
	}
	return errors.Join(problems...)
}

// InstanceStore is where instance records live: one file per instance, under one
// directory.
//
// It is deliberately small. Writing is atomic and durable — a record is written
// whole and replaced by rename, and the rename is what makes a checkpoint the
// thing a killed process leaves behind rather than half a file — and reading
// refuses anything this build does not recognize. What it does not do is decide
// anything about what it holds: which process may act on an instance, and what
// happens to one nobody is acting on, are the lease and the reconciliation the
// design describes, and neither is here.
type InstanceStore struct {
	root string
}

// NewInstanceStore holds a store to one directory, which every record it writes
// is inside. The path is absolute because a store rooted at a relative path is
// rooted wherever a process happened to be standing.
func NewInstanceStore(root string) (*InstanceStore, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("workflow instance root %q must be an absolute path", root)
	}
	return &InstanceStore{root: filepath.Clean(root)}, nil
}

// Root is the directory this store keeps its records in.
func (s *InstanceStore) Root() string { return s.root }

// Create records an instance that does not exist yet, refusing one that does. It
// is exclusive because two instances under one identifier are two positions in
// one workflow, and a resume would not say which of them it meant.
func (s *InstanceStore) Create(instance Instance) error {
	if err := instance.Validate(); err != nil {
		return fmt.Errorf("create workflow instance: %w", err)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create workflow instance directory: %w", err)
	}
	path, err := s.instancePath(instance.ID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create workflow instance: %s already exists", instance.ID)
		}
		return fmt.Errorf("create workflow instance: %w", err)
	}
	if err := writeInstanceFile(file, instance); err != nil {
		return errors.Join(err, file.Close(), removeFailedInstance(path))
	}
	if err := file.Close(); err != nil {
		return errors.Join(fmt.Errorf("close workflow instance: %w", err), removeFailedInstance(path))
	}
	if err := syncInstanceDirectory(s.root); err != nil {
		return errors.Join(err, removeFailedInstance(path))
	}
	return nil
}

// Save replaces the record of an instance that already exists.
//
// The replacement is a write to a temporary file and a rename over the record,
// so a reader either sees the checkpoint before this one or the checkpoint this
// one records, and never a record half written by a process that died during it.
func (s *InstanceStore) Save(instance Instance) error {
	if err := instance.Validate(); err != nil {
		return fmt.Errorf("save workflow instance: %w", err)
	}
	path, err := s.instancePath(instance.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("save workflow instance %s: %w", instance.ID, err)
	}
	temporary, err := os.CreateTemp(s.root, ".instance-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary workflow instance: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure temporary workflow instance: %w", err), temporary.Close())
	}
	if err := writeInstanceFile(temporary, instance); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary workflow instance: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace workflow instance %s: %w", instance.ID, err)
	}
	return syncInstanceDirectory(s.root)
}

// Load reads one record back, refusing anything this build does not recognize:
// an unknown field is a record written by a different version of this code, and
// reading it as if the field were not there is resuming an instance whose state
// was partly ignored.
func (s *InstanceStore) Load(id string) (Instance, error) {
	path, err := s.instancePath(id)
	if err != nil {
		return Instance{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Instance{}, fmt.Errorf("open workflow instance %s: %w", id, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Instance{}, fmt.Errorf("stat workflow instance %s: %w", id, err)
	}
	if info.Size() > maxEncodedInstanceBytes {
		return Instance{}, fmt.Errorf("workflow instance %s is %d bytes, limit is %d", id, info.Size(), maxEncodedInstanceBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedInstanceBytes))
	decoder.DisallowUnknownFields()
	var instance Instance
	if err := decoder.Decode(&instance); err != nil {
		return Instance{}, fmt.Errorf("decode workflow instance %s: %w", id, err)
	}
	if err := ensureInstanceEOF(decoder); err != nil {
		return Instance{}, fmt.Errorf("decode workflow instance %s: %w", id, err)
	}
	if instance.ID != id {
		return Instance{}, fmt.Errorf("the workflow instance file for %s holds the instance %s", id, instance.ID)
	}
	if err := instance.Validate(); err != nil {
		return Instance{}, fmt.Errorf("read workflow instance %s: %w", id, err)
	}
	return instance, nil
}

// instancePath is where one record lives. The identifier is held to the shape
// every identifier in this harness has, which is also what keeps it a file name
// inside this store rather than a path reaching out of it.
func (s *InstanceStore) instancePath(id string) (string, error) {
	if err := domain.ValidateIdentifier("workflow instance id", id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, id+".json"), nil
}

// writeInstanceFile writes one record and puts it on the disk. The sync is what
// a checkpoint rests on: a record the operating system has agreed to write later
// is not a boundary a killed process can be resumed from.
func writeInstanceFile(file *os.File, instance Instance) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(instance); err != nil {
		return fmt.Errorf("encode workflow instance: %w", err)
	}
	if buffer.Len() > maxEncodedInstanceBytes {
		return fmt.Errorf("encoded workflow instance is %d bytes, limit is %d", buffer.Len(), maxEncodedInstanceBytes)
	}
	written, err := file.Write(buffer.Bytes())
	if err != nil {
		return fmt.Errorf("write workflow instance: %w", err)
	}
	if written != buffer.Len() {
		return fmt.Errorf("write workflow instance: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync workflow instance: %w", err)
	}
	return nil
}

func removeFailedInstance(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove failed workflow instance: %w", err)
	}
	return nil
}

func syncInstanceDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open workflow instance directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync workflow instance directory: %w", err)
	}
	return nil
}

func ensureInstanceEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("the file holds more than one record")
		}
		return err
	}
	return nil
}
