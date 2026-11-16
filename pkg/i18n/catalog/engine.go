package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// Sentinel failures of loading the message sources. Each is a startup failure: a
// source that does not load is one no message can be rendered from, a key two
// modules both declare cannot be resolved to one message, and a configured
// overlay holding a malformed file is a directory the run cannot be built from.
var (
	// ErrInvalidCatalog reports a declared catalog whose files are missing or
	// whose entries are not a JSON object of string to string.
	ErrInvalidCatalog = errors.New("i18n: a declared catalog is not well formed")
	// ErrDuplicateKey reports one message key declared by more than one module.
	ErrDuplicateKey = errors.New("i18n: two modules declare the same message key")
	// ErrNoCatalog reports that the configured default locale has no entries
	// while other locales do, so the fallback every message relies on cannot be
	// rendered.
	ErrNoCatalog = errors.New("i18n: the default locale has no catalog")
	// ErrInvalidOverlay reports a configured catalogs directory holding a file
	// that is not a message catalog: its base name is not a language tag, or its
	// body is not a JSON object of string to string. It is distinct from
	// ErrInvalidCatalog, whose message names a catalog a module declares.
	ErrInvalidOverlay = errors.New("i18n: a configured catalogs directory holds a file that is not a well-formed catalog")
	// ErrInvalidMessage reports a message the engine cannot parse: a segment
	// naming a keyword other than plural or time, a plural segment naming a
	// category that is not one of the six CLDR categories, one whose branches
	// omit "other" or repeat a category, or a time segment carrying an empty
	// layout. The failure names the key and the source it came from. It is
	// distinct from the sentinels of the sources: the message text itself is
	// malformed, not the file that carries it.
	ErrInvalidMessage = errors.New("i18n: a message is not well formed")
)

// engine is the merged index. It is built once in New and read afterwards. Every
// field but the zone cache is written only at construction, so every method is
// safe to call from any goroutine; the zone cache is the one field written after
// construction, guarded by mu.
type engine struct {
	// index is locale to key to parsed message. A locale that a catalog did not
	// cover has no entry, and a key a locale did not carry has none under it.
	// Every message is parsed and validated at assembly, so rendering never
	// parses again and a malformed message stops startup.
	index map[i18n.Locale]map[i18n.Key]*template
	// supported is the sorted set of locales a declared catalog or the overlay
	// carried.
	supported []i18n.Locale
	// matcher negotiates a request's language preferences against supported.
	matcher language.Matcher
	// localeTags is the parsed tag per supported locale, kept for plural-form
	// selection, which needs the tag rather than the locale string.
	localeTags map[i18n.Locale]language.Tag
	// printers is a locale-aware number formatter per supported locale, built at
	// assembly so the request path allocates nothing for it. A *message.Printer
	// is read-only after construction, so sharing one across goroutines is safe.
	printers map[i18n.Locale]*message.Printer
	// profiles is every module that delivers i18n.Profile, in module-name order.
	profiles []i18n.Profile
	// defaultLocale is the configured locale a request that names none resolves
	// to, and the fallback for a key a catalog misses.
	defaultLocale i18n.Locale
	// mu guards locations, the only field written after construction.
	mu sync.RWMutex
	// locations caches a loaded *time.Location by the IANA name it was loaded
	// under, so a time form renders without a filesystem read after the first
	// lookup of each name. time.LoadLocation re-reads the zoneinfo files on every
	// call — the standard library keeps no cross-call cache — so the cache is
	// held here. A name this process cannot load is cached as UTC too, so a bad
	// name does not re-read the filesystem on every render. The map has an entry
	// only once a time form naming it is first rendered, so a run that renders no
	// time form never touches mu.
	locations map[string]*time.Location
}

