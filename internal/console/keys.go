package console

import "unicode/utf8"

// keyCode is what one keystroke means to the line being composed. The set is
// deliberately small: this edits one line of prose, and anything it does not
// understand is ignored rather than inserted, because a stray escape sequence
// in the middle of a message to the product manager is worse than a keystroke
// that did nothing.
type keyCode int

const (
	keyIgnored keyCode = iota
	keyRune
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyKillLine
	keyKillWord
)

type key struct {
	code  keyCode
	value rune
}

// maxEscapeBytes bounds how long an escape sequence may be before it is taken
// for rubbish. Without it, a terminal that sends an escape and stops would hold
// every keystroke after it.
const maxEscapeBytes = 16

// decodeKey reads the first keystroke out of the buffer. It reports how many
// bytes it consumed, and whether the buffer held a whole keystroke at all: a
// rune or an escape sequence split across two reads is not one, and waits for
// the rest rather than being applied in halves.
//
// The terminal is left in cbreak rather than raw mode, so the signal keys are
// still the terminal's: Ctrl-C interrupts the process as it always did, and
// nothing here has to reimplement that badly.
func decodeKey(buffer []byte) (key, int, bool) {
	if len(buffer) == 0 {
		return key{}, 0, false
	}
	switch buffer[0] {
	case '\r', '\n':
		return key{code: keyEnter}, 1, true
	case 0x7f, 0x08:
		return key{code: keyBackspace}, 1, true
	case 0x01:
		return key{code: keyHome}, 1, true
	case 0x05:
		return key{code: keyEnd}, 1, true
	case 0x15:
		return key{code: keyKillLine}, 1, true
	case 0x17:
		return key{code: keyKillWord}, 1, true
	case 0x1b:
		return decodeEscape(buffer)
	}
	if buffer[0] < 0x20 {
		return key{code: keyIgnored}, 1, true
	}
	if !utf8.FullRune(buffer) {
		if len(buffer) < utf8.UTFMax {
			return key{}, 0, false
		}
		return key{code: keyIgnored}, 1, true
	}
	value, size := utf8.DecodeRune(buffer)
	if value == utf8.RuneError && size == 1 {
		return key{code: keyIgnored}, 1, true
	}
	return key{code: keyRune, value: value}, size, true
}

// decodeEscape reads the sequences a terminal sends for the keys that have no
// character of their own. Anything else it sends is consumed and ignored, which
// is what keeps a function key from being typed into the message.
func decodeEscape(buffer []byte) (key, int, bool) {
	if len(buffer) == 1 {
		return key{}, 0, false
	}
	if buffer[1] != '[' && buffer[1] != 'O' {
		// Escape followed by something else is not a sequence this understands.
		return key{code: keyIgnored}, 1, true
	}
	for index := 2; index < len(buffer); index++ {
		if buffer[index] < 0x40 || buffer[index] > 0x7e {
			continue
		}
		return escapeKey(string(buffer[2:index]), buffer[index]), index + 1, true
	}
	if len(buffer) >= maxEscapeBytes {
		return key{code: keyIgnored}, 1, true
	}
	return key{}, 0, false
}

func escapeKey(parameters string, final byte) key {
	switch final {
	case 'A':
		return key{code: keyUp}
	case 'B':
		// Up and down move through a list of answers where there is one. A line
		// being composed has no history to move through yet, and moving the
		// cursor off it would take the operator's text out from under them, so
		// the line editor ignores both.
		return key{code: keyDown}
	case 'C':
		return key{code: keyRight}
	case 'D':
		return key{code: keyLeft}
	case 'H':
		return key{code: keyHome}
	case 'F':
		return key{code: keyEnd}
	case '~':
		switch parameters {
		case "1", "7":
			return key{code: keyHome}
		case "3":
			return key{code: keyDelete}
		case "4", "8":
			return key{code: keyEnd}
		}
	}
	return key{code: keyIgnored}
}
