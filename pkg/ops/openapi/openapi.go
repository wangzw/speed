// Package openapi merges the OpenAPI fragments the modules of a binary declare
// into one document.
//
// It holds no module, no lifecycle and no resource: Merge is a pure function of
// the fragments and the two options, so the ops module merges in its Init and a
// build tool merges the same way without constructing any module. The two see
// the same bytes for the same declarations, which is what lets a build write
// into an artifact the document a running process serves.
//
// The document it produces is the contract its readers depend on: the version
// it declares, the info it carries, and the two extension keys below.
package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	// openAPIVersion is the version the merged document declares at its top
	// level. Every fragment a module declares is written against it.
	openAPIVersion = "3.1.0"

	// endpointExtension is the key every merged operation carries, naming the
	// endpoint the operation is served on. One document describes several
	// listening endpoints, and a path alone does not say which one answers it.
	endpointExtension = "x-endpoint"

	// fragmentsExtension is the top-level key listing where each fragment came
	// from: the module that declared it and the endpoint it was declared for.
	fragmentsExtension = "x-fragments"
)

var (
	// ErrUnreadable reports a fragment that cannot be read as a JSON object.
	// The error names the module that declared it.
	ErrUnreadable = errors.New("openapi: a fragment cannot be read")
	// ErrOperationConflict reports the same path and method declared by two
	// fragments. The error names both declaring modules and the endpoint each
	// declared it for, which is the only place the two can be tied back to
	// their sources.
	ErrOperationConflict = errors.New("openapi: two fragments declare the same operation")
	// ErrComponentConflict reports one component declared with different
	// content under one section. The error names the module that declared the
	// later one and the component's name.
	ErrComponentConflict = errors.New("openapi: two fragments declare one component differently")
)

// Options carries the values the merged document's info section takes. They are
// the module's ops.title and ops.version, or the values a build tool took from
// its own command line.
type Options struct {
	// Title is the document's info.title.
	Title string
	// Version is the document's info.version.
	Version string
}

// fragment is the part of a declared OpenAPI fragment the merge reads: the
// paths by method, and the components by section (schemas, parameters, and the
// rest).
type fragment struct {
	paths      map[string]map[string]json.RawMessage
	components map[string]map[string]json.RawMessage
}

// fragmentSource says which module declared a fragment and for which endpoint.
type fragmentSource struct {
	Module   string `json:"module"`
	Endpoint string `json:"endpoint"`
}

// info is the merged document's info section.
type info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

