// Package openapigen is the importable entry point to the fragment generator
// for modules outside the pkg module.
//
// The implementation lives in pkg/internal/openapigen. That location is not
// visible to a host module under starters/, which is where a host's own
// fragment is declared: Go's internal rule admits only importers inside the
// pkg tree, and the host's self-service creation fragment is still declared
// there. This package forwards the one function such a host needs,
// so the generator keeps a single implementation and a host's generation test
// runs the same code every pkg module's test runs.
//
// Nothing imports this package from a module's non-test code, so it stays a
// build-time dependency: no host binary links the generator or the libraries
// it bridges.
package openapigen

import (
	speedhttp "github.com/vislake/speed/pkg/http"
	internal "github.com/vislake/speed/pkg/internal/openapigen"
)

// Generate turns the declarations into their OpenAPI fragment as JSON.
//
// `title` and `version` become the fragment's info section; paths stay
// relative to the API root, the form every fragment in this repository uses.
// See pkg/internal/openapigen for the declaration forms the generator
// accepts and the normalisation it applies.
func Generate(title, version string, operations speedhttp.Operations) ([]byte, error) {
	return internal.Generate(title, version, operations)
}