// newEngine builds the index from the declared catalogs and then the configured
// overlay directory, holds the profiles that answer a user's stored preference
// off the request path, and records the configured default locale the resolver
// and the fallbacks use. The overlay is read here and merged after the declared
// catalogs, so it wins on a shared key and adds a locale no declared catalog
// carries; the default check runs after the merge, so the overlay may supply it.
func newEngine(catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, defaultLocale i18n.Locale, catalogsDir string) (*engine, error) {
	index := map[i18n.Locale]map[i18n.Key]*template{}
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
				bucket = map[i18n.Key]*template{}
				index[locale] = bucket
			}
			for key, raw := range messages {
				// A key is one declaration however many locales a module writes
				// it in; only a second *module* claiming it is a collision.
				if first, ok := owner[key]; ok && first != catalog.Module {
					return nil, fmt.Errorf("%w: key %q is declared by module %q and module %q",
						ErrDuplicateKey, string(key), first, catalog.Module)
				}
				parsed, err := parseMessage(raw)
				if err != nil {
					return nil, fmt.Errorf("%w: key %q declared by module %q: %w",
						ErrInvalidMessage, string(key), catalog.Module, err)
				}
				owner[key] = catalog.Module
				bucket[key] = parsed
			}
		}
	}
	overlay, err := readOverlay(catalogsDir)
	if err != nil {
		return nil, err
	}
	// The overlay is not a module declaration: it overwrites a key a declared
	// catalog carries rather than colliding with it, and its keys take no part in
	// the owner check that refuses two modules claiming one key.
	for locale, messages := range overlay {
		bucket := index[locale]
		if bucket == nil {
			bucket = map[i18n.Key]*template{}
			index[locale] = bucket
		}
		for key, raw := range messages {
			parsed, err := parseMessage(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: key %q in the configured catalogs directory %q: %w",
					ErrInvalidMessage, string(key), catalogsDir, err)
			}
			bucket[key] = parsed
		}
	}
	if err := checkDefault(index, defaultLocale); err != nil {
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
	localeTags := make(map[i18n.Locale]language.Tag, len(supported))
	printers := make(map[i18n.Locale]*message.Printer, len(supported))
	for _, locale := range supported {
		tag, err := language.Parse(string(locale))
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a language tag: %w", ErrInvalidCatalog, string(locale), err)
		}
		tags = append(tags, tag)
		localeTags[locale] = tag
		// A printer built on the tag formats a number for the locale: the decimal
		// formatter it holds groups and separates digits by CLDR data. It needs no
		// catalog — the message text is already in the index — only the tag.
		printers[locale] = message.NewPrinter(tag)
	}
	return &engine{
		index:         index,
		supported:     supported,
		matcher:       language.NewMatcher(tags),
		localeTags:    localeTags,
		printers:      printers,
		profiles:      profiles,
		defaultLocale: defaultLocale,
		locations:     map[string]*time.Location{},
	}, nil
}

// validate checks the declared catalogs and the configured overlay against the
// default without building the index, so the check runs in Prepare and a bad
// catalog or overlay terminates the startup before any instance is constructed.
func validate(catalogs []core.Resource[i18n.Catalog], defaultLocale i18n.Locale, catalogsDir string) error {
	_, err := newEngine(catalogs, nil, defaultLocale, catalogsDir)
	return err
}

// overlayPattern selects the message files a configured catalogs directory
// holds: one file per locale, named for the locale it carries, as "{tag}.json".
const overlayPattern = "*.json"

// readOverlay reads the extra catalogs a configured directory holds, through the
// same loader the declared catalogs go through. An unset directory, a path that
// names no existing directory, and a directory holding no *.json file all mean
// "no extras" and are legal: the overlay is an addition to the catalogs a run
// ships, not a source it depends on. A file that does not load as a catalog
// fails startup under ErrInvalidOverlay, naming the directory and the file.
//
// The directory is read here, once, at assembly. Nothing reads it again, so
// reading a message costs no IO and takes no lock, exactly as it does for the
// declared catalogs. Rendering a message that carries a time form is the one
// exception, and it is the engine's own: a zone name it has not seen before is
// read from the filesystem once, behind the engine's mutex.
func readOverlay(dir string) (map[i18n.Locale]map[i18n.Key]string, error) {
	if dir == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		// A path that names no existing directory, or names a file rather than a
		// directory, is the legal "no extras".
		return nil, nil
	}
	fsys := os.DirFS(dir)
	// The loader treats a pattern that matches no file as an error, which is
	// right for a declared catalog — a module that ships none is a mistake — and
	// wrong for the overlay, where an empty directory is a legal "no extras".
	// Find the files first, so that error never surfaces for this source.
	names, err := fs.Glob(fsys, overlayPattern)
	if err != nil {
		return nil, fmt.Errorf("%w: the pattern %q is malformed: %w", ErrInvalidOverlay, overlayPattern, err)
	}
	if len(names) == 0 {
		return nil, nil
	}
	entries, err := read(i18n.Catalog{FS: fsys, Pattern: overlayPattern})
	if err != nil {
		return nil, fmt.Errorf("%w (in %q): %w", ErrInvalidOverlay, dir, err)
	}
	return entries, nil
}

