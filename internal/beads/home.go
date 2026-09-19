package beads

// Home is the one repository Beads lives in, and the URL every document and
// remedy in this repository sends somebody to for it. The tracker's own release
// source settled which of its two names is canonical: its releases are cut
// under gastownhall, its installer fetches from there, and its own documents
// say the repository moved there from steveyegge/beads -- which GitHub still
// redirects, so the old name resolves, and still sends a newcomer to a name
// nothing is released under. `docs/diagnoses/yoyodyne-ifd-125-6-beads-home.md`
// is the evidence. TestEveryBeadsHomeThisRepositoryNamesIsTheCanonicalOne holds
// every tracked file to this constant, so a second spelling cannot creep back
// into the install path.
const Home = "https://github.com/gastownhall/beads"

// InstallCommand is what a machine with no bd on it is told to run: the
// tracker's own installer, served from Home. It is the tracker's own leading
// install route, and it is the remedy rather than `go install` because the
// go-install form is the one route that cannot be spelled with Home at all --
// the released modules still declare ModulePath, so `go install` of a path under
// Home fails on the mismatch -- and because a bare `go install` of the module
// path takes a CGO and ICU build the upstream says it does not support, which
// is what turned every pull request here red on 2026-09-05.
const InstallCommand = "curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash"

// ModulePath is the Go module path the tracker's released tags still declare,
// kept for compatibility with them rather than as a second home. It is here so
// the one place the old owner is legitimately named is named once, with the
// reason beside it; nothing in this repository installs bd through it.
const ModulePath = "github.com/steveyegge/beads"
