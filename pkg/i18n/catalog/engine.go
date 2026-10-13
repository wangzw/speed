package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"golang.org/x/text/language"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// Sentinel failures of loading the declared catalogs. Each is a startup failure:
// a catalog that does not load is one no message can be rendered from, and a
// key two modules both declare cannot be resolved to one message.
var (
	// ErrInvalidCatalog reports a declared catalog whose files are missing or
	// whose entries are not a JSON object of string to string.
	ErrInvalidCatalog = errors.New("i18n: a declared catalog is not well formed")
	// ErrDuplicateKey reports one message key declared by more than one module.
	ErrDuplicateKey = errors.New("i18n: two modules declare the same message key")
	// ErrNoCatalog reports that the default locale has no entries while other
	// locales do, so the fallback every message relies on cannot be rendered.
	ErrNoCatalog = errors.New("i18n: the default locale has no catalog")
)

// engine is the merged index. It is built once in New and read afterwards; no
// field is written after construction, so every method is safe to call from any
// goroutine.
type engine struct {
	// index is locale to key to message. A locale that a catalog did not cover
	// has no entry, and a key a locale did not carry has none under it.
	index map[i18n.Locale]map[i18n.Key]string
	// supported is the sorted set of locales some catalog carried.
	supported []i18n.Locale
	// matcher negotiates a request's language preferences against supported.
	matcher language.Matcher
	// profiles is every module that delivers i18n.Profile, in module-name order.
	profiles []i18n.Profile
}

// newEngine builds the index from the declared catalogs and holds the profiles
// that answer a user's stored preference off the request path.
func newEngine(catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile) (*engine, error) {
	index := map[i18n.Locale]map[i18n.Key]string{}
	// owner records which module declared a key, so a collision names both.
	owner := map[i18n.Key]string{}
	for _, catalog := range catalogs {
		entries, err := read(catalog.Value)
		if err != nil {
			return nil, fmt.Errorf("%w (declared by module %q): %w", ErrInvalidCatalog, catalog.Module, err)
		}
		for locale, messages := range entries {
			bucket := index[locale]
			if bucket == nil {
				bucket = map[i18n.Key]string{}
				index[locale] = bucket
			}
			for key, message := range messages {
				// A key is one declaration however many locales a module writes
				// it in; only a second *module* claiming it is a collision.
				if first, ok := owner[key]; ok && first != catalog.Module {
					return nil, fmt.Errorf("%w: key %q is declared by module %q and module %q",
						ErrDuplicateKey, string(key), first, catalog.Module)
				}
				owner[key] = catalog.Module
				bucket[key] = message
			}
		}
	}
	if err := checkDefault(index); err != nil {
		return nil, err
	}
	supported := make([]i18n.Locale, 0, len(index))
	for locale := range index {
		supported = append(supported, locale)
	}
	sort.Slice(supported, func(a, b int) bool { return supported[a] < supported[b] })
	// tags is built in the same order as supported, so the index the matcher
	// returns addresses both: Negotiate reads supported[index], and a tags slice
	// in another order would name a different locale for the same index.
	tags := make([]language.Tag, 0, len(supported))
	for _, locale := range supported {
		tag, err := language.Parse(string(locale))
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a language tag: %w", ErrInvalidCatalog, string(locale), err)
		}
		tags = append(tags, tag)
	}
	return &engine{
		index:     index,
		supported: supported,
		matcher:   language.NewMatcher(tags),
		profiles:  profiles,
	}, nil
}

// validate checks the declared catalogs without building the index, so the
// check runs in Prepare and a bad catalog terminates the startup before any
// instance is constructed.
func validate(catalogs []core.Resource[i18n.Catalog]) error {
	_, err := newEngine(catalogs, nil)
	return err
}

// checkDefault refuses a set of catalogs that carries messages but no entries
// in the default locale: every message a locale does not carry falls back to
// the default, and a set without it would render keys to every request.
func checkDefault(index map[i18n.Locale]map[i18n.Key]string) error {
	if len(index) == 0 {
		return nil
	}
	if _, ok := index[i18n.Default]; !ok {
		return fmt.Errorf("%w: no catalog carries %q", ErrNoCatalog, string(i18n.Default))
	}
	return nil
}

