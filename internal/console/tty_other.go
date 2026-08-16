//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package console

import "errors"

// Where the terminal cannot be addressed, the conversation is the plain stream
// of text. That is the honest degradation: no cursor control is written, and
// nothing pretends the composing line has a region of its own.

func isTerminal(uintptr) bool { return false }

func enterCBreak(uintptr) (func() error, error) {
	return nil, errors.New("this platform has no terminal control")
}

func terminalWidth(uintptr) int { return 80 }
