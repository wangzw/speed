package config

import (
	"context"
	"net/url"
)

// Source is the transport extension point: one implementation per URI scheme,
// delivered as a resource.
type Source interface {
	// Scheme is the URI scheme this transport answers for, without the
	// separator, for example "file". It is registered in lower case.
	Scheme() string
	// Fetch reads the primary config source the locator points at and reports
	// the format name of the data it returns. The name is matched against the
	// registered formats, folded to lower case; config never sniffs content. A
	// locator this transport cannot serve is an error rather than a silent
	// fallback.
	Fetch(ctx context.Context, locator *url.URL) (data []byte, format string, err error)
}

// Format is the format extension point: one implementation per format name,
// delivered as a resource.
type Format interface {
	// Name is the format name, for example "json". It is registered in lower
	// case.
	Name() string
	// Unmarshal decodes the data into a tree of string-keyed maps. A non-string
	// key anywhere in the tree, and a key given twice in one mapping, are errors.
	Unmarshal(data []byte) (map[string]any, error)
}

// Reader is how a module reads its own values back. It carries reads only:
// configuration is a snapshot taken at startup.
type Reader interface {
	// Decode writes the values under a path into target. The path is the
	// complete one in the config data, namespace plus mount path. Defaults arrive
	// through the base layer, and a path never declared leaves target as it is.
	//
	// A required item no source gave is ErrMissingRequired when its module is one
	// this run takes in at run level; the required items of other modules are not
	// judged. Decode panics, rather than returning an error, when target is not a
	// pointer, is nil, or is a struct that is cyclic: that is a programming error
	// at the call site.
	Decode(path string, target any) error
}