// checkDefault refuses a set of catalogs that carries messages but no entries
// in the configured default locale: every message a locale does not carry falls
// back to the default, and a set without it would render keys to every request.
func checkDefault(index map[i18n.Locale]map[i18n.Key]*template, defaultLocale i18n.Locale) error {
	if len(index) == 0 {
		return nil
	}
	if _, ok := index[defaultLocale]; !ok {
		return fmt.Errorf("%w: no catalog carries %q", ErrNoCatalog, string(defaultLocale))
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
	if msg, ok := e.index[locale][key]; ok {
		return e.render(ctx, msg, args, locale)
	}
	if locale != e.defaultLocale {
		if msg, ok := e.index[e.defaultLocale][key]; ok {
			return e.render(ctx, msg, args, e.defaultLocale)
		}
	}
	return string(key)
}

// Translate renders key in the named locale and reports whether a catalog
// carried it. It does not fall back: a caller that names a locale gets that
// locale's message or nothing. It carries no context, so a time form rendered
// through it reads the zone as unset, which is UTC.
func (e *engine) Translate(locale i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	msg, ok := e.index[locale][key]
	if !ok {
		return "", false
	}
	return e.render(context.Background(), msg, args, locale), true
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

// Default is the locale a request that names none resolves to, and the fallback
// for a key a catalog misses.
func (e *engine) Default() i18n.Locale {
	return e.defaultLocale
}

// LocaleOf is the locale of ctx, or the configured default when it carries none.
func (e *engine) LocaleOf(ctx context.Context) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	return e.defaultLocale
}

// TimeZoneOf is the time zone of ctx, or "" when it carries none, which reads
// as UTC.
func (e *engine) TimeZoneOf(ctx context.Context) string {
	name, _ := i18n.TimeZoneFromContext(ctx)
	return name
}

// LocaleFor is the locale for a named account: the context's when it carries
// one, otherwise the first stored preference a profile answers, otherwise the
// default. Every profile is asked even after one reports a failed read: the
// error is swallowed and the next profile answers, because one profile's
// failure says nothing about another's answer and a preference a later one
// stores is still the account's. When no profile answers, the default decides.
func (e *engine) LocaleFor(ctx context.Context, account string) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	for _, profile := range e.profiles {
		locale, ok, err := profile.LocaleFor(ctx, account)
		if err != nil {
			continue
		}
		if ok {
			return locale
		}
	}
	return e.defaultLocale
}

// TimeZoneFor is the time zone for a named account: the context's when it
// carries one, otherwise the account's stored preference, otherwise "". A
// profile that cannot be read stops the walk and reports its failure rather
// than reading as "no preference stored": the caller renders in UTC for the
// latter, and a failed read must not freeze a wrong instant into a message.
func (e *engine) TimeZoneFor(ctx context.Context, account string) (string, bool, error) {
	if name, ok := i18n.TimeZoneFromContext(ctx); ok {
		return name, true, nil
	}
	for _, profile := range e.profiles {
		name, ok, err := profile.TimeZoneFor(ctx, account)
		if err != nil {
			return "", false, err
		}
		if ok {
			return name, true, nil
		}
	}
	return "", false, nil
}

// template is a message parsed into its literal text and its placeholders, in
// order. It is parsed and validated at assembly, so rendering walks the nodes
// rather than scanning the text again.
type template struct {
	nodes []node
}

// node is one piece of a parsed message: a literal run of text followed by at
// most one placeholder.
type node struct {
	// text is the literal text this node writes before its placeholder.
	text string
	// raw is the placeholder as written — "{{name}}" or the whole parameterised
	// segment — kept so that a placeholder whose value is missing is written back
	// as it stood, by the same rule a plain {{name}} follows.
	raw string
	// arg is the parameter a plain {{arg}} placeholder interpolates, "" for a
	// literal-only node.
	arg string
	// group is the parsed body of a {{arg, plural, ...}} placeholder, nil for a
	// plain, literal-only or time node.
	group *pluralGroup
	// time is the parsed body of a {{arg, time, layout}} placeholder, nil for a
	// plain, literal-only or plural node.
	time *timeForm
}

// pluralGroup is a parsed {{name, plural, ...}} placeholder: the parameter it
// selects by and its branches by category.
type pluralGroup struct {
	arg      string
	branches map[plural.Form]string
}

// timeForm is a parsed {{name, time, layout}} placeholder: the parameter it
// formats and the Go layout it formats with.
type timeForm struct {
	arg    string
	layout string
}

