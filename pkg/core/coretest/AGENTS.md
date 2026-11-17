# pkg/core/coretest

The shared value functions the Go test bounds of `pkg` and the demo server
go through. The frontend and end-to-end tiers read `GATE_TIME_SCALE` through
their own helpers and do not import it.
`Scale(d)` multiplies a bound by the binary's race factor and by
`GATE_TIME_SCALE`, so that a loaded machine, or several gates running at
once, make a test slower rather than failing it. `Declared(d)` multiplies by
`GATE_TIME_SCALE` alone, for the upper bound of a case that tells a right
implementation from a wrong one by how long it took.
`DeclaredAgainst(t, fixed, declared, wrong)` is that upper bound with the
wrong implementation's own time stated in code: it returns
`fixed + Declared(declared)` and fails the case through `t.Fatalf` once that
bound reaches nine tenths of `wrong`, which is before the two meet. A run
whose scale reaches the meeting point therefore fails the case loudly, naming
the scale, the bound, the wrong time and the meeting point, rather than
letting the case pass without telling the two implementations apart. A wrong
implementation whose time the case controls is widened far past the bound; a
product default or poll interval the case cannot move is stated as it is.

## Boundaries

- **It imports no `testing`.** A product test and a `starters/demo` test both
  import it, so it stays a plain library: a helper that pulled in the test
  framework would link it into every binary that imports its caller's test
  binary.
- **It carries its own tests**, which pin the parse of `GATE_TIME_SCALE` and
  the two factors apart.
- **It sits beside `core` and not under `pkg/internal`, because
  `starters/demo/server` is a separate module and Go's `internal` rule keeps
  it out.** The authentication and authorization fixtures sit beside their
  own capabilities for the same reason. What keeps it out of production code
  is the dependency rule in `pkg/core/deps_test.go`, which fails when a
  package's non-test code imports it.
- **It imports the standard library only.** It lives inside `pkg/core`, whose
  dependency check covers its whole tree, and the scale is read from the
  environment rather than from a library.
- **It never probes the machine.** `GATE_TIME_SCALE` is the caller's
  declaration and nothing else decides a bound. Unset, empty or whitespace
  means 1; any other value must be a decimal number of at least 1, fractions
  and exponents included, and a value that is not one (non-finite, below 1,
  hexadecimal, or underscore-separated) stops the run at startup with a
  message naming the requirement, never a silent 1. Reading the load, the
  core count or the available CPUs would decide differently on the same
  commit, which is what the concurrency work rules out.
- **A lower bound goes through neither function.** "This took at least X" is
  only easier to satisfy on a slow machine; scaling it would stop it from
  holding the code to having waited at all.
- **A bound that detects behaviour uses `Declared`, not `Scale`.** Such a
  bound separates a right implementation from a wrong one by elapsed time,
  and the race factor slows the program under test rather than the difference
  being measured. The case widens the wrong implementation's own time far
  past the bound, so a larger `GATE_TIME_SCALE` does not bring the two
  together. **Where the wrong implementation's time can be named, the bound
  goes through `DeclaredAgainst`**, which states that time and fails the case
  before the scaled bound meets it; a hand-written meeting point in a comment
  drifts from the constants, and a case that meets the wrong implementation
  passes without saying so.

## Running it

From the repository root:

```
make test
make test-race
make lint
```

On this package alone, inside the `pkg` module:

```
go -C pkg test ./core/coretest/...
```
