// Package openapi merges the OpenAPI fragments the modules of a binary declare
// into one document. Merge is a pure function shared by the ops module and a
// build tool.
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
	openAPIVersion     = "3.1.0"
	endpointExtension  = "x-endpoint"
	fragmentsExtension = "x-fragments"
)

var (
	// ErrUnreadable reports a fragment that cannot be read as a JSON object, naming
	// the module that declared it.
	ErrUnreadable = errors.New("openapi: a fragment cannot be read")
	// ErrOperationConflict reports the same path and method declared by two
	// fragments, naming both declaring modules and the endpoint each declared it
	// for.
	ErrOperationConflict = errors.New("openapi: two fragments declare the same operation")
	// ErrComponentConflict reports one component declared with different content
	// under one section.
	ErrComponentConflict = errors.New("openapi: two fragments declare one component differently")
)

// Options carries the values the merged document's top level takes beside the
// fragments.
type Options struct {
	// Title is the document's info.title.
	Title string
	// Version is the document's info.version.
	Version string
	// Description is the document's info.description; empty leaves it out.
	Description string
	// Servers are the origins the API is reached at; empty leaves the list out.
	Servers []string
}

type fragment struct {
	paths      map[string]map[string]json.RawMessage
	components map[string]map[string]json.RawMessage
}

type fragmentSource struct {
	Module   string `json:"module"`
	Endpoint string `json:"endpoint"`
}

type info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type server struct {
	URL string `json:"url"`
}

type tag struct {
	Name string `json:"name"`
}

