# coding-flow end-to-end test artifact

This directory is an artifact of an end-to-end test of the coding-flow
pipeline: design, planning, implementation, merge, audit.

It takes no part in the build, the tests or the runtime, no `make check`
or CI gate judges it, and no other file references it. It is read by
`tools/scan_cjk.py`, run on demand: that script reports the CJK
characters found outside the paths it carves out, which is how the
language rule stated in the root `CLAUDE.md` gets checked, and it is
not part of the `make check` chain. This file lies inside that scan, so
it stays English — a CJK character written here would be reported by
that script.

It is safe to delete: removing this directory requires no change in any
other file, and the scan reads one file fewer.
