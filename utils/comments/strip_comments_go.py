#!/usr/bin/env python3
"""Strip // and /* */ comments from .go files under given roots.

Keeps compiler directive comments (//go:..., // +build ...) since those are
required for the build, not documentation. Skips whole files listed in
SKIP_FILES entirely (e.g. embedded.go). Strings, raw strings (`...`), and
rune literals are tokenized so comment-like sequences inside them are left
untouched.

Usage: python3 strip_comments_go.py [root ...]   (default root: src)
"""
import os
import sys

SKIP_FILES = {"embedded.go"}


def is_directive_comment(line_comment_text: str) -> bool:
    # line_comment_text includes the leading "//"
    body = line_comment_text[2:]
    return body.startswith("go:") or body.strip().startswith("+build")


def strip_comments(src: str) -> str:
    out = []
    i = 0
    n = len(src)

    while i < n:
        c = src[i]

        if c == "/" and i + 1 < n and src[i + 1] == "/":
            j = src.find("\n", i)
            end = j if j != -1 else n
            comment = src[i:end]
            if is_directive_comment(comment):
                out.append(comment)
            i = end
            continue

        if c == "/" and i + 1 < n and src[i + 1] == "*":
            end = src.find("*/", i + 2)
            end = end + 2 if end != -1 else n
            i = end
            continue

        if c == '"':
            j = i + 1
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == '"':
                    j += 1
                    break
                if src[j] == "\n":
                    break
                j += 1
            out.append(src[i:j])
            i = j
            continue

        if c == "`":
            j = src.find("`", i + 1)
            j = j + 1 if j != -1 else n
            out.append(src[i:j])
            i = j
            continue

        if c == "'":
            j = i + 1
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == "'":
                    j += 1
                    break
                if src[j] == "\n":
                    break
                j += 1
            out.append(src[i:j])
            i = j
            continue

        out.append(c)
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
                if fn.endswith(".go") and fn not in SKIP_FILES:
                    fp = os.path.join(dirpath, fn)
                    if process_file(fp):
                        changed.append(fp)
    for fp in changed:
        print(fp)
    print(f"\n{len(changed)} files changed", file=sys.stderr)


if __name__ == "__main__":
    main()