type document struct {
	OpenAPI    string                                `json:"openapi"`
	Info       info                                  `json:"info"`
	Servers    []server                              `json:"servers,omitempty"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components map[string]map[string]json.RawMessage `json:"components"`
	Tags       []tag                                 `json:"tags,omitempty"`
	Fragments  []fragmentSource                      `json:"x-fragments"`
}

var securitySchemes = map[string]any{
	"bearerAuth": map[string]any{
		"type":        "http",
		"scheme":      "bearer",
		"description": "A credential in the Authorization header, written by the caller. The gateway reads the scheme and hands the credential to the backend that declares it.",
	},
	"sessionCookie": map[string]any{
		"type":        "apiKey",
		"in":          "cookie",
		"name":        "__Host-authn-sid",
		"description": "The session cookie a browser attaches by itself. It is __Host-authn-sid; __Secure-authn-sid when the deployment configures a cookie domain; authn-sid when the public URL is a loopback http address, where WebKit refuses a prefixed cookie.",
	},
}

// Merge combines every fragment into one document and reports a conflict instead
// of choosing between two declarations.
func Merge(specs []core.Resource[speedhttp.Spec], opts Options) ([]byte, error) {
	doc := document{
		OpenAPI: openAPIVersion,
		Info: info{
			Title:       opts.Title,
			Version:     opts.Version,
			Description: opts.Description,
		},
		Servers:    serversOf(opts.Servers),
		Paths:      map[string]map[string]json.RawMessage{},
		Components: map[string]map[string]json.RawMessage{},
		Fragments:  []fragmentSource{},
	}
	owners := map[string]fragmentSource{}
	groups := map[string]bool{}
	for _, res := range specs {
		part, err := readFragment(res.Module, res.Value.Document)
		if err != nil {
			return nil, err
		}
		source := fragmentSource{Module: res.Module, Endpoint: res.Value.Endpoint}
		doc.Fragments = append(doc.Fragments, source)
		groups[res.Module] = true
		if err := mergePaths(&doc, owners, source, res.Value, part); err != nil {
			return nil, err
		}
		if err := mergeComponents(&doc, res.Module, part); err != nil {
			return nil, err
		}
	}
	if err := addSecuritySchemes(&doc); err != nil {
		return nil, err
	}
	doc.Tags = tagsOf(groups)
	slices.SortFunc(doc.Fragments, func(a, b fragmentSource) int {
		return strings.Compare(a.Module+" "+a.Endpoint, b.Module+" "+b.Endpoint)
	})
	merged, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi: writing the merged document: %w", err)
	}
	return append(merged, '\n'), nil
}

func readFragment(module string, declared []byte) (fragment, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(declared, &top); err != nil {
		return fragment{}, fmt.Errorf("%w: the fragment module %q declares is not a JSON "+
			"object: %w", ErrUnreadable, module, err)
	}
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

func mergePaths(doc *document, owners map[string]fragmentSource, source fragmentSource, spec speedhttp.Spec, part fragment) error {
	for path, operations := range part.paths {
		absolute := spec.ResolvePath(path)
		merged := doc.Paths[absolute]
		if merged == nil {
			merged = map[string]json.RawMessage{}
			doc.Paths[absolute] = merged
		}
		for method, raw := range operations {
			where := strings.ToUpper(method) + " " + absolute
			if other, taken := owners[where]; taken {
				return fmt.Errorf("%w: %s is declared by module %q for endpoint %q and by "+
					"module %q for endpoint %q", ErrOperationConflict, where, other.Module,
					other.Endpoint, source.Module, source.Endpoint)
			}
			owners[where] = source
			tagged, err := annotate(raw, source)
			if err != nil {
				return fmt.Errorf("%w: %s in the fragment module %q declares: %w",
					ErrUnreadable, where, source.Module, err)
			}
			merged[method] = tagged
		}
	}
	return nil
}

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

// annotate adds the endpoint extension, the declaring module's tag and, for an
// operation that declared none, the requirement that admits an identified
// caller.
func annotate(raw json.RawMessage, source fragmentSource) (json.RawMessage, error) {
	var operation map[string]json.RawMessage
	if err := json.Unmarshal(raw, &operation); err != nil {
		return nil, err
	}
	if operation == nil {
		return nil, errors.New("the operation is not a JSON object")
	}
	endpoint, err := json.Marshal(source.Endpoint)
	if err != nil {
		return nil, err
	}
	operation[endpointExtension] = endpoint
	var tags []string
	if declared, found := operation["tags"]; found {
		if err = json.Unmarshal(declared, &tags); err != nil {
			return nil, fmt.Errorf("its tags are not a string array: %w", err)
		}
	}
	if !slices.Contains(tags, source.Module) {
		tags = append(tags, source.Module)
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	operation["tags"] = encoded
	if _, found := operation["security"]; !found {
		operation["security"] = requiredSecurity
	}
	return json.Marshal(operation)
}

var requiredSecurity = json.RawMessage(`[{"bearerAuth":[]},{"sessionCookie":[]}]`)

func addSecuritySchemes(doc *document) error {
	section := doc.Components["securitySchemes"]
	if section == nil {
		section = map[string]json.RawMessage{}
		doc.Components["securitySchemes"] = section
	}
	names := make([]string, 0, len(securitySchemes))
	for name := range securitySchemes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		encoded, err := json.Marshal(securitySchemes[name])
		if err != nil {
			return fmt.Errorf("openapi: writing the %s security scheme: %w", name, err)
		}
		if existing, taken := section[name]; taken && !jsonEqual(existing, encoded) {
			return fmt.Errorf("%w: components.securitySchemes.%s is declared differently from the platform's",
				ErrComponentConflict, name)
		}
		section[name] = encoded
	}
	return nil
}

func serversOf(urls []string) []server {
	if len(urls) == 0 {
		return nil
	}
	out := make([]server, 0, len(urls))
	for _, url := range urls {
		out = append(out, server{URL: url})
	}
	return out
}

func tagsOf(groups map[string]bool) []tag {
	if len(groups) == 0 {
		return nil
	}
	out := make([]tag, 0, len(groups))
	for name := range groups {
		out = append(out, tag{Name: name})
	}
	slices.SortFunc(out, func(a, b tag) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func jsonEqual(a, b json.RawMessage) bool {
	left, okLeft := canonicalJSON(a)
	right, okRight := canonicalJSON(b)
	return okLeft && okRight && left == right
}

// canonicalJSON re-encodes one value with its keys sorted; numbers keep their
// literal text, because a plain decode into any would collapse two integers that
// differ past 2^53 into one float64.
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
