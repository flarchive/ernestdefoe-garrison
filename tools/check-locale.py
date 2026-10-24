#!/usr/bin/env python3
"""
Refuse a locale file with a duplicate key.

🚨 THIS EXISTS BECAUSE A DUPLICATE KEY 500s THE ENTIRE FORUM API.

Symfony's YAML parser throws ParseException on a repeated key, and Flarum loads
every extension's locale file on boot — so one duplicate in this file takes down
/api for every user of the forum, not just Garrison's pages. It has happened
twice in one afternoon here: a second `settings:` under `forum:`, and a second
`players:`. Both were natural mistakes — the parent already had a key with the
obvious name, several hundred lines away.

An indentation heuristic is not enough and was the reason the first one shipped:
`settings` appeared once at each of two nesting levels, which a flat scan reads
as no duplicate at all. Keys have to be compared within their PARENT PATH.
"""

import re
import sys
from pathlib import Path


def duplicates(path: Path):
    stack: list[tuple[int, str]] = []
    seen: dict[tuple[tuple[str, ...], str], int] = {}
    found = []

    for number, raw in enumerate(path.read_text().splitlines(), 1):
        line = raw.rstrip()

        if not line.strip() or line.lstrip().startswith("#"):
            continue

        match = re.match(r"^(\s*)([^\s:][^:]*):(.*)$", line)

        if not match:
            continue

        indent = len(match.group(1))
        key = match.group(2).strip()

        while stack and stack[-1][0] >= indent:
            stack.pop()

        parent = tuple(k for _, k in stack)

        if (parent, key) in seen:
            found.append((number, ".".join(parent + (key,)), seen[(parent, key)]))
        else:
            seen[(parent, key)] = number

        stack.append((indent, key))

    return found


def main() -> int:
    failed = False

    for path in sorted(Path("resources/locale").glob("*.yml")):
        for number, key, first in duplicates(path):
            print(f"{path}:{number}: duplicate key {key!r}, first defined on line {first}")
            failed = True

    if failed:
        print("\nA duplicate key makes Symfony's YAML parser throw, which 500s the whole forum API.")
        return 1

    print("locale: no duplicate keys")
    return 0


if __name__ == "__main__":
    sys.exit(main())
