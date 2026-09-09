#!/usr/bin/env python3
"""Strip // and /* */ comments from .ts/.js files under given roots.

Keeps /** ... */ JSDoc blocks. Everything else (line comments, plain block
comments) is removed. Strings, template literals (including ${...}
interpolation) and regex literals are tokenized so comment-like sequences
inside them are left untouched.

Usage: python3 strip_comments_ts.py [root ...]   (default root: src)
"""
import os
import sys

EXTS = (".ts", ".js")

# Tokens/keywords after which a `/` starts a regex literal rather than being division.
REGEX_PRECEDERS = set("([{,;:=!&|?+-*%^~<>".split()) | {
    "return", "typeof", "instanceof", "in", "of", "new", "delete", "void",
    "throw", "case", "do", "else", "yield", "await",
}


def strip_comments(src: str) -> str:
    out = []
    i = 0
    n = len(src)
    last_significant = ""  # last non-whitespace, non-comment token text, for regex detection

    def prev_token_allows_regex():
        if not last_significant:
            return True
        if last_significant[-1].isalnum() or last_significant[-1] == "_" or last_significant[-1] == "$":
            return last_significant in REGEX_PRECEDERS
        return last_significant[-1] in REGEX_PRECEDERS or last_significant[-1] not in ")]}"

    while i < n:
        c = src[i]

        if c == "/" and i + 1 < n and src[i + 1] == "/":
            j = src.find("\n", i)
            if j == -1:
                i = n
            else:
                i = j
            continue

        if c == "/" and i + 1 < n and src[i + 1] == "*":
            is_jsdoc = i + 2 < n and src[i + 2] == "*" and not (i + 3 < n and src[i + 3] == "/")
            end = src.find("*/", i + 2)
            if end == -1:
                end = n
                block = src[i:end]
            else:
                end += 2
                block = src[i:end]
            if is_jsdoc:
                out.append(block)
                last_significant = "*/"
            i = end
            continue

        if c == "'" or c == '"':
            quote = c
            j = i + 1
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == quote:
                    j += 1
                    break
                if src[j] == "\n":
                    break
                j += 1
            out.append(src[i:j])
            last_significant = src[i:j]
            i = j
            continue

        if c == "`":
            j = i + 1
            depth = 0
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == "`" and depth == 0:
                    j += 1
                    break
                if src[j] == "$" and j + 1 < n and src[j + 1] == "{":
                    j += 2
                    depth += 1
                    continue
                if depth > 0:
                    if src[j] == "{":
                        depth += 1
                    elif src[j] == "}":
                        depth -= 1
                j += 1
            out.append(src[i:j])
            last_significant = src[i:j]
            i = j
            continue

        if c == "/" and prev_token_allows_regex():
            j = i + 1
            in_class = False
            ok = True
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == "\n":
                    ok = False
                    break
                if src[j] == "[":
                    in_class = True
                elif src[j] == "]":
                    in_class = False
                elif src[j] == "/" and not in_class:
                    j += 1
                    break
                j += 1
            else:
                ok = False
            if ok:
                while j < n and src[j].isalpha():
                    j += 1
                out.append(src[i:j])
                last_significant = src[i:j]
                i = j
                continue

        out.append(c)
        if not c.isspace():
            last_significant = c
        i += 1

    return "".join(out)


def clean_blank_lines(text: str) -> str:
    lines = [l.rstrip() for l in text.split("\n")]
    result = []
    blank_run = 0
    for line in lines:
        if line == "":
            blank_run += 1
            if blank_run > 1:
                continue
        else:
            blank_run = 0
        result.append(line)
    return "\n".join(result)


def process_file(path: str) -> bool:
    with open(path, encoding="utf-8") as f:
        src = f.read()
    stripped = strip_comments(src)
    cleaned = clean_blank_lines(stripped)
    if cleaned != src:
        with open(path, "w", encoding="utf-8") as f:
            f.write(cleaned)
        return True
    return False


def main():
    roots = sys.argv[1:] or ["src"]
    changed = []
    for root in roots:
        for dirpath, _dirs, files in os.walk(root):
            for fn in files:
                if fn.endswith(EXTS):
                    fp = os.path.join(dirpath, fn)
                    if process_file(fp):
                        changed.append(fp)
    for fp in changed:
        print(fp)
    print(f"\n{len(changed)} files changed", file=sys.stderr)


if __name__ == "__main__":
    main()
