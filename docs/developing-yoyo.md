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

[`scripts/install.sh`](../scripts/install.sh) is the one part of the install
path that is not the binary, because it is the part that fetches the binary.
[`scripts/install-test.sh`](../scripts/install-test.sh) runs it against a
fabricated release and a fabricated machine — `curl` and `uname` are stubs on
`PATH` — so a platform with no release binary, a download that does not match
the published checksums, a `PATH` the binary is not on, and a missing
prerequisite are executed rather than asserted. It needs no network and no
published release, and CI runs it on every change.

`make dist VERSION=<tag>` builds the release archives and their checksums into
`dist/`, and `make dist-verify VERSION=<tag>` does that and then unpacks the
archive for the platform it is running on and asserts the binary reports
`<tag>`. That target is the whole of what a release consists of: the release
workflow runs it for a pushed tag and publishes what it produced, and CI runs
the same target on every change with a placeholder version, so a tag push
reruns a path that is already exercised rather than executing it for the first
time when a failure would mean a botched or missing release.

## Cutting a release

`make release VERSION=<tag>` is that build with its gate in front, so a daily
cadence costs two commands rather than a procedure:

```sh
make release VERSION=v0.3.0
git push origin v0.3.0
```

It walks [the documented adoption path](../scripts/walk-adoption.sh), runs
`check`, builds and verifies the archives for `<tag>`, then tags the commit
they were built from — in that order, so a red gate refuses the cut, names what
was red, and leaves nothing to undo. It also refuses a tag that is not
`vMAJOR.MINOR.PATCH` or that already exists, a dirty working tree, a checkout
that is not on `main`, and a `HEAD` that is not where `origin/main` is; where
origin is unreachable it says that last one went unchecked rather than passing
over it.

It stops at the tag. Publishing is the `git push`, which is the irreversible
half and what the release workflow acts on, so it stays something you do
deliberately. [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh)
executes every one of those refusals against fabricated repositories.