// categoryNames are the six CLDR plural categories a branch may name, in the
// order the error message lists them.
var categoryNames = []string{"zero", "one", "two", "few", "many", "other"}

// categoryToForm maps a CLDR category name to the library's Form. The library's
// own name-to-form table is unexported, so the mapping is written here.
var categoryToForm = map[string]plural.Form{
	"zero":  plural.Zero,
	"one":   plural.One,
	"two":   plural.Two,
	"few":   plural.Few,
	"many":  plural.Many,
	"other": plural.Other,
}

// parseMessage parses a message into its nodes and validates every plural
// segment it carries. It is called at assembly, so a malformed message stops
// startup rather than failing at request time.
func parseMessage(raw string) (*template, error) {
	m := &template{}
	rest := raw
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			m.nodes = append(m.nodes, node{text: rest})
			return m, nil
		}
		close := strings.Index(rest[open:], "}}")
		if close < 0 {
			// An opening {{ with no closing }} is literal text, as it always was.
			m.nodes = append(m.nodes, node{text: rest})
			return m, nil
		}
		close += open
		n := node{text: rest[:open], raw: rest[open : close+2]}
		inner := rest[open+2 : close]
		if strings.Contains(inner, ",") {
			if err := parseSegment(&n, inner); err != nil {
				return nil, err
			}
		} else {
			n.arg = inner
		}
		m.nodes = append(m.nodes, n)
		rest = rest[close+2:]
	}
}

// parseSegment parses the body of a placeholder that carries a comma — " name,
// keyword, body" — into the node's argument and either a plural group or a time
// form, decided by the keyword. It is called at assembly, so a malformed segment
// stops startup rather than failing at request time.
func parseSegment(n *node, inner string) error {
	name, rest, _ := strings.Cut(inner, ",")
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a parameterised segment names no parameter")
	}
	keyword, body, _ := strings.Cut(rest, ",")
	switch strings.TrimSpace(keyword) {
	case "plural":
		// The message parser cuts a placeholder at its first "}}", so a branch
		// carrying a nested placeholder truncates the segment before this point. A
		// brace left in the body is that case, and it is reported as itself rather
		// than as the missing "other" branch the truncation would otherwise produce.
		if strings.Contains(inner, "{{") {
			return errors.New("a plural branch may not contain a nested placeholder")
		}
		branches, err := parseBranches(body)
		if err != nil {
			return err
		}
		n.arg = name
		n.group = &pluralGroup{arg: name, branches: branches}
		return nil
	case "time":
		// The layout is everything after the second comma, holding the
		// conventional separating spaces aside. It is not split on its commas: a
		// Go layout may itself carry one, as "Mon, 02 Jan 2006 15:04" does.
		layout := strings.TrimSpace(body)
		if layout == "" {
			return fmt.Errorf("the time segment for %q carries an empty layout", name)
		}
		n.arg = name
		n.time = &timeForm{arg: name, layout: layout}
		return nil
	default:
		return fmt.Errorf("the segment for %q names %q; want \"plural\" or \"time\"", name, strings.TrimSpace(keyword))
	}
}

// parseBranches parses the branch list of a plural segment into a map from
// category to branch text, validating the category names, the presence of
// "other" and the absence of a repeated category.
func parseBranches(s string) (map[plural.Form]string, error) {
	branches := map[plural.Form]string{}
	for _, token := range splitBranches(s) {
		form, ok := categoryToForm[token.name]
		if !ok {
			return nil, fmt.Errorf("%q is not a CLDR plural category (want one of %s)",
				token.name, strings.Join(categoryNames, ", "))
		}
		if _, dup := branches[form]; dup {
			return nil, fmt.Errorf("the category %q appears more than once", token.name)
		}
		branches[form] = token.text
	}
	if len(branches) == 0 {
		return nil, errors.New("a plural segment carries no branch")
	}
	if _, ok := branches[plural.Other]; !ok {
		return nil, errors.New(`a plural segment carries no "other" branch`)
	}
	return branches, nil
}

// branchToken is a category name and its branch text as split off, before the
// name is validated.
type branchToken struct {
	name string
	text string
}

