//go:build darwin

package console

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// TestARealTerminalIsRecognizedAndHandedBack is the only test that touches a
// real terminal. Everything else drives the console over pipes, which proves
// what it draws but not that the descriptor underneath it was ever put into the
// state the drawing assumes; this puts a pseudo-terminal into cbreak mode and
// takes it out again, because leaving an operator's shell in a state they have
// to run stty to get out of is the one failure here nobody would forgive.
//
// It skips where a pseudo-terminal cannot be opened, which includes sandboxed
// builds. That is the honest outcome: no terminal was available to test.
func TestARealTerminalIsRecognizedAndHandedBack(t *testing.T) {
	t.Parallel()

	terminal, close := openPseudoTerminal(t)
	defer close()

	if !isTerminal(terminal.Fd()) {
		t.Fatal("a pseudo-terminal was not recognized as one")
	}
	if width := terminalWidth(terminal.Fd()); width < minimumColumns {
		t.Fatalf("terminal width = %d, want at least %d", width, minimumColumns)
	}
	// A file is not a terminal, which is what keeps cursor control out of a
	// redirected transcript.
	plainFile, err := os.CreateTemp(t.TempDir(), "transcript")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer plainFile.Close()
	if isTerminal(plainFile.Fd()) || isTerminalStream(plainFile) {
		t.Fatal("a file was taken for a terminal")
	}

	var before syscall.Termios
	if err := ioctl(terminal.Fd(), readTermios, unsafe.Pointer(&before)); err != nil {
		t.Fatalf("read terminal settings: %v", err)
	}
	restore, err := enterCBreak(terminal.Fd())
	if err != nil {
		t.Fatalf("enterCBreak() error = %v", err)
	}
	var raw syscall.Termios
	if err := ioctl(terminal.Fd(), readTermios, unsafe.Pointer(&raw)); err != nil {
		t.Fatalf("read terminal settings: %v", err)
	}
	if raw.Lflag&syscall.ECHO != 0 || raw.Lflag&syscall.ICANON != 0 {
		t.Fatalf("the terminal still echoes or buffers lines: %#v", raw.Lflag)
	}
	// The signal characters are deliberately left alone, so Ctrl-C interrupts
	// the conversation exactly as it did before any of this existed.
	if raw.Lflag&syscall.ISIG == 0 {
		t.Fatal("the terminal no longer sends signals")
	}
	if err := restore(); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	var after syscall.Termios
	if err := ioctl(terminal.Fd(), readTermios, unsafe.Pointer(&after)); err != nil {
		t.Fatalf("read terminal settings: %v", err)
	}
	if after != before {
		t.Fatalf("the terminal was handed back in a different state:\n%#v\n%#v", after, before)
	}
}

// openPseudoTerminal opens one terminal a test can hold both ends of.
func openPseudoTerminal(t *testing.T) (*os.File, func()) {
	t.Helper()

	primary, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal is available here: %v", err)
	}
	for _, request := range []uintptr{syscall.TIOCPTYGRANT, syscall.TIOCPTYUNLK} {
		if err := ioctl(primary.Fd(), request, nil); err != nil {
			primary.Close()
			t.Skipf("this pseudo-terminal cannot be prepared: %v", err)
		}
	}
	var name [128]byte
	if err := ioctl(primary.Fd(), syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0])); err != nil {
		primary.Close()
		t.Skipf("this pseudo-terminal will not name itself: %v", err)
	}
	path := strings.TrimRight(string(name[:]), "\x00")
	replica, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		primary.Close()
		t.Skipf("this pseudo-terminal cannot be opened: %v", err)
	}
	return replica, func() {
		replica.Close()
		primary.Close()
	}
}
