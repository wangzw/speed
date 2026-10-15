# pkg/internal/configtest

The configuration reader a carrier case hands a module under test. It registers
no module, delivers no capability and takes part in no lifecycle stage: it wraps
"build the real configuration loader over a JSON primary source", so that the
holders do not each carry a copy of the loader setup and drift apart on which
sources and which host identity a case runs against.

## Boundaries

- **No module is registered here and no capability is delivered.** The carrier
  cases use it, each in its own test, for its own sections.
- **It sits here, outside a capability directory, because two capability
  directories use it.** The carrier cases under `pkg/sms` and the carrier cases
  under `pkg/mail` both call `Reader`; a package under `pkg/sms` cannot import an
  `internal` package of `pkg/mail`, and both directories are under the one `pkg`
  module's `internal/`.
- **It is the authentication capability's `authntest` reader without the authn
  dependency.** A carrier reads its own sections and must not pull the
  authentication capability into its tests, so this local reader is what keeps
  the sending capabilities free of it.
- **It is the loader itself rather than a stand-in.** Half of what these cases
  hold belongs to the declaration rather than to the code — that an absent item a
  module declared required is refused at all comes from the item being declared
  required. A stand-in reader would not hold that.
- **The command line is empty and the prefix is one no machine carries variables
  under,** so the locator is the host identity's default and nothing outside the
  case reaches the values.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this package alone, inside the `pkg` module:

```
go -C pkg build ./internal/configtest/...
go -C pkg vet ./internal/configtest/...
go -C pkg test ./internal/configtest/...
```

The package holds no test of its own; the carrier cases that call `Reader`
exercise it.
