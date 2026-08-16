//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package console

import (
	"fmt"
	"syscall"
	"unsafe"
)

// defaultColumns is the width assumed when the terminal will not say. It is the
// width terminals have been by default for long enough that guessing it is
// safer than guessing anything else.
const defaultColumns = 80

// minimumColumns keeps the arithmetic that positions the cursor away from
// widths a region cannot be drawn in at all.
const minimumColumns = 20

// winsize is the terminal's own account of its size. The syscall package
// declares the request but not the structure it fills in, so it is declared
// here, in the layout every one of these systems uses for it.
type winsize struct {
	rows    uint16
	columns uint16
	xpixels uint16
	ypixels uint16
}

func ioctl(fd, request uintptr, argument unsafe.Pointer) error {
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(argument)); err != 0 {
		return err
	}
	return nil
}

// isTerminal reports whether the descriptor is a terminal, by asking it for the
// settings only a terminal has.
func isTerminal(fd uintptr) bool {
	var settings syscall.Termios
	return ioctl(fd, readTermios, unsafe.Pointer(&settings)) == nil
}

// enterCBreak puts the terminal into cbreak mode: keystrokes arrive as they are
// typed and nothing is echoed, so the composing line is drawn rather than typed
// into and output can be put above it.
//
// It is deliberately not full raw mode. The signal characters are left alone,
// so Ctrl-C still interrupts the conversation exactly as it did before this
// existed rather than being reimplemented here; output processing is left alone
// too, so a newline is still a newline and the harness can go on writing lines.
func enterCBreak(fd uintptr) (func() error, error) {
	var original syscall.Termios
	if err := ioctl(fd, readTermios, unsafe.Pointer(&original)); err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	settings := original
	settings.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	// One byte is enough to wake a read, and nothing waits for a second, so a
	// keystroke reaches the line as it is typed.
	settings.Cc[syscall.VMIN] = 1
	settings.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, writeTermios, unsafe.Pointer(&settings)); err != nil {
		return nil, fmt.Errorf("set terminal settings: %w", err)
	}
	restored := false
	return func() error {
		if restored {
			return nil
		}
		restored = true
		return ioctl(fd, writeTermios, unsafe.Pointer(&original))
	}, nil
}

// terminalWidth asks the terminal how wide it is, every time it is drawn, so a
// window that was resized is drawn at the size it is now.
func terminalWidth(fd uintptr) int {
	var size winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&size)); err != nil || size.columns == 0 {
		return defaultColumns
	}
	if int(size.columns) < minimumColumns {
		return minimumColumns
	}
	return int(size.columns)
}
