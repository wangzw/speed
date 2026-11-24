package config

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// transports are the Source and Format implementations this binary carries,
// indexed by the name each one answers for.
type transports struct {
	sources map[string]Source
	formats map[string]Format
}

// collectTransports gathers the transports and formats, refusing two that answer
// for the same name. Names are compared in lower case.
func collectTransports(reg *core.Registry) (*transports, error) {
	t := &transports{sources: map[string]Source{}, formats: map[string]Format{}}
	declaredBy := map[string]string{}
	for _, res := range core.Resources[Source](reg) {
		scheme := strings.ToLower(res.Value.Scheme())
		if other, taken := declaredBy[scheme]; taken {
			return nil, fmt.Errorf("%w: modules %q and %q both deliver a Source for the scheme "+
				"%q, and a scheme selects one transport exactly. Import one of them",
				ErrConfigConflict, other, res.Module, scheme)
		}
		declaredBy[scheme] = res.Module
		t.sources[scheme] = res.Value
	}
	declaredBy = map[string]string{}
	for _, res := range core.Resources[Format](reg) {
		name := strings.ToLower(res.Value.Name())
		if other, taken := declaredBy[name]; taken {
			return nil, fmt.Errorf("%w: modules %q and %q both deliver a Format named %q, and a "+
				"format name selects one parser exactly. Import one of them",
				ErrConfigConflict, other, res.Module, name)
		}
		declaredBy[name] = res.Module
		t.formats[name] = res.Value
	}
	return t, nil
}

// locate gives the config locator of this run: the host's default, then
// <PREFIX>_CONFIG, then --config, each overriding the one before it. All three
// silent is a legal configuration with no primary config source.
func locate(m *manifest, environ map[string]string, flagLocator string, flagGiven bool) string {
	locator := m.host.DefaultLocator
	if m.host.Prefix != "" {
		if given, set := environ[locatorEnvName(m.host.Prefix)]; set {
			locator = given
		}
	}
	if flagGiven {
		locator = flagLocator
	}
	return locator
}

// ReadPrimary reads the primary config source and puts it above the defaults. An
// empty locator means there is no primary source. The content is checked before
// it is merged in, since the layering tool folds keys in place. A second call
// panics.
func (l *loader) ReadPrimary(ctx context.Context, locator string) error {
	if l.readPrimary {
		panic("config: the primary config source was read twice. A run has one primary source, " +
			"and reading a second would stack it on the first instead of replacing it")
	}
	l.readPrimary = true
	if locator == "" {
		return nil
	}
	content, err := l.t.read(ctx, locator)
	if err != nil {
		return err
	}
	if err := validatePrimary(l.m, content); err != nil {
		return err
	}
	if cloned, ok := clonedTree(content).(map[string]any); ok {
		l.primary = cloned
	}
	return l.v.MergeConfigMap(content)
}

// clonedTree copies a parsed source so the layering tool's in-place folding
// leaves the copy alone.
func clonedTree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			out[k] = clonedTree(inner)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = clonedTree(inner)
		}
		return out
	default:
		return v
	}
}

// read fetches the primary config source and parses it. The transport answers
// where the bytes come from and the format how they are read.
func (t *transports) read(ctx context.Context, locator string) (map[string]any, error) {
	parsed, err := url.Parse(locator)
	if err != nil {
		return nil, fmt.Errorf("%w: %q does not parse as a URI: %w", ErrMalformedLocator, locator, err)
	}
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("%w: %q names no scheme, and the scheme is what selects the "+
			"transport. A local file is written file:///etc/app.yaml", ErrMalformedLocator, locator)
	}
	source, known := t.sources[parsed.Scheme]
	if !known {
		return nil, fmt.Errorf("%w: %q names the scheme %q, which no imported Source answers for. "+
			"Import a transport that answers for it: by convention that is "+
			"github.com/vislake/speed/pkg/config/source/%s. The convention is a lead, not a "+
			"guarantee: an implementation is free to live elsewhere, and this module cannot "+
			"enumerate the ones that exist",
			ErrUnknownScheme, locator, parsed.Scheme, parsed.Scheme)
	}

	raw, formatName, err := source.Fetch(ctx, parsed)
	if err != nil {
		return nil, fetchError(locator, err)
	}
	if formatName == "" {
		return nil, fmt.Errorf("%w: the transport for %q reported no format name. A file source "+
			"reads it off the extension and a remote one off the format query parameter; neither "+
			"guesses from the content, because valid JSON is also valid YAML",
			ErrUndeterminedFormat, locator)
	}
	formatName = strings.ToLower(formatName)
	format, known := t.formats[formatName]
	if !known {
		return nil, fmt.Errorf("%w: %q is in the %q format, which no imported Format parses. "+
			"Import a parser for it: by convention that is "+
			"github.com/vislake/speed/pkg/config/format/%s. The convention is a lead, not a "+
			"guarantee: an implementation is free to live elsewhere, and this module cannot "+
			"enumerate the ones that exist",
			ErrUnknownFormat, locator, formatName, formatName)
	}

	content, err := format.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %q does not parse as %s: %w", ErrMalformedConfig, locator, formatName, err)
	}
	if content == nil {
		content = map[string]any{}
	}
	return content, nil
}

// fetchError classifies what a transport reported, keeping the sentinel it
// already carried.
func fetchError(locator string, err error) error {
	for _, sentinel := range []error{ErrMalformedLocator, ErrUndeterminedFormat, ErrSourceUnavailable} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %q could not be read: %w", ErrSourceUnavailable, locator, err)
}