// splitBranches splits a branch list into "category=text" tokens. A comma
// begins a new branch when the text after it (ignoring spaces) is a word
// immediately followed by "="; a comma anywhere else is part of the branch text,
// so "other=# items, and more" keeps its comma. The boundary test does not check
// the word against the CLDR categories — parseBranches does — so a misspelled
// category name is reported rather than swallowed into the branch before it.
func splitBranches(s string) []branchToken {
	var tokens []branchToken
	s = strings.TrimLeft(s, " \t,")
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			// No "=": the whole remainder is one token, whose name will not match
			// a category and is reported as such.
			tokens = append(tokens, branchToken{name: strings.TrimSpace(s)})
			break
		}
		token := branchToken{name: strings.TrimSpace(s[:eq])}
		body := s[eq+1:]
		if cut := nextBranchStart(body); cut >= 0 {
			token.text = body[:cut]
			s = body[cut+1:]
		} else {
			token.text = body
			s = ""
		}
		tokens = append(tokens, token)
		s = strings.TrimLeft(s, " \t,")
	}
	return tokens
}

// nextBranchStart returns the index of the comma in body that begins the next
// branch, or -1 when body holds only the current branch's text.
func nextBranchStart(body string) int {
	for i := 0; i < len(body); i++ {
		if body[i] == ',' && beginsBranch(body[i+1:]) {
			return i
		}
	}
	return -1
}

// beginsBranch reports whether s begins (ignoring leading spaces) with a branch
// boundary: a word — a run of characters up to a space or "=" — followed by "=".
func beginsBranch(s string) bool {
	s = strings.TrimLeft(s, " \t")
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '=' {
		i++
	}
	if i == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(s[i:], " \t"), "=")
}

// render writes a parsed message with its parameters. Each {{name}} is replaced
// by its value formatted for locale: a string verbatim, a number by the locale's
// printer. A name with no value is left as written. A plural segment selects its
// branch by its integer value's CLDR category for locale, and # in the branch
// stands for the count formatted for locale. A time segment formats its instant
// with its layout in the zone ctx names, or — under TimeRenderMarked — writes a
// marker naming the parameter for the reader to resolve.
func (e *engine) render(ctx context.Context, m *template, args i18n.Args, locale i18n.Locale) string {
	// A message with no placeholder writes back the text it was parsed from, so
	// nothing below runs for the common case.
	if len(m.nodes) == 1 && m.nodes[0].arg == "" && m.nodes[0].group == nil && m.nodes[0].time == nil {
		return m.nodes[0].text
	}
	mode := i18n.TimeRenderFromContext(ctx)
	var b strings.Builder
	for _, n := range m.nodes {
		b.WriteString(n.text)
		switch {
		case n.group != nil:
			b.WriteString(e.renderPlural(n.group, n.raw, args, locale))
		case n.time != nil:
			b.WriteString(e.renderTime(ctx, n.time, n.raw, args, mode))
		case n.arg != "":
			b.WriteString(e.renderArg(n.arg, n.raw, args, locale, mode))
		}
	}
	return b.String()
}

// renderArg renders one {{name}} placeholder. A name with no value is left as
// written; a string is written verbatim; a number is formatted for locale. A
// time has no layout here, so it is left as written in a plain placeholder in
// the formatted mode; the marked mode writes the marker instead, since the
// reader resolves the parameter without needing a layout. A string in the
// marked mode has the marker delimiters removed first.
func (e *engine) renderArg(name, raw string, args i18n.Args, locale i18n.Locale, mode i18n.TimeRender) string {
	v, ok := args[name]
	if !ok {
		return raw
	}
	switch i18n.KindOf(v) {
	case i18n.KindString:
		if mode == i18n.TimeRenderMarked {
			return i18n.StripTimeMarkerDelimiters(i18n.StringOf(v))
		}
		return i18n.StringOf(v)
	case i18n.KindInt:
		return e.formatInt(i18n.IntOf(v), locale)
	case i18n.KindFloat:
		return e.formatFloat(i18n.FloatOf(v), locale)
	case i18n.KindTime:
		// A plain placeholder carries no layout, so the marked mode is the one
		// place a time can be written for a client to resolve: the client's
		// formatter needs only the parameter name.
		if mode == i18n.TimeRenderMarked {
			return i18n.MarkedTime(name)
		}
	}
	return raw
}

// renderPlural renders one {{name, plural, ...}} placeholder. The parameter must
// be an integer; a parameter that is missing or of another type leaves the
// segment as written. The branch is the one named for the count's CLDR category
// in locale, falling back to "other" when the message carries no branch for it.
func (e *engine) renderPlural(g *pluralGroup, raw string, args i18n.Args, locale i18n.Locale) string {
	v, ok := args[g.arg]
	if !ok || i18n.KindOf(v) != i18n.KindInt {
		return raw
	}
	count := i18n.IntOf(v)
	text, ok := g.branches[e.pluralForm(locale, count)]
	if !ok {
		text = g.branches[plural.Other]
	}
	return strings.ReplaceAll(text, "#", e.formatInt(count, locale))
}