// document is the merged document. Its field order is the order the keys appear
// in the JSON it marshals to.
type document struct {
	OpenAPI    string                                `json:"openapi"`
	Info       info                                  `json:"info"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components map[string]map[string]json.RawMessage `json:"components"`
	Fragments  []fragmentSource                      `json:"x-fragments"`
}

// Merge combines every fragment into one document and reports a conflict
// instead of choosing between two declarations.
//
// It reads the whole slice it is given and takes nothing from a registry, a
// capability or a lifecycle: the caller decides which fragments those are, and
// the available set is what a reader hands it. An operation declared for the
// same path and method by two fragments is a conflict judged across endpoints
// as well as within one, because one document holds an operation once and a
// route served on two endpoints is two operations the paths cannot tell apart.
// A component declared under one section and name with different content is a
// conflict too; the same content declared twice merges into one entry, since
// several fragments sharing an error schema is ordinary.
func Merge(specs []core.Resource[speedhttp.Spec], opts Options) ([]byte, error) {
	doc := document{
		OpenAPI: openAPIVersion,
		// Options carries the same two fields info does, so it converts
		// directly.
		Info:       info(opts),
		Paths:      map[string]map[string]json.RawMessage{},
		Components: map[string]map[string]json.RawMessage{},
		Fragments:  []fragmentSource{},
	}
	owners := map[string]fragmentSource{}
	for _, res := range specs {
		part, err := readFragment(res.Module, res.Value.Document)
		if err != nil {
			return nil, err
		}
		source := fragmentSource{Module: res.Module, Endpoint: res.Value.Endpoint}
		doc.Fragments = append(doc.Fragments, source)
		if err := mergePaths(&doc, owners, source, part); err != nil {
			return nil, err
		}
		if err := mergeComponents(&doc, res.Module, part); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(doc.Fragments, func(a, b fragmentSource) int {
		return strings.Compare(a.Module+" "+a.Endpoint, b.Module+" "+b.Endpoint)
	})
	merged, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi: writing the merged document: %w", err)
	}
	return merged, nil
}

// readFragment reads one declared fragment into its paths and components. It
// insists on a JSON object at the top level, because a fragment that decodes to
// anything else contributes nothing and would otherwise be indistinguishable
// from one that declared nothing.
func readFragment(module string, declared []byte) (fragment, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(declared, &top); err != nil {
		return fragment{}, fmt.Errorf("%w: the fragment module %q declares is not a JSON "+
			"object: %w", ErrUnreadable, module, err)
	}
	// A literal null decodes into a nil map without an error, so it would reach
	// here as a document with no fields rather than as a decoding failure.
	if top == nil {
		return fragment{}, fmt.Errorf("%w: the fragment module %q declares is null, not a JSON "+
			"object", ErrUnreadable, module)
	}
	var part fragment
	if raw, ok := top["paths"]; ok {
		if err := json.Unmarshal(raw, &part.paths); err != nil {
			return fragment{}, fmt.Errorf("%w: the paths of the fragment module %q declares: %w",
				ErrUnreadable, module, err)
		}
	}
	if raw, ok := top["components"]; ok {
		if err := json.Unmarshal(raw, &part.components); err != nil {
			return fragment{}, fmt.Errorf("%w: the components of the fragment module %q declares: %w",
				ErrUnreadable, module, err)
		}
	}
	return part, nil
}

// mergePaths merges one fragment's operations into the document, judging each
// against the ones already merged and tagging it with the endpoint it is served
// on. owners is keyed by "path METHOD" and remembers the source of the
// operation each key was taken by.
func mergePaths(doc *document, owners map[string]fragmentSource, source fragmentSource, part fragment) error {
	for path, operations := range part.paths {
		merged := doc.Paths[path]
		if merged == nil {
			merged = map[string]json.RawMessage{}
			doc.Paths[path] = merged
		}
		for method, raw := range operations {
			where := strings.ToUpper(method) + " " + path
			if other, taken := owners[where]; taken {
				return fmt.Errorf("%w: %s is declared by module %q for endpoint %q and by "+
					"module %q for endpoint %q", ErrOperationConflict, where, other.Module,
					other.Endpoint, source.Module, source.Endpoint)
			}
			owners[where] = source
			tagged, err := withEndpoint(raw, source.Endpoint)
			if err != nil {
				return fmt.Errorf("%w: %s in the fragment module %q declares: %w",
					ErrUnreadable, where, source.Module, err)
			}
			merged[method] = tagged
		}
	}
	return nil
}

// mergeComponents merges one fragment's components into the document. The same
// name under one section merges into one entry when the content is equal and is
// a conflict when it is not; the conflict names the module that declared the
// later one, which is all this section-by-section merge holds of the two.
func mergeComponents(doc *document, module string, part fragment) error {
	for section, entries := range part.components {
		merged := doc.Components[section]
		if merged == nil {
			merged = map[string]json.RawMessage{}
			doc.Components[section] = merged
		}
		for name, value := range entries {
			if existing, taken := merged[name]; taken && !jsonEqual(existing, value) {
				return fmt.Errorf("%w: module %q declares components.%s.%s differently from "+
					"an earlier fragment", ErrComponentConflict, module, section, name)
			}
			merged[name] = value
		}
	}
	return nil
}

// withEndpoint adds the endpoint extension to one operation object.
func withEndpoint(raw json.RawMessage, endpoint string) (json.RawMessage, error) {
	var operation map[string]json.RawMessage
	if err := json.Unmarshal(raw, &operation); err != nil {
		return nil, err
	}
	if operation == nil {
		return nil, errors.New("the operation is not a JSON object")
	}
	name, err := json.Marshal(endpoint)
	if err != nil {
		return nil, err
	}
	operation[endpointExtension] = name
	return json.Marshal(operation)
}

// jsonEqual compares two JSON values by content rather than by spelling, so the
// same schema written with different key order or whitespace reads as equal.
func jsonEqual(a, b json.RawMessage) bool {
	left, okLeft := canonicalJSON(a)
	right, okRight := canonicalJSON(b)
	return okLeft && okRight && left == right
}

// canonicalJSON re-encodes one value with its keys sorted, so two spellings of
// the same value compare equal. Numbers are kept as their literal text: a plain
// decode into any would make every number a float64, and two integers differing
// past 2^53 would then compare equal — a difference this merge exists to report.
func canonicalJSON(v json.RawMessage) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(v))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return "", false
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return "", false
	}
	return string(out), true
}
