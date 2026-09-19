# yoyodyne-ifd.125.6: Beads has one home, and it is gastownhall/beads

The README, the install script, and the documents beside the configuration
link `https://github.com/gastownhall/beads`; the `.beads/README.md` that `bd
init` writes links `https://github.com/steveyegge/beads`. One of the two sends
a newcomer to the wrong place, and this is which — established from the
tracker's own release source rather than from which spelling this repository
happened to use more.

## The evidence

The network is refused in a developer run, so what was read is what this
machine already holds of the tracker's releases, which is the release source
itself: the Go module cache carries `github.com/steveyegge/beads@v1.2.2`,
fetched on 2026-08-15 from `refs/tags/v1.2.2` of
`https://github.com/steveyegge/beads` (its `.info` record), and it is the
newest release the upstream has cut — its changelog opens with v1.2.2, dated
2026-08-15, and its `go.mod` retracts v1.2.1, v1.2.0, and v1.1.1 so that
`@latest` resolves to it.

What that release says about its own home, in its own files:

- **Its installer fetches from gastownhall.** `scripts/install.sh` in the
  module asks `https://api.github.com/repos/gastownhall/beads/releases/latest`
  for the version and downloads
  `https://github.com/gastownhall/beads/releases/download/<version>/beads_<version>_<os>_<arch>.tar.gz`.
  Its own usage line is
  `curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash`.
- **Its README leads with the same line**, badges its release count from
  `gastownhall/beads`, and sends documentation to
  `https://gastownhall.github.io/beads/`.
- **Its installing guide names the move outright**, at `docs/INSTALLING.md`
  line 125: *"Use the `github.com/steveyegge/beads` path for `go install`. The
  repository now lives under `gastownhall/beads`, but released Go modules still
  declare `github.com/steveyegge/beads` for compatibility."* The installer's
  own comment says the same thing at its `go install` fallback.
- **Its npm package was re-pointed for the same reason**, per its changelog:
  *"updated npm-package/package.json URLs (repository, bugs, homepage) from
  steveyegge/beads to gastownhall/beads so sigstore provenance validation
  accepts the artifact."* Provenance is checked against where the artifact was
  actually built, so this is the release pipeline itself saying where it lives.
- **Its issue references are all `gastownhall/beads#…`** — the changelog and the
  migration comments in the binary cite `gastownhall/beads#4259`, `#4380`,
  `#4566`, and so on.

And what this repository already knew: the `adoption` job in
`.github/workflows/ci.yml` downloads the pinned tracker from
`https://github.com/gastownhall/beads/releases/download/v1.2.2/…` on every
pull request, and the walkthrough it installs for runs behind it — a download
URL that is exercised on every change rather than only read.

So the home is `https://github.com/gastownhall/beads`. The old name still
resolves because GitHub redirects a renamed repository, which is why nothing
written against it has failed and why nobody noticed — but it is a redirect to
the home, not a second home, and nothing is released under it.

## Why `bd init` still writes the old name

The `.beads/README.md` in this repository was written by `bd` 1.1.2, and it
links `steveyegge/beads` because that is what the tracker's own template says:
`cmd/bd/init_templates.go` in v1.2.2 — which is, by its changelog, the v1.1.2
code under a higher number — still carries `**Learn more:**
[github.com/steveyegge/beads]` and the installer line under the old name. That
is the upstream's template lagging its own move, not a claim that the old name
is canonical; the same binary's `package.json` and `AGENTS.md` text say
gastownhall. It is bd's file, rewritten wholesale whenever `bd init` or `bd
setup` runs, so it is not edited here; the README now says the link is the
older name of the same project, and the sweep below leaves `.beads/` alone.

## The Go module path is the one place the old name is right

`go.mod` in every released tag declares `module github.com/steveyegge/beads`,
so `go install github.com/gastownhall/beads/cmd/bd@latest` — which is what
`yoyo doctor` and `yoyo setup` printed as the remedy for a missing `bd` until
this change — cannot work: Go fetches the module at the new path, finds it
declaring the old one, and refuses on the mismatch. The upstream guards its own
documents against exactly that line (`scripts/check-go-install-guidance.sh`:
*"use github.com/steveyegge/beads/cmd/bd because go.mod still declares that
module path"*), and the same guard calls a bare `go install` under either name
unsupported, because it takes a CGO and ICU build — which is what turned every
pull request here red on 2026-09-05 and why CI pins a prebuilt release instead
(`docs/developing-yoyo.md#the-tracker-version-ci-pins`).

So the remedy is not spelled with the home at all in `go install` form, and it
is not spelled with the old name either, because the supported forms of that
need a build tag and a decision about embedded versus server mode that a remedy
line is the wrong place for. The remedy is the tracker's own installer from its
home, which is what its README leads with and what produces the embedded-Dolt
binary `bd init` needs:

```sh
curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash
```

`internal/beads/home.go` holds that as `beads.InstallCommand`, the home as
`beads.Home`, and the module path as `beads.ModulePath` with the reason beside
it — the one place the old owner is named on purpose.

## What now holds it

- `beads.Home` and `beads.InstallCommand` are what `yoyo doctor` and `yoyo
  setup` print, and what the README and `docs/operations.md` quote.
- `TestEveryBeadsHomeThisRepositoryNamesIsTheCanonicalOne` sweeps every file
  the repository carries — tracked, or untracked and unignored — for a Beads
  URL under `github.com` or `raw.githubusercontent.com` and fails on any owner
  but the home's, outside `.beads/` (the tracker's own files), `docs/diagnoses/`
  (records of what was, this one included), and `internal/beads/home.go` (the
  module path, named once with its reason).
- `TestTheReadmeAndTheInstallScriptAgreeOnTheBeadsHome` states the pairwise
  claim for the two documents a newcomer reads. The install script is landing
  under yoyodyne-ifd.125.2 and already names the home; on a checkout without
  it the test holds the README on its own and says so.
- `TestTheDocumentsStateTheRemedyDoctorPrints` holds the README and the
  operations guide to the remedy verbatim.
- `scripts/walk-adoption.sh` fetches the pinned release from the home into its
  scratch root on a machine with no `bd`, from the same URL CI builds, so `make
  adoption` installs the tracker from one place wherever it runs.

The scaffold `yoyo init` writes names no Beads repository at all, so there was
nothing there to bring into agreement; it was on the item's list from a reading
of the documents beside the configuration rather than of the scaffold's own
comments.
