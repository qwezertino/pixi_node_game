# Project rules

## Comments

Do not write explanatory/rationale comments in code (no "why this way", no
narrating design decisions, history, or obvious behavior). Default to no
comments at all.

The only comments allowed:
- Doc comments (JSDoc `/** */` in TS/JS, Go doc comments directly above an
  exported declaration).
- Comments that are functionally required by the language/tooling, e.g. Go's
  `//go:embed`, `//go:build` / `// +build` directives.

If in doubt whether a comment is needed, don't write it.
