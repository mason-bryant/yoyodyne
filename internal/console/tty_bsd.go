//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package console

import "syscall"

// The BSD terminals name the same two operations differently from Linux, which
// is the whole of the difference between them here.
const (
	readTermios  = syscall.TIOCGETA
	writeTermios = syscall.TIOCSETA
)
