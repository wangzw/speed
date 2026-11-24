// Package openapigen is the importable entry point to the fragment generator for
// modules outside the pkg module.
package openapigen

import (
	speedhttp "github.com/vislake/speed/pkg/http"
	internal "github.com/vislake/speed/pkg/internal/openapigen"
)

// Generate turns the declarations into their OpenAPI fragment as JSON.
func Generate(title, version string, operations speedhttp.Operations) ([]byte, error) {
	return internal.Generate(title, version, operations)
}
