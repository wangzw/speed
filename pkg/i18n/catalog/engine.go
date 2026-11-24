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

// The sentinel failures of loading the message sources; each is a startup
// failure.
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
	// that is not a message catalog.
	ErrInvalidOverlay = errors.New("i18n: a configured catalogs directory holds a file that is not a well-formed catalog")
	// ErrInvalidMessage reports a message the engine cannot parse: an unknown
	// keyword, an invalid plural category, a missing "other" or repeated
	// category, or a time segment with an empty layout.
	ErrInvalidMessage = errors.New("i18n: a message is not well formed")
)

type engine struct {
	index         map[i18n.Locale]map[i18n.Key]*template
	supported     []i18n.Locale
	matcher       language.Matcher
	localeTags    map[i18n.Locale]language.Tag
	printers      map[i18n.Locale]*message.Printer
	profiles      []i18n.Profile
	defaultLocale i18n.Locale
	mu            sync.RWMutex
	locations     map[string]*time.Location
}

func newEngine(catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, defaultLocale i18n.Locale, catalogsDir string) (*engine, error) {
	index := map[i18n.Locale]map[i18n.Key]*template{}
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

func validate(catalogs []core.Resource[i18n.Catalog], defaultLocale i18n.Locale, catalogsDir string) error {
	_, err := newEngine(catalogs, nil, defaultLocale, catalogsDir)
	return err
}

const overlayPattern = "*.json"

func readOverlay(dir string) (map[i18n.Locale]map[i18n.Key]string, error) {
	if dir == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	fsys := os.DirFS(dir)
	// The loader treats a pattern matching no file as an error, which is wrong
	// for the overlay: an empty directory is a legal "no extras". Glob first so
	// that error never surfaces for this source.
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

func checkDefault(index map[i18n.Locale]map[i18n.Key]*template, defaultLocale i18n.Locale) error {
	if len(index) == 0 {
		return nil
	}
	if _, ok := index[defaultLocale]; !ok {
		return fmt.Errorf("%w: no catalog carries %q", ErrNoCatalog, string(defaultLocale))
	}
	return nil
}

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

func (e *engine) Translate(locale i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	msg, ok := e.index[locale][key]
	if !ok {
		return "", false
	}
	return e.render(context.Background(), msg, args, locale), true
}

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

func (e *engine) Default() i18n.Locale {
	return e.defaultLocale
}

func (e *engine) LocaleOf(ctx context.Context) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	return e.defaultLocale
}

func (e *engine) TimeZoneOf(ctx context.Context) string {
	name, _ := i18n.TimeZoneFromContext(ctx)
	return name
}

// LocaleFor asks every profile even after one reports a failed read: one
// profile's failure says nothing about another's answer.
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

type template struct {
	nodes []node
}

type node struct {
	text  string
	raw   string
	arg   string
	group *pluralGroup
	time  *timeForm
}

type pluralGroup struct {
	arg      string
	branches map[plural.Form]string
}

type timeForm struct {
	arg    string
	layout string
}

var categoryNames = []string{"zero", "one", "two", "few", "many", "other"}

var categoryToForm = map[string]plural.Form{
	"zero":  plural.Zero,
	"one":   plural.One,
	"two":   plural.Two,
	"few":   plural.Few,
	"many":  plural.Many,
	"other": plural.Other,
}

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

func parseSegment(n *node, inner string) error {
	name, rest, _ := strings.Cut(inner, ",")
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a parameterised segment names no parameter")
	}
	keyword, body, _ := strings.Cut(rest, ",")
	switch strings.TrimSpace(keyword) {
	case "plural":
		// The parser cuts a placeholder at its first "}}" before this check, so a
		// branch carrying a nested placeholder arrives truncated.
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
		// The layout is everything after the second comma, so its own commas
		// stay part of it.
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

type branchToken struct {
	name string
	text string
}

// splitBranches splits a branch list into "category=text" tokens. A comma begins
// a new branch only when the text after it is a word immediately followed by
// "="; a comma anywhere else belongs to the branch text, so "other=# items, and
// more" keeps its comma. The boundary test does not check the word against the
// CLDR categories, so a misspelled category is reported by parseBranches rather
// than swallowed into the branch before it.
func splitBranches(s string) []branchToken {
	var tokens []branchToken
	s = strings.TrimLeft(s, " \t,")
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
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

func nextBranchStart(body string) int {
	for i := 0; i < len(body); i++ {
		if body[i] == ',' && beginsBranch(body[i+1:]) {
			return i
		}
	}
	return -1
}

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

func (e *engine) render(ctx context.Context, m *template, args i18n.Args, locale i18n.Locale) string {
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
		if mode == i18n.TimeRenderMarked {
			return i18n.MarkedTime(name)
		}
	}
	return raw
}

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

func (e *engine) renderTime(ctx context.Context, tf *timeForm, raw string, args i18n.Args, mode i18n.TimeRender) string {
	if mode == i18n.TimeRenderMarked {
		return i18n.MarkedTime(tf.arg)
	}
	v, ok := args[tf.arg]
	if !ok || i18n.KindOf(v) != i18n.KindTime {
		return raw
	}
	name, _ := i18n.TimeZoneFromContext(ctx)
	return i18n.TimeOf(v).In(e.location(name)).Format(tf.layout)
}

// location answers "Local", empty and unloadable names as UTC. A loaded location
// is cached: time.LoadLocation re-reads the zoneinfo files on every call.
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
		loc = time.UTC
	}
	e.mu.Lock()
	e.locations[name] = loc
	e.mu.Unlock()
	return loc
}

// pluralForm selects the CLDR category on the count's magnitude: the library
// indexes an inclusion mask by the operand's remainder, which is negative for a
// negative operand in Go and would index out of range.
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

func (e *engine) formatInt(n int64, locale i18n.Locale) string {
	return e.printer(locale).Sprintf("%d", n)
}

// formatFloat uses number.Decimal because the printer's %v verb switches to
// scientific notation at 1e6.
func (e *engine) formatFloat(f float64, locale i18n.Locale) string {
	return e.printer(locale).Sprint(number.Decimal(f))
}

func (e *engine) printer(locale i18n.Locale) *message.Printer {
	if p, ok := e.printers[locale]; ok {
		return p
	}
	return message.NewPrinter(language.Make(string(locale)))
}
