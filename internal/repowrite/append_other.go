//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package repowrite

import "os"

// Refusing to follow a link at the final component is O_NOFOLLOW on the Unix
// hosts Yoyodyne supports. Elsewhere the resolution above is the whole of the
// answer, which leaves the link-planted-after-the-check case open on a platform
// this harness is not run on.
const appendFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
