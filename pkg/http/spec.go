package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/core"
)

// openAPIField is the one top-level field a fragment has to carry. Its
// presence is what tells an OpenAPI document apart from some other JSON that
// ended up in the declaration.
const openAPIField = "openapi"

// validateSpecs checks the shape of every declared fragment and reports every
// offending one at once. A single fragment's failure aborts the startup
// anyway, so reporting them one run at a time would only make the host fix
// them one run at a time.
func validateSpecs(specs []core.Resource[Spec]) error {
	var failures []error
	for _, res := range specs {
		if err := validateSpec(res.Module, res.Value); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// validateSpec checks that one fragment has a usable shape. It does not check
// the fragment against the OpenAPI grammar: that needs a parsing library, which
// this module does not bring in. What is caught here is
// the class that would otherwise break a reader's merge long after startup,
// where nothing connects the crash back to the module that declared the
// fragment.
//
// Nor does it check the fragment against what the module actually registered.
// The two are separately maintained facts and are allowed to drift; a mismatch
// produces no failure signal at all.
func validateSpec(module string, s Spec) error {
	if err := validateSpecRoot(module, s); err != nil {
		return err
	}
	if len(bytes.TrimSpace(s.Document)) == 0 {
		return fmt.Errorf("%w: module %q declared a Spec with an empty Document. "+
			"Either embed the module's openapi.json into it or drop the declaration",
			ErrInvalidSpec, module)
	}
	// utf8.Valid comes before the JSON decoding on purpose: encoding/json
	// replaces an illegal byte inside a string with U+FFFD instead of
	// failing, so a fragment carrying mis-encoded text would pass the decode
	// and reach the reader's merge as silently corrupted content.
	if !utf8.Valid(s.Document) {
		return fmt.Errorf("%w: module %q declared a Spec whose Document is not valid UTF-8. "+
			"JSON is UTF-8 by definition; re-encode the file the fragment came from",
			ErrInvalidSpec, module)
	}
	if !json.Valid(s.Document) {
		return fmt.Errorf("%w: module %q declared a Spec whose Document is not valid JSON. "+
			"The Document is the JSON encoding of the fragment, not YAML",
			ErrInvalidSpec, module)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(s.Document, &top); err != nil {
		return fmt.Errorf("%w: module %q declared a Spec whose Document is not a JSON object at the "+
			"top level. An OpenAPI document is an object; wrap or replace the fragment: %w",
			ErrInvalidSpec, module, err)
	}
	// A literal null decodes into a nil map without an error, so it reaches
	// here as a document with no fields rather than as a decoding failure.
	// Reported as a missing openapi field it would send the reader looking
	// for a field to add to a document that has no top-level object to add
	// it to.
	if top == nil {
		return fmt.Errorf("%w: module %q declared a Spec whose Document is null, not a JSON object at "+
			"the top level. An OpenAPI document is an object", ErrInvalidSpec, module)
	}
	if _, ok := top[openAPIField]; !ok {
		return fmt.Errorf("%w: module %q declared a Spec whose Document has no %q field at the top "+
			"level. Add it with the OpenAPI version the fragment is written against, "+
			"for example %q: \"3.1.0\"", ErrInvalidSpec, module, openAPIField, openAPIField)
	}
	return validateDocumentPaths(module, s, top)
}

// validateSpecRoot checks Prefix, Version and Scope, the path parts the reader
// prepends to every path in the fragment.
func validateSpecRoot(module string, s Spec) error {
	if s.Prefix != "" {
		if err := checkPrefixSegments(s.Prefix); err != nil {
			return fmt.Errorf("%w: module %q declared a Spec whose Prefix %q %s. Write the "+
				"endpoint's path as bare segments without a leading slash, for example \"api\"",
				ErrInvalidSpec, module, s.Prefix, err)
		}
	}
	if s.Version != "" && !literalSegment(s.Version) {
		return fmt.Errorf("%w: module %q declared a Spec whose Version %q is not one bare literal "+
			"segment. Write the version without slashes or wildcards, for example \"v1\"",
			ErrInvalidSpec, module, s.Version)
	}
	if s.Scope != "" && !s.Scope.Valid() {
		return fmt.Errorf("%w: module %q declared the Scope %q, which is not one of %q or %q. "+
			"The ordinary surface declares no scope and its paths hang directly off the API root",
			ErrInvalidSpec, module, s.Scope, ScopeTenants, ScopeSystem)
	}
	return nil
}

// validateDocumentPaths holds the fragment's paths to the relative form the
// reader expects: each begins with "/" and none already carries the API root,
// which would be prepended twice.
func validateDocumentPaths(module string, s Spec, top map[string]json.RawMessage) error {
	raw, ok := top["paths"]
	if !ok {
		return nil
	}
	var paths map[string]json.RawMessage
	if err := json.Unmarshal(raw, &paths); err != nil {
		return fmt.Errorf("%w: module %q declared a Spec whose paths section is not a JSON object: %w",
			ErrInvalidSpec, module, err)
	}
	root := s.APIRoot()
	for path := range paths {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%w: module %q declared the path %q, which does not begin with \"/\". "+
				"Every OpenAPI path is absolute from the API root", ErrInvalidSpec, module, path)
		}
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, root+"/") {
			return fmt.Errorf("%w: module %q declared the path %q, which already carries the API root "+
				"%q. Write the path relative to the root, for example %q",
				ErrInvalidSpec, module, path, root, strings.TrimPrefix(path, root))
		}
		if !s.Scope.joinsPath(path) {
			return fmt.Errorf("%w: module %q declared the path %q in the %q scope, where every path "+
				"names the tenant first: write it as %q",
				ErrInvalidSpec, module, path, s.Scope, tenantPathPrefix+strings.TrimPrefix(path, "/"))
		}
	}
	return nil
}

// checkPrefixSegments validates a path written as bare literal segments, the
// form Prefix takes: "api" or "internal/api", never "/api".
func checkPrefixSegments(prefix string) error {
	if strings.HasPrefix(prefix, "/") {
		return errors.New("begins with \"/\"; write bare segments, for example \"api\"")
	}
	if strings.HasSuffix(prefix, "/") {
		return errors.New("ends with \"/\"")
	}
	for _, segment := range strings.Split(prefix, "/") {
		if !literalSegment(segment) {
			return fmt.Errorf("carries the segment %q, which is not a literal segment name", segment)
		}
	}
	return nil
}

// literalSegment reports whether one path segment is a plain name: no
// wildcard, parameter marker, query, fragment, escape or whitespace.
func literalSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	return !strings.ContainsAny(segment, "/:*?#%") &&
		!strings.ContainsFunc(segment, unicode.IsSpace)
}