// renderTime renders one {{name, time, layout}} placeholder. The parameter must
// carry a time; one that is missing or of another kind leaves the segment as
// written, the rule a plural segment follows for a wrong-kind value. The instant
// is formatted with the layout the message carries, in the zone ctx names: the
// zone is the context's IANA name, read as UTC when the context names none or
// when this process cannot load the name. Under TimeRenderMarked the segment is
// replaced by the marker instead, without reading the value or the zone.
func (e *engine) renderTime(ctx context.Context, tf *timeForm, raw string, args i18n.Args, mode i18n.TimeRender) string {
	if mode == i18n.TimeRenderMarked {
		// The marker names the parameter and carries neither the instant nor the
		// layout nor the zone: the reader resolves the value it already has in the
		// display zone and language it has now, so a stored message follows both.
		return i18n.MarkedTime(tf.arg)
	}
	v, ok := args[tf.arg]
	if !ok || i18n.KindOf(v) != i18n.KindTime {
		return raw
	}
	name, _ := i18n.TimeZoneFromContext(ctx)
	return i18n.TimeOf(v).In(e.location(name)).Format(tf.layout)
}

// location is the *time.Location for an IANA name, read as UTC for the empty
// name, for Go's own special name "Local", and for a name this process cannot
// load.
//
// "Local" is answered here rather than resolved: time.LoadLocation("Local")
// succeeds and yields the host's own zone, so a "Local" that reached the
// context would render user-visible text in whatever zone the process happens
// to run in. The write side refuses the name as a preference too
// (ValidTimeZone), but the context accessor is exported and takes a value
// unexamined, so the read path keeps its own guard instead of trusting where
// the value came from.
//
// A loaded location is cached so that a name reads the filesystem once:
// time.LoadLocation re-reads the zoneinfo files on every call — the standard
// library keeps no cross-call cache. The cache is guarded by mu, which is taken
// here and nowhere else, so a message that carries no time form touches neither
// the lock nor the filesystem.
func (e *engine) location(name string) *time.Location {
	if name == "" || name == "Local" {
		return time.UTC
	}
	e.mu.RLock()
	loc, ok := e.locations[name]
	e.mu.RUnlock()
	if ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// A name this process cannot load renders as UTC rather than failing, and
		// is cached as UTC so it does not re-read the filesystem on every render.
		loc = time.UTC
	}
	e.mu.Lock()
	e.locations[name] = loc
	e.mu.Unlock()
	return loc
}

// pluralForm is the CLDR cardinal category the count falls in for locale. CLDR
// defines the operand as the absolute value of the source number, so a negative
// count selects the same category as its magnitude; the selection is taken on
// the magnitude while the count itself is printed with its sign, wherever it is
// printed. The rule engine indexes an inclusion mask by the operand's remainder,
// which is negative for a negative operand in Go and would index out of range.
func (e *engine) pluralForm(locale i18n.Locale, count int64) plural.Form {
	tag, ok := e.localeTags[locale]
	if !ok {
		tag = language.Make(string(locale))
	}
	if count < 0 {
		count = -count
	}
	return plural.Cardinal.MatchPlural(tag, int(count), 0, 0, 0, 0)
}

// formatInt renders an integer for locale, groups and separators by CLDR data.
func (e *engine) formatInt(n int64, locale i18n.Locale) string {
	return e.printer(locale).Sprintf("%d", n)
}

// formatFloat renders a number for locale as a plain decimal, grouped and
// separated by CLDR data. number.Decimal forces the decimal form the criterion
// asks for, where the printer's %v verb would switch to scientific notation at
// 1e6 (1000000 renders as "1×10⁰⁶" rather than "1,000,000").
func (e *engine) formatFloat(f float64, locale i18n.Locale) string {
	return e.printer(locale).Sprint(number.Decimal(f))
}

// printer is the locale-aware number formatter for locale. The one built at
// assembly is read-only and shared; a locale that did not enter the supported
// set — which a rendered message never names — gets a formatter on demand.
func (e *engine) printer(locale i18n.Locale) *message.Printer {
	if p, ok := e.printers[locale]; ok {
		return p
	}
	return message.NewPrinter(language.Make(string(locale)))
}
