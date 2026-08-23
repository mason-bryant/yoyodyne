#!/usr/bin/env python3
#
# check-doc-links.py - resolve every intra-repository Markdown link, path and
# fragment both, against the headings that actually exist.
#
#   scripts/check-doc-links.py            # sweep the whole repository
#   scripts/check-doc-links.py README.md  # sweep named files instead
#
# A fragment GitHub cannot resolve is ignored silently: the reader lands at the
# top of the right file with nothing to say anything went wrong, which is the
# one broken link a reviewer reading a diff does not catch and a 404 check does
# not either. yoyodyne-ifd.54 recorded two README anchors left dead by a rename,
# found by hand months later. yoyodyne-ifd.121 moved 22 README sections and the
# links between them at once, and yoyodyne-ifd.117 will move 30 more, so the
# sweep is written down here rather than performed once and forgotten.
#
# What is checked, for every Markdown file outside .git, .beads, and bin:
#
#   - a relative path resolves to a file or directory that exists
#   - a fragment resolves to a heading in the target file, using GitHub's slug
#     rules -- lowercase, punctuation dropped, each space becoming one hyphen
#     (so an em dash between spaces leaves two), and a repeated slug suffixed
#     -1, -2, and so on
#   - a link into a directory carries no fragment, since there is no heading to
#     resolve it against
#
# Anchors cited from Go source are checked too: a docs/... URL or path with a
# fragment anywhere under the module is resolved the same way, which is what
# holds docs/configuration.md#checks -- the link in every .yoyodyne/config.yaml
# yoyo init has ever written -- to the heading it names.
#
# Absolute http(s) links are not fetched; a fragment on a URL naming this
# repository is resolved against the file it names.
#
# Requires Python 3. Exits non-zero, naming each broken link, if any fails.

import os
import re
import sys
import urllib.parse

SKIP_DIRECTORIES = {".git", ".beads", "bin", "node_modules"}
REPOSITORY_URL = "github.com/mason-bryant/yoyodyne"

LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")
FENCE = re.compile(r"^\s*```")
HEADING = re.compile(r"^#{1,6} (.+?)\s*$")
INLINE_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")
# A docs path with a fragment, as Go source or a workflow writes one: either a
# bare docs/... path or a full GitHub blob URL, and either quoted or not.
SOURCE_ANCHOR = re.compile(r"(?:" + re.escape(REPOSITORY_URL) + r"/blob/[^/\s\"']+/)?(docs/[\w./-]+\.md)#([\w-]+)")


def repository_root():
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def walk(root):
    """Every file under root, skipping the directories nothing is checked in."""
    for directory, subdirectories, filenames in os.walk(root):
        subdirectories[:] = [d for d in subdirectories if d not in SKIP_DIRECTORIES]
        for filename in filenames:
            yield os.path.relpath(os.path.join(directory, filename), root)


def slug(heading):
    """GitHub's anchor for a heading, before de-duplication."""
    text = INLINE_LINK.sub(r"\1", heading).replace("`", "")
    text = re.sub(r"[^\w\s-]", "", text.lower())
    # Each space becomes one hyphen: runs are not collapsed, which is why
    # "2. `yoyo init` -- give ..." resolves as ...-init--give-...
    return text.strip().replace(" ", "-")


def anchors_of(path):
    """Every fragment the file at path answers to."""
    found, seen, fenced = set(), {}, False
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            if FENCE.match(line):
                fenced = not fenced
                continue
            if fenced:
                continue
            heading = HEADING.match(line.rstrip("\n"))
            if not heading:
                continue
            base = slug(heading.group(1))
            repeat = seen.get(base, 0)
            seen[base] = repeat + 1
            found.add(base if repeat == 0 else "%s-%d" % (base, repeat))
    return found


def strip_code(text):
    """Drop fenced blocks, so an example link is not resolved as a real one."""
    return re.sub(r"^\s*```.*?^\s*```", "", text, flags=re.S | re.M)


class Sweep:
    def __init__(self, root):
        self.root = root
        self.anchors = {}
        self.broken = []
        self.links = 0

    def anchors_for(self, relative):
        if relative not in self.anchors:
            self.anchors[relative] = anchors_of(os.path.join(self.root, relative))
        return self.anchors[relative]

    def report(self, kind, source, target):
        self.broken.append("%-14s %s -> %s" % (kind, source, target))

    def check_fragment(self, source, target, relative, fragment):
        if fragment not in self.anchors_for(relative):
            self.report("DEAD FRAGMENT", source, target)

    def check_link(self, source, target):
        """source is a repository-relative path; target is the link as written."""
        if target.startswith(("mailto:", "#!")):
            return
        if target.startswith(("http://", "https://", "<http")):
            # Only a fragment on a URL naming this repository is resolvable
            # here, and it is the form published release notes use.
            match = re.search(re.escape(REPOSITORY_URL) + r"(?:/blob/[^/\s]+/([\w./-]+))?#([\w-]+)", target)
            if match:
                self.links += 1
                self.check_fragment(source, target, match.group(1) or "README.md", match.group(2))
            return

        self.links += 1
        target = urllib.parse.unquote(target)
        path, _, fragment = target.partition("#")
        if path == "":
            self.check_fragment(source, target, source, fragment)
            return

        relative = os.path.normpath(os.path.join(os.path.dirname(source), path))
        absolute = os.path.join(self.root, relative)
        if not os.path.exists(absolute):
            self.report("MISSING FILE", source, target)
            return
        if os.path.isdir(absolute):
            if fragment:
                self.report("DIR+FRAGMENT", source, target)
            return
        if fragment and relative.endswith(".md"):
            self.check_fragment(source, target, relative, fragment)

    def sweep_markdown(self, relative):
        with open(os.path.join(self.root, relative), encoding="utf-8") as handle:
            body = strip_code(handle.read())
        for target in LINK.findall(body):
            self.check_link(relative, target)

    def sweep_source(self, relative):
        with open(os.path.join(self.root, relative), encoding="utf-8", errors="replace") as handle:
            body = handle.read()
        for path, fragment in SOURCE_ANCHOR.findall(body):
            self.links += 1
            if not os.path.exists(os.path.join(self.root, path)):
                self.report("MISSING FILE", relative, path + "#" + fragment)
                continue
            self.check_fragment(relative, path + "#" + fragment, path, fragment)


def main(argv):
    root = repository_root()
    named = argv[1:]
    paths = named if named else sorted(walk(root))

    sweep = Sweep(root)
    markdown = [p for p in paths if p.endswith(".md")]
    source = [p for p in paths if p.endswith((".go", ".yml", ".yaml", ".sh"))]
    for relative in markdown:
        sweep.sweep_markdown(relative)
    for relative in source:
        sweep.sweep_source(relative)

    for line in sweep.broken:
        print(line)
    print(
        "checked %d link(s) in %d Markdown file(s) and %d source file(s): %d broken"
        % (sweep.links, len(markdown), len(source), len(sweep.broken))
    )
    return 1 if sweep.broken else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
