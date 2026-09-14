#!/usr/bin/env python3
"""Verify every internal markdown link and heading anchor in the blueprint.

Run it in CI. Broken cross-references are how a pattern library quietly stops
being usable: an agent follows a link, gets nothing, and invents its own answer.

    ./scripts/check-links.py          # check
    ./scripts/check-links.py --list   # also print every anchor found
"""

import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

LINK_WITH_ANCHOR = re.compile(r"\]\(([A-Za-z0-9._/-]*\.md)#([A-Za-z0-9_-]+)\)")
LINK_PLAIN = re.compile(r"\]\(([A-Za-z0-9._/-]*\.md)\)")
LINK_SAME_FILE = re.compile(r"\]\(#([A-Za-z0-9_-]+)\)")
HEADING = re.compile(r"^(#{1,6})\s+(.*)$")
FENCE = re.compile(r"^\s*```")


def slug(text):
    """GitHub's heading -> anchor transform."""
    text = text.strip().lower()
    text = re.sub(r"[^\w\s-]", "", text)
    return re.sub(r"\s", "-", text)


def headings(text):
    """Anchors defined in a document, ignoring anything inside code fences."""
    found, in_fence = set(), False
    for line in text.splitlines():
        if FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        m = HEADING.match(line)
        if m:
            found.add(slug(m.group(2)))
    return found


def main():
    docs = sorted(
        [p for p in ROOT.glob("*.md")] + [p for p in ROOT.glob("patterns/*.md")]
    )
    if not docs:
        print("no markdown files found", file=sys.stderr)
        return 1

    text = {p: p.read_text(encoding="utf-8") for p in docs}
    anchors = {p: headings(t) for p, t in text.items()}

    if "--list" in sys.argv:
        for p in docs:
            print(f"\n{p.relative_to(ROOT)}")
            for a in sorted(anchors[p]):
                print(f"  #{a}")
        return 0

    problems = []

    def resolve(src, target):
        path = (src.parent / target).resolve()
        try:
            path.relative_to(ROOT)
        except ValueError:
            return None
        return path

    for src in docs:
        body = text[src]
        rel = src.relative_to(ROOT)

        for target, anchor in LINK_WITH_ANCHOR.findall(body):
            path = resolve(src, target)
            if path is None or not path.exists():
                problems.append(f"{rel}: missing file  -> {target}")
            elif anchor not in anchors.get(path, set()):
                problems.append(f"{rel}: missing anchor -> {target}#{anchor}")

        for target in LINK_PLAIN.findall(body):
            path = resolve(src, target)
            if path is None or not path.exists():
                problems.append(f"{rel}: missing file  -> {target}")

        for anchor in LINK_SAME_FILE.findall(body):
            if anchor not in anchors[src]:
                problems.append(f"{rel}: missing anchor -> #{anchor}")

    if problems:
        print(f"{len(problems)} broken reference(s):\n", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1

    total = sum(
        len(LINK_WITH_ANCHOR.findall(t))
        + len(LINK_PLAIN.findall(t))
        + len(LINK_SAME_FILE.findall(t))
        for t in text.values()
    )
    print(f"OK - {total} internal references across {len(docs)} documents resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main())
