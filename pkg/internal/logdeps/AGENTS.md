# pkg/internal/logdeps

The descriptor-level cases about the logging dependency: a module that takes a
logger out of the registry has to declare the requirement on the logging
capability. It registers no module and delivers no capability — it holds the one
assertion that requirement has.

## Boundaries

- **The cases read module descriptors and run no host.** That is the only shape
  this requirement has: what a module resolves is inside its `New` or its `Init`,
  so a descriptor is all there is to read.
- **The modules are listed by hand.** Nothing in a descriptor says a module logs,
  so the list is the test's own. A module added later that both omits the
  declaration and is not listed here passes silently; that is the price the design
  records for this requirement.
- **It is a test-only package.** The file here is the cases themselves, and the
  package is declared in it.

## Why the omission is worth a case

A module that takes a logger without declaring the requirement is handed one all
the same, as long as some other module in the run declared it: one declaration
anywhere orders the logging module ahead of every construction and behind every
shutdown. So the omission leaves no trace in a run until the module carrying the
declaration is removed, and then it surfaces as a startup failure in a module
nobody changed. What it costs meanwhile is invisible too — a module constructed
before the logging module resolves no logger and falls back to the process
default logger, which writes to standard output and bypasses the configured
destination.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
```
