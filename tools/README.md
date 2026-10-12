# tools

Repository self-checks. Each script is a plain executable with a `--root`
option (default: the current directory) and prints the paths it reports
relative to that root. They are stdlib-only Python, so `python3` is the
whole dependency list — except `check_markdown_examples.py`, which shells
out to the Go toolchain because compiling an example is its job.

Python 3.11 or newer is required: `check_toolchain.py` parses TOML with
`tomllib`. The root `.mise.toml` pins the version `mise install` provides.

## Running them

A script named `check_*.py` is a gate: it takes no arguments, it reports
against the current directory, and `make repo-check` runs every one of
them, so a gate added here is wired by its name alone.

Each script also runs on its own:

```
python3 tools/check_toolchain.py
python3 tools/check_markdown_examples.py
python3 tools/check_workflow_refs.py
python3 tools/check_conflict_markers.py
```

Their own test suites run together, from the repository root:

```
make tools-test
```

`test_makefile.py` runs there as well. It is not a script's suite: it holds
the root `Makefile`'s legs that loop over the modules or the gates to running
every item and failing at the end, driving the real `Makefile` over scratch
directories with stand-ins for `go` and `golangci-lint` first on `PATH`, so
it needs `make` and nothing else.

## The scripts

`check_toolchain.py` — fails when a version pinned in the root
`.mise.toml` no longer mirrors the authoritative source that a tool
actually reads. Today that is one pin, `go` against `go.work`'s `go`
directive; the file's own header says which pins have no mirror and are
therefore not checked. Exit codes: 0 clean, 1 drift, 2 infrastructure
error (a source missing or unparsable, a mirrored tool absent from
`.mise.toml`).

`check_markdown_examples.py` — compiles or parse-checks the fenced Go code
blocks in markdown prose, which no other harness covers (godoc `Example`
functions compile inside their module's own test binary; prose does not).
A block that carries its own `package` clause is claiming to be real
code and is built and vetted for real, in a throwaway module wired by
`replace` to the checkout it documents. A block without one is a
fragment, and is syntax-checked under several wrappings instead of being
forced into a padded full program. The script's own docstring carries the
scope rules and the escape hatch for a fragment that genuinely cannot
parse.

`check_conflict_markers.py` — fails when a line of git's conflict
markers survives into a file. A resolution done by hand and committed
leaves them as ordinary content: `git status` is clean and no other gate
reads them, since the document checker reads diagrams, sections, indexes
and links and the example checker reads Go blocks. The three labeled
markers are findings on their own; the bare middle one is reported only
in a file that also carries a labeled one, because a line of seven `=`
alone is also how Markdown underlines a heading. Everything text-shaped
in the tree is in scope, `node_modules` and non-UTF-8 files excepted.
Exit codes: 0 clean, 1 markers found (each with file, line and text),
2 infrastructure error (the root is not a directory, or the scan read no
text file at all).

`check_workflow_refs.py` — fails when a coding-flow template copy under
`.github/workflows/` carries a relative path segment (a `.` or `..`
that begins one — followed by `/` and not preceded by another `.`, so an
ellipsis is not a hit). The templates are byte-copies of files that
live inside the upstream skill directory, whose own cross-references
(the `../../SKILL.md`-style two-level form, once carried and gone now)
are written relative to it and can arrive with a
sync; copied into a consuming repository the same text points into a
different tree. The gate does not judge where a given reference would
land — a copy's relative path segment is treated as suspected to dangle,
so an intentional repository-relative reference is a hit too, a
criterion trade-off rather than an inline exemption. Guarded files are
the lowercase `.yml`/`.yaml` files carrying a
`uses: vislake/coding-flows@...` line — the call to the composite action
upstream publishes — so this repository's own workflows may keep using
repository-relative paths, and a fifth template lands under the gate
with nothing to maintain here. Exit codes: 0 clean (every guarded file
reported by name), 1 a relative reference found, 2 infrastructure error
(no `.github/workflows/` directory, no guarded file to check, or a file
that cannot be read).

