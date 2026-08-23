# Release notes

*One file per release tag, named for it. Part of
[yoyo's documentation](../../README.md#further-reading).*

`v0.3.1.md` is what changed in `v0.3.1`. The file is written before the tag
exists and committed with the commit the tag names, so the release page and the
repository tell one story rather than two: the
[release workflow](../../.github/workflows/release.yml) publishes this file as
the release's body, with the install preamble under it.

## The shape

Three sections, in this order, because it is the order a reader needs them in:

1. **Key functionality** — what the release is *for*. A newcomer reading only
   this heading should be able to say what changed.
2. **Enhancements** — what got better about something that already worked.
3. **Bug fixes** — what was broken and is not any more.

There is one exception, and only one: **a critical bug fix may go up with key
functionality**. A release whose headline is that a data-loss defect is closed
should say so at the top, not three headings down.

## How a file gets here

[`scripts/release-notes.sh`](../../scripts/release-notes.sh) drafts one from
what actually landed — the work items closed between the previous release tag
and this one, with their titles, their types, and the goals they served. The
work item behind a change says what somebody wanted, which is the difference
between notes and a changelog:

```sh
scripts/release-notes.sh v0.3.1            # draft docs/releases/v0.3.1.md
scripts/release-notes.sh v0.3.1 --print    # see the draft without writing it
```

Where the draft puts each item is placed from its type alone, and that placement
is a starting point rather than an answer. **Which work is key, which is an
enhancement, and which fix is critical enough to go to the top is the product
manager's judgement**, until the post-v1 release-manager role exists. Edit the
file, then commit it.

[Cutting a release](../developing-yoyo.md#cutting-a-release) gates on this file
being present: `make release VERSION=<tag>` with no notes for `<tag>` drafts
them and refuses, so the judgement happens before the tag rather than after the
release page is published.
