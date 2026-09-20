package console

// A paste arrives at the console exactly as typing does unless the terminal is
// told otherwise: byte by byte, with each newline in it the same carriage return
// that return sends. So a paste of three lines used to send the first as the
// message and spill the other two into the prompts that followed. Bracketed
// paste is the terminal's answer to that. Asked for it, the terminal wraps what
// is pasted in a pair of markers, and everything between them is one block the
// console takes whole, its newlines and all, rather than a run of keystrokes.
//
// It is asked for rather than negotiated, unlike the keyboard: there is no
// question a terminal answers about it, and one that does not have the mode
// ignores the request and goes on sending a paste as keystrokes, which is no
// worse than before. kitty, xterm, and Terminal.app all have it.

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

const (
	// pasteOn asks the terminal to bracket what is pasted, and pasteOff stops it
	// doing so. The mode is turned on for the life of the conversation and off
	// whenever the terminal changes hands, exactly as the keyboard is, so the
	// shell that gets the terminal back is not left with markers it did not ask
	// for around its own pastes.
	pasteOn  = "\x1b[?2004h"
	pasteOff = "\x1b[?2004l"
	// pasteBegin and pasteEnd are the markers the terminal puts around a paste.
	pasteBegin = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// decodePaste reads a bracketed paste from the start of the buffer. It reports
// whether the buffer opened with one at all — anything else is left to the
// escape decoding that would otherwise have read it — and, where it did, how
// many bytes the whole of it took and whether the whole of it has arrived. A
// paste whose closing marker has not turned up yet waits for it rather than
// being applied in halves: the terminal writes the markers around the block it
// was given, so the end is coming. One that has not come by the time a whole
// message's worth has arrived is taken for a marker nobody will close, for the
// reason an escape that goes on too long is: the opening marker is dropped and
// what followed it is read as the keystrokes it would have been without it,
// rather than holding every keystroke after it for good.
func decodePaste(buffer []byte) (key, int, bool, bool) {
	if !bytes.HasPrefix(buffer, []byte(pasteBegin)) {
		return key{}, 0, false, false
	}
	body := buffer[len(pasteBegin):]
	end := bytes.Index(body, []byte(pasteEnd))
	if end < 0 {
		if len(body) > MaxLineBytes {
			return key{code: keyIgnored}, len(pasteBegin), true, true
		}
		return key{}, 0, false, true
	}
	size := len(pasteBegin) + end + len(pasteEnd)
	return key{code: keyPaste, text: pastedText(body[:end])}, size, true, true
}

// pastedText is what a pasted block becomes in the message. The line structure
// is kept exactly, whichever way the terminal spelled it: a terminal sends the
// newlines of a paste as carriage returns, as a line feed, or as the pair, and
// every one of those is the one newline the operator sees. Everything else that
// is not text is not kept, because it is not something the region can draw. A
// tab becomes a space rather than nothing, since dropping it would run two words
// together; the region measures a rune as one column and a tab is as many as
// the terminal decides, so kept as it was it would leave the erase a row short.
// Other control characters, and the escape that would start a sequence, are
// dropped as they are when typed, and a byte that is not text at all is dropped
// with them.
func pastedText(pasted []byte) string {
	var text strings.Builder
	text.Grow(len(pasted))
	for index := 0; index < len(pasted); {
		character, size := utf8.DecodeRune(pasted[index:])
		index += size
		switch {
		case character == '\r':
			if index < len(pasted) && pasted[index] == '\n' {
				index++
			}
			text.WriteByte('\n')
		case character == '\n':
			text.WriteByte('\n')
		case character == '\t':
			text.WriteByte(' ')
		case character < 0x20, character == 0x7f:
		case character == utf8.RuneError && size == 1:
		default:
			text.WriteRune(character)
		}
	}
	return text.String()
}