// read loads every message file a catalog selects. It returns, per locale, the
// message for each key. The locale is the file's base name without its
// extension; the file is a JSON object of string to string.
func read(catalog i18n.Catalog) (map[i18n.Locale]map[i18n.Key]string, error) {
	if catalog.FS == nil || catalog.Pattern == "" {
		return nil, fmt.Errorf("a catalog must name a filesystem and a pattern")
	}
	names, err := fs.Glob(catalog.FS, catalog.Pattern)
	if err != nil {
		return nil, fmt.Errorf("the pattern %q is malformed: %w", catalog.Pattern, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the pattern %q matched no file", catalog.Pattern)
	}
	out := map[i18n.Locale]map[i18n.Key]string{}
	for _, name := range names {
		locale := i18n.Locale(strings.TrimSuffix(path.Base(name), path.Ext(name)))
		if _, err := language.Parse(string(locale)); err != nil {
			return nil, fmt.Errorf("the file %q is not named for a language tag: %w", name, err)
		}
		raw, err := fs.ReadFile(catalog.FS, name)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", name, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("%q is not a JSON object of message text: %w", name, err)
		}
		bucket := out[locale]
		if bucket == nil {
			bucket = map[i18n.Key]string{}
			out[locale] = bucket
		}
		for key, message := range entries {
			bucket[i18n.Key(key)] = message
		}
	}
	return out, nil
}

// Localize renders key in the locale of ctx. A key the locale does not carry
// falls back to the default locale; a key no catalog carries is written back as
// the key, so a caller never receives an empty string it would have to test.
func (e *engine) Localize(ctx context.Context, key i18n.Key, args i18n.Args) string {
	locale := e.LocaleOf(ctx)
	if message, ok := e.index[locale][key]; ok {
		return interpolate(message, args)
	}
	if locale != i18n.Default {
		if message, ok := e.index[i18n.Default][key]; ok {
			return interpolate(message, args)
		}
	}
	return string(key)
}

// Translate renders key in the named locale and reports whether a catalog
// carried it. It does not fall back: a caller that names a locale gets that
// locale's message or nothing.
func (e *engine) Translate(locale i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	message, ok := e.index[locale][key]
	if !ok {
		return "", false
	}
	return interpolate(message, args), true
}

// Negotiate returns the supported locale that best matches an Accept-Language
// header value. A header naming only unsupported languages, or none, reports no
// match, which a caller reads as "leave the locale unset".
func (e *engine) Negotiate(acceptLanguage string) (i18n.Locale, bool) {
	if len(e.supported) == 0 {
		return "", false
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return "", false
	}
	_, index, confidence := e.matcher.Match(tags...)
	if confidence == language.No {
		return "", false
	}
	return e.supported[index], true
}

// LocaleOf is the locale of ctx, or the default when it carries none.
func (e *engine) LocaleOf(ctx context.Context) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	return i18n.Default
}

// TimeZoneOf is the time zone of ctx, or "" when it carries none, which reads
// as UTC.
func (e *engine) TimeZoneOf(ctx context.Context) string {
	name, _ := i18n.TimeZoneFromContext(ctx)
	return name
}

// LocaleFor is the locale for a named account: the context's when it carries
// one, otherwise the account's stored preference, otherwise the default.
func (e *engine) LocaleFor(ctx context.Context, account string) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	for _, profile := range e.profiles {
		if locale, ok := profile.LocaleFor(ctx, account); ok {
			return locale
		}
	}
	return i18n.Default
}

// TimeZoneFor is the time zone for a named account: the context's when it
// carries one, otherwise the account's stored preference, otherwise "".
func (e *engine) TimeZoneFor(ctx context.Context, account string) string {
	if name, ok := i18n.TimeZoneFromContext(ctx); ok {
		return name
	}
	for _, profile := range e.profiles {
		if name, ok := profile.TimeZoneFor(ctx, account); ok {
			return name
		}
	}
	return ""
}

// interpolate replaces each {{name}} in message with args[name]. A name with no
// value is left as written, so a missing parameter shows in the text rather than
// disappearing.
func interpolate(message string, args i18n.Args) string {
	if len(args) == 0 || !strings.Contains(message, "{{") {
		return message
	}
	for name, value := range args {
		message = strings.ReplaceAll(message, "{{"+name+"}}", value)
	}
	return message
}
