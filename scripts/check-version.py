#!/usr/bin/env python3
"""Verify the blueprint's version bookkeeping.

VERSION names the current version, and every version has a file in upgrades/
telling a project how to reach it. Run it in CI: a version bumped without an
upgrade file leaves every project that updates with no instructions.

    ./scripts/check-version.py
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SEMVER = re.compile(r"^(\d+)\.(\d+)\.(\d+)$")


def parse(text):
    m = SEMVER.match(text)
    return tuple(int(n) for n in m.groups()) if m else None


def main():
    problems = []

    raw = (ROOT / "VERSION").read_text(encoding="utf-8")
    version = raw.strip()
    current = parse(version)
    if current is None:
        print(f"VERSION must be MAJOR.MINOR.PATCH, found {version!r}", file=sys.stderr)
        return 1
    if raw != version + "\n":
        problems.append("VERSION must be the version and a single newline")

    upgrades = ROOT / "upgrades"
    own = upgrades / f"{version}.md"
    if not own.exists():
        problems.append(f"upgrades/{version}.md is missing")

    for path in sorted(upgrades.glob("*.md")):
        if path.name == "README.md":
            continue
        found = parse(path.stem)
        if found is None:
            problems.append(f"upgrades/{path.name}: name is not a version")
            continue
        if found > current:
            problems.append(f"upgrades/{path.name} is newer than VERSION ({version})")
        lines = path.read_text(encoding="utf-8").splitlines()
        if not lines or lines[0].strip() != f"# {path.stem}":
            problems.append(f"upgrades/{path.name}: first line must be '# {path.stem}'")
        if not any(line.startswith("Action required:") for line in lines):
            problems.append(f"upgrades/{path.name}: no 'Action required:' line")

    pyproject = ROOT / "pyproject.toml"
    if pyproject.exists():
        m = re.search(r'^version\s*=\s*"([^"]+)"', pyproject.read_text(encoding="utf-8"), re.M)
        if m and m.group(1) != version:
            problems.append(f"pyproject.toml says {m.group(1)}, VERSION says {version}")

    if problems:
        print(f"{len(problems)} version problem(s):\n", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1

    print(f"OK - version {version}, {len(list(upgrades.glob('*.md'))) - 1} upgrade file(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
