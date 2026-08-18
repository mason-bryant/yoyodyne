## Install

```sh
go install github.com/mason-bryant/yoyodyne/cmd/yoyo@<tag>
```

Or download the archive for your platform below, unpack it, and put `yoyo` on
your `PATH`. `checksums.txt` carries a SHA-256 for each archive.

## What this release is tested on

Yoyodyne is developed and used on macOS. The `linux_amd64` binary is built by
the same workflow as the others and is exercised by CI, and by nothing else;
the `darwin_amd64` binary is built and is not regularly run by anyone. Treat
anything other than macOS on Apple silicon as **untested**, not as a platform
with the same evidence behind it.

There is no Windows binary. Windows is not supported and is not built.

## What a release still requires

`yoyo` is one binary, but the harness it drives is not self-contained: a
project needs [Beads](https://github.com/gastownhall/beads) (`bd`) initialized
in the repository and [Claude Code](https://code.claude.com/docs) installed and
authenticated before a run can do anything. Publishing pull requests needs a Git
remote and an authenticated [`gh`](https://cli.github.com) on top of that.

See [the README](https://github.com/mason-bryant/yoyodyne#getting-started) for
the three steps a new project follows.
