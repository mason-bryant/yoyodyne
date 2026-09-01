//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package redeploy

import "errors"

// imageReplacement is false where a process cannot be replaced by another
// binary in place. A session there is refused a redeploy when it is opened
// rather than told at the end, which is the honest answer: the platform cannot
// do this, and a session that had already drained itself to find that out would
// have paid for the discovery with the queue.
const imageReplacement = false

func replaceImage(string, []string, []string) error {
	return errors.New("replacing the running process image is unsupported on this platform")
}
