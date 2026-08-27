//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

import (
	"context"
	"errors"
	"os"
)

func lockStateFile(context.Context, *os.File) error {
	return errors.New("cross-process run reservation locking is unsupported on this platform")
}

func tryLockStateFile(*os.File) (bool, error) {
	return false, errors.New("cross-process run locking is unsupported on this platform")
}

// unlockStateFile has nothing to drop: no lock is ever taken on this platform,
// so no lease exists to release.
func unlockStateFile(*os.File) error {
	return nil
}

func closeStateFile(file *os.File) error {
	return file.Close()
}
