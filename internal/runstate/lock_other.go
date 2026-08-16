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
