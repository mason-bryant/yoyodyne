# Working on yoyo itself

*For someone changing yoyo itself. Part of
[yoyo's documentation](../README.md#further-reading).*

`yoyo` is configured against its own repository, so a checkout of it is a
project like any other. From one, verify the tools, run every check, and open
the conversation:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyo config validate
./bin/yoyo chat
```

`make check` is `fmtcheck`, `test`, `race`, and `vet`, and it is the gate CI
runs.

Two scripts check things the gate does not, and a change that touches what they
cover should run them:

```sh
scripts/check-doc-links.py     # every Markdown link resolves, fragment included
scripts/walk-adoption.sh       # the README's Getting started, executed
```

`check-doc-links.py` resolves relative paths and fragments against the headings
that exist, under GitHub's own slug rules, and covers the `docs/…#anchor`
citations in Go source as well as the Markdown. A fragment nothing resolves is
ignored silently by GitHub rather than 404ing, so it is the one broken link that
survives review; run this after moving or renaming any heading.

`make dist VERSION=<tag>` builds the release archives and their checksums into
`dist/`, and `make dist-verify VERSION=<tag>` does that and then unpacks the
archive for the platform it is running on and asserts the binary reports
`<tag>`. That target is the whole of what a release is: the release workflow
runs it for a pushed tag and publishes what it produced, and CI runs the same
target on every change with a placeholder version, so a tag push reruns a path
that is already exercised rather than executing it for the first time when a
failure would mean a botched or missing release.
