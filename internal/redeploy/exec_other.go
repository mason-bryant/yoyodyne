//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package redeploy

import "errors"

// imageReplacement is false where a process cannot be replaced by another binary
// in place. It is answered when a session opens rather than at the end, so a
// session there watches exactly as it always did and simply never redeploys —
// rather than draining itself first and only then finding out that the platform
// cannot do the thing it drained for.
const imageReplacement = false

func replaceImage(string, []string, []string) error {
	return errors.New("replacing the running process image is unsupported on this platform")
}
