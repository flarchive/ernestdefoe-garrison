#!/usr/bin/env python3
"""
Flags raw SQL that names a table without the database table prefix.

The query builder prefixes only the identifiers it wraps itself — from(),
join(), where(), groupBy(), whereColumn(). Anything handed to selectRaw(),
whereRaw(), orderByRaw(), raw(), statement() or a raw select() is passed
through VERBATIM, so a table named inside one of those must carry the prefix
itself:

    $p = $db->getTablePrefix();          # '' when no prefix is configured
    ->selectRaw("{$p}discussion_tag.tag_id as tid")

Left unprefixed it fails ONLY on forums that set a table prefix — which is why
it survives every local test and then 500s a customer's whole forum.

Usage:  check-table-prefix.py [path ...]      (defaults to the current dir)
Exit:   1 if anything is flagged, 0 if clean.
"""
import re
import sys
from pathlib import Path

# Calls whose argument reaches the database verbatim.
RAW_CALL = re.compile(
    r"(selectRaw|whereRaw|orWhereRaw|orderByRaw|havingRaw|groupByRaw|fromRaw|joinRaw"
    r"|->raw|DB::raw|DB::select|DB::statement|->statement|->unprepared)\s*\(",
)
# Quoted PHP string literals (single or double), non-greedy, escape-aware.
STRING = re.compile(r"'((?:[^'\\]|\\.)*)'|\"((?:[^\"\\]|\\.)*)\"")
# A name used AS A TABLE: after FROM/JOIN/UPDATE/INTO, or qualifying a column.
AS_TABLE = re.compile(
    r"(?:\b(?:FROM|JOIN|UPDATE|INTO)\s+`?)([a-z_][a-z0-9_]*)"
    r"|(?<![\w.$}])([a-z_][a-z0-9_]{2,})\.[a-z_]",
    re.IGNORECASE,
)
# SQL keywords and common aliases that are not table names.
NOT_A_TABLE = {
    "select", "from", "where", "count", "sum", "max", "min", "avg", "case",
    "when", "then", "else", "end", "and", "or", "not", "null", "as", "on",
    "order", "group", "by", "distinct", "coalesce", "date", "cast", "int",
    "row_number", "over", "partition", "limit", "desc", "asc", "inner", "left",
    "join", "exists", "in", "is", "true", "false", "interval",
}
# The line already builds a prefix into the expression.
PREFIXED = re.compile(r"getTablePrefix|\$prefix|\{\$p\}|\$p\s*\.|\$pivot|\$posts\b")


def _argument_span(text: str, open_paren: int) -> str:
    """The source between a call's parentheses — raw SQL often spans lines."""
    depth = 0
    for i in range(open_paren, min(len(text), open_paren + 4000)):
        if text[i] == "(":
            depth += 1
        elif text[i] == ")":
            depth -= 1
            if depth == 0:
                return text[open_paren + 1:i]
    return text[open_paren + 1:open_paren + 4000]


def flagged(text: str):
    """Yield (line_number, sorted table names) for each unprefixed raw call."""
    for call in RAW_CALL.finditer(text):
        span = _argument_span(text, call.end() - 1)
        if PREFIXED.search(span):
            continue
        names = []
        for m in STRING.finditer(span):
            literal = m.group(1) if m.group(1) is not None else m.group(2)
            if not literal:
                continue
            for t in AS_TABLE.finditer(literal):
                name = (t.group(1) or t.group(2) or "").lower()
                if name and name not in NOT_A_TABLE:
                    names.append(name)
        if names:
            yield text.count("\n", 0, call.start()) + 1, sorted(set(names))


def main(argv):
    roots = [Path(a) for a in (argv[1:] or ["."])]
    hits = 0
    for root in roots:
        files = root.rglob("*.php") if root.is_dir() else [root]
        for f in files:
            parts = set(f.parts)
            if parts & {"vendor", "node_modules", ".git"}:
                continue
            try:
                text = f.read_text(errors="replace")
            except OSError:
                continue
            lines = text.splitlines()
            for n, names in flagged(text):
                stripped = lines[n - 1].lstrip() if n <= len(lines) else ""
                if stripped.startswith(("*", "//", "#")):
                    continue
                hits += 1
                print(f"{f}:{n}: unprefixed {', '.join(names)}")
                print(f"    {stripped}")
    if hits:
        print(
            f"\n{hits} raw expression(s) name a table without the prefix.\n"
            "Interpolate $db->getTablePrefix() in front of the table name, as\n"
            "core does in Flarum\\Post\\Post::boot()."
        )
        return 1
    print("clean — no raw SQL names a table without the prefix")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
