package runstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// holderStamp is what a process holding a lease writes down beside it so the
// hold can be observed without being taken. Conversations and runs keep one;
// the lock itself answers only whoever tries to take it, and taking it to ask
// is, for the instant it lasts, indistinguishable from a second holder.
type holderStamp struct {
	// PID is the process that took the hold. It is what makes the stamp
	// self-correcting: a holder that exits without releasing leaves the file
	// behind, and a reader that finds no such process reports what the operating
	// system already decided when it dropped the lock.
	PID int `json:"pid"`
	// HeldAt is when the hold was taken. Nothing decides from it, and it is
	// written so that a state directory somebody is reading by hand says when.
	HeldAt time.Time `json:"held_at"`
}

// stampHolder writes this process's stamp for a lease it now holds, at path
// inside dir. It is replaced by rename rather than written in place, so a
// reader sees the whole of one stamp or none of it and never half of one. The
// label names what is held, for the failure.
func stampHolder(dir, path, label string) error {
	temporary, err := os.CreateTemp(dir, ".holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary %s holder: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary %s holder: %w", label, err)
	}
	holder := holderStamp{PID: os.Getpid(), HeldAt: time.Now().UTC()}
	if err := writeJSONFile(temporary, label+" holder", holder); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s holder: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s holder: %w", label, err)
	}
	return syncDirectory(dir)
}

// readHolder is one stamp as it sits on disk. A file that is not there is
// reported as ErrNotExist for the caller to read as an unheld lease; a file
// that is there and will not decode is a failure to answer, because a reader
// that guessed at it would be inventing whether somebody holds the thing.
//
// That is why this is the strict door and stays there while the listings beside
// it move to the tolerant one. A stamp carrying something this build does not
// know is a stamp a different build wrote, and what the field might say is
// exactly what this has to decide from; the caller reports the refusal rather
// than being handed an answer about who holds what.
func readHolder(path, label string) (holderStamp, error) {
	file, err := os.Open(path)
	if err != nil {
		return holderStamp{}, fmt.Errorf("open the %s holder: %w", label, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var holder holderStamp
	if err := decoder.Decode(&holder); err != nil {
		return holderStamp{}, fmt.Errorf("decode the %s holder: %w", label, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return holderStamp{}, fmt.Errorf("decode the %s holder: %w", label, err)
	}
	if holder.PID <= 0 {
		return holderStamp{}, fmt.Errorf("the %s holder names no process", label)
	}
	return holder, nil
}

// holderRunning reports whether the stamp at path names a process that is
// running, and takes nothing to answer it. No stamp is no holder.
//
// Two things it cannot see are worth stating. A stamp whose process identifier
// has been reused by an unrelated process reads as held until the next hold
// rewrites it, which is something reported busy rather than something anybody
// is locked out of. And a holder from a build older than the stamp wrote none,
// so its hold reads as free until it ends.
func holderRunning(path, label string) (bool, error) {
	holder, err := readHolder(path, label)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	running, err := processIsRunning(holder.PID)
	if err != nil {
		return false, fmt.Errorf("ask whether the %s holder is running: %w", label, err)
	}
	return running, nil
}
