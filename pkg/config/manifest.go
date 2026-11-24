package config

import (
	"encoding"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// leafKind is how a leaf takes a value.
type leafKind uint8

const (
	// kindScalar is a single assignable value.
	kindScalar leafKind = iota
	// kindStringList is a list of strings given comma separated and replaced as
	// a whole.
	kindStringList
	// kindContainer is a map or a list of structs, given as a whole.
	kindContainer
)

// manifestItem is one input item: a leaf of a mounted struct together with the
// metadata its module declared for it.
type manifestItem struct {
	module  string
	path    string
	key     string
	item    Item
	origins Origin
	envName string
	typ     reflect.Type
	kind    leafKind
	def     any
	// documented is whether the host defaults document gave this item its
	// default.
	documented bool
}

// expandSchema turns one module's declaration into its input items. prefix is
// the host's environment variable prefix; with no prefix, derived names do not
// apply while pinned ones still do.
func expandSchema(module string, s Schema, prefix string) ([]*manifestItem, error) {
	if d := pathDefect(s.Namespace); d != "" {
		return nil, fmt.Errorf("%w: module %q declares namespace %q: %s",
			ErrInvalidSchema, module, s.Namespace, d)
	}
	e := &expander{module: module, schema: s, prefix: prefix, keys: make(map[string]bool)}
	for i, m := range s.Mounts {
		if err := e.mount(i, m); err != nil {
			return nil, err
		}
	}
	if err := e.checkItemKeys(); err != nil {
		return nil, err
	}
	return e.items, nil
}

// expander carries the state of one module's expansion.
type expander struct {
	module string
	schema Schema
	prefix string
	items  []*manifestItem
	keys   map[string]bool // relative keys the expansion produced
}

func (e *expander) mount(index int, m Mount) error {
	if d := pathDefect(m.Path); d != "" {
		return fmt.Errorf("%w: module %q mounts at %q: %s", ErrInvalidSchema, e.module, m.Path, d)
	}
	v := reflect.ValueOf(m.Value)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return fmt.Errorf("%w: module %q gives %T as mount %d, and a mount carries a struct "+
			"or a pointer to one: the fields are where the accepted keys and their defaults come from",
			ErrInvalidSchema, e.module, m.Value, index)
	}
	return e.walk(v, m.Path, []reflect.Type{v.Type()})
}

// walk expands one struct. rel is the path reached so far; chain holds the
// struct types on the way in, which turns a self-referencing declaration into an
// error instead of a hang. An embedded field is inlined at rel, the embedding
// semantics of encoding/json.
func (e *expander) walk(v reflect.Value, rel string, chain []reflect.Type) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, tagged, skip := fieldName(f)
		if skip {
			continue
		}
		fv := v.Field(i)
		inner, inline, hollow := inlineEmbedded(f, fv, tagged)
		if hollow {
			continue // an embedded nil struct pointer has no defaults to read
		}
		if inline {
			if err := e.descend(inner, rel, chain, f); err != nil {
				return err
			}
			continue
		}
		if d := segmentDefect(name); d != "" {
			return fmt.Errorf("%w: module %q derives key %q from field %s of %s: %s",
				ErrInvalidSchema, e.module, name, f.Name, typeName(t), d)
		}
		key := joinPath(rel, name)
		switch f.Type.Kind() {
		case reflect.Func, reflect.Chan, reflect.Interface:
			continue // no source can give these
		case reflect.Pointer:
			if f.Type.Elem().Kind() == reflect.Struct && !decodesFromText(f.Type) {
				if fv.IsNil() {
					continue // no defaults to read out of it
				}
				if err := e.descend(fv.Elem(), key, chain, f); err != nil {
					return err
				}
				continue
			}
		case reflect.Struct:
			if !decodesFromText(f.Type) {
				if err := e.descend(fv, key, chain, f); err != nil {
					return err
				}
				continue
			}
		}
		if err := e.leaf(key, fv, f.Type); err != nil {
			return err
		}
	}
	return nil
}

// descend expands a nested struct, refusing a type already on the way in. The
// same type on two sibling branches is not a cycle.
func (e *expander) descend(v reflect.Value, key string, chain []reflect.Type, f reflect.StructField) error {
	st := v.Type()
	if slices.Contains(chain, st) {
		return fmt.Errorf("%w: module %q reaches %s again through field %s at %s, so expanding "+
			"the declaration does not terminate. A configuration tree has no back edges: "+
			"drop the field, or mirror the part of it that is meant to be configurable",
			ErrInvalidSchema, e.module, typeName(st), f.Name, pathLabel(key))
	}
	return e.walk(v, key, append(chain, st))
}

// leaf records one input item and checks the metadata the module gave it.
func (e *expander) leaf(key string, fv reflect.Value, ft reflect.Type) error {
	e.keys[key] = true
	item := e.schema.Items[key]
	kind := classify(ft)
	origins := resolveOrigins(item.Origins, kind)
	// The declared set, not the resolved one, decides this. A container has no
	// command-line form; the environment does take one as JSON.
	if kind == kindContainer && item.Origins&OriginFlag != 0 {
		return fmt.Errorf("%w: module %q gives %q the command line as an origin, and it is %s. "+
			"A map or a list of structs has no form for the command line to give, so the "+
			"declaration could never produce a value; such an item takes the primary config "+
			"source, the environment as JSON, or the host defaults document",
			ErrInvalidSchema, e.module, key, typeName(ft))
	}
	if utf8.RuneCountInString(item.FlagShort) > 1 {
		return fmt.Errorf("%w: module %q gives %q the command-line short name %q, and a short "+
			"name is a single character. The command-line syntax reads -%s as that many names "+
			"written together, so no declaration would ever match it as one",
			ErrInvalidSchema, e.module, key, item.FlagShort, item.FlagShort)
	}
	if d := shortNameDefect(item.FlagShort); d != "" {
		return fmt.Errorf("%w: module %q gives %q the command-line short name %q: %s",
			ErrInvalidSchema, e.module, key, item.FlagShort, d)
	}
	if d := longNameDefect(item.FlagName); d != "" {
		return fmt.Errorf("%w: module %q gives %q the command-line name %q: %s",
			ErrInvalidSchema, e.module, key, item.FlagName, d)
	}
	if magicValueTypes[item.Placeholder] {
		return fmt.Errorf("%w: module %q gives %q the placeholder %q, which is one of the type "+
			"names the layering tool reads a command-line value by. It would convert the text "+
			"itself under that name and swallow whatever failed to convert. Choose another "+
			"placeholder",
			ErrInvalidSchema, e.module, key, item.Placeholder)
	}
	if d := envNameDefect(item.EnvName); d != "" {
		return fmt.Errorf("%w: module %q pins the environment variable %q for %q: %s",
			ErrInvalidSchema, e.module, item.EnvName, key, d)
	}
	if origins.has(OriginFlag) && item.FlagName == "" {
		return fmt.Errorf("%w: module %q gives %q the command line as an origin but no FlagName. "+
			"Long names are never derived from the path, because the command line is a human "+
			"interface and the name should be chosen deliberately",
			ErrInvalidSchema, e.module, key)
	}
	if item.Sensitive && item.Description == "" {
		return fmt.Errorf("%w: module %q marks %q sensitive but gives no Description. The help "+
			"output does not echo a sensitive default, so without a description the reader has "+
			"no way to know what to supply",
			ErrInvalidSchema, e.module, key)
	}
	mi := &manifestItem{
		module:  e.module,
		path:    joinPath(e.schema.Namespace, key),
		key:     key,
		item:    item,
		origins: origins,
		typ:     ft,
		kind:    kind,
		def:     defaultValue(fv),
	}
	if origins.has(OriginEnv) {
		switch {
		case item.EnvName != "":
			mi.envName = item.EnvName
		case e.prefix != "":
			mi.envName = deriveEnvName(e.prefix, mi.path)
		}
	}
	e.items = append(e.items, mi)
	return nil
}

// checkItemKeys refuses an Items key that reached no leaf.
func (e *expander) checkItemKeys() error {
	for _, key := range slices.Sorted(maps.Keys(e.schema.Items)) {
		if !e.keys[key] {
			return fmt.Errorf("%w: module %q declares Items key %q, which matches no field of "+
				"its mounts. Check the spelling and the mount path: the key is relative to the "+
				"namespace and includes the mount path",
				ErrInvalidSchema, e.module, key)
		}
	}
	return nil
}

// shortNameDefect reports why a short name is one no command line can reach, or
// "" when it is not.
func shortNameDefect(short string) string {
	switch {
	case short == "":
		return ""
	case len(short) > 1:
		return "the command-line syntax takes a short name of one ASCII character, and this " +
			"one is written in several bytes"
	case short == "-":
		return "a dash after the leading one begins a long name, so -- is never read as this " +
			"short name"
	}
	return ""
}

// longNameDefect reports why a long name is one no command line can reach, or
// "" when it is not. The name is written without the leading dashes.
func longNameDefect(name string) string {
	switch {
	case strings.HasPrefix(name, "-"):
		return "the name is written without the leading dashes, which the command line adds, " +
			"and one that begins with a dash is never read as a name"
	case strings.Contains(name, "="):
		return "the command line cuts an argument at its first equals sign to find the value, " +
			"so a name holding one is never the name looked up"
	}
	return ""
}

// fieldName derives the path segment of a field: the config tag first, the json
// tag next, the field name in lower kebab case otherwise. A tag of "-" keeps the
// field out. tagged reports that the name came from a tag.
func fieldName(f reflect.StructField) (name string, tagged, skip bool) {
	for _, key := range [...]string{"config", "json"} {
		tag, ok := f.Tag.Lookup(key)
		if !ok {
			continue
		}
		value, _, _ := strings.Cut(tag, ",")
		if value == "-" {
			return "", false, true
		}
		if value != "" {
			return value, true, false
		}
	}
	return camelToKebab(f.Name), false, false
}

// inlineEmbedded reports how an anonymous field takes part in a walk. An
// embedded struct without an explicit tag is inlined; an explicit tag names a
// segment. hollow marks an embedded nil struct pointer, which is skipped.
func inlineEmbedded(f reflect.StructField, fv reflect.Value, tagged bool) (inner reflect.Value, inline, hollow bool) {
	if !f.Anonymous || tagged || decodesFromText(f.Type) {
		return reflect.Value{}, false, false
	}
	switch {
	case f.Type.Kind() == reflect.Struct:
		return fv, true, false
	case f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct:
		if fv.IsNil() {
			return reflect.Value{}, false, true
		}
		return fv.Elem(), true, false
	}
	return reflect.Value{}, false, false
}

// pathLabel renders a path for an error message, naming the root when the path
// is empty, which is where the fields of an inlined embedded struct sit.
func pathLabel(key string) string {
	if key == "" {
		return "the mount root"
	}
	return fmt.Sprintf("%q", key)
}

// resolveOrigins settles the origins of one leaf. A container leaf defaults to
// the primary source alone.
func resolveOrigins(declared Origin, kind leafKind) Origin {
	if declared == 0 && kind == kindContainer {
		return OriginPrimary
	}
	return declared.resolved()
}

// camelToKebab turns a Go field name into a config key: PoolSize becomes
// pool-size, HTTPPort becomes http-port.
func camelToKebab(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if !unicode.IsUpper(r) {
			b.WriteRune(r)
			continue
		}
		startsWord := i > 0 && !unicode.IsUpper(runes[i-1])
		endsAcronym := i > 0 && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if startsWord || endsAcronym {
			b.WriteRune('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// deriveEnvName maps a path to an environment variable name: the prefix, an
// underscore, then the segments joined by double underscores with hyphens
// turned into single underscores, upper cased throughout.
//
//	cache.redis.max-conns  ->  MYAPP_CACHE__REDIS__MAX_CONNS
//
// This is the only place a variable name is built.
func deriveEnvName(prefix, path string) string {
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(segment, "-", "_")
	}
	return strings.ToUpper(prefix + "_" + strings.Join(segments, "__"))
}

// pathDefect reports why a dotted path is not a legal one, or "" when it is.
// An empty path is legal: it means the namespace root.
func pathDefect(path string) string {
	if path == "" {
		return ""
	}
	for _, segment := range strings.Split(path, ".") {
		if d := segmentDefect(segment); d != "" {
			return d
		}
	}
	return ""
}

// segmentDefect reports why one path segment is not a legal config key segment,
// or "" when it is. Segments hold lowercase letters, digits and hyphens, which
// keeps the environment variable mapping reversible.
func segmentDefect(segment string) string {
	if segment == "" {
		return "it has an empty segment, and every segment names something"
	}
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			continue
		}
		return fmt.Sprintf("segment %q contains %q, and a config key segment holds "+
			"lowercase letters, digits and hyphens only", segment, string(r))
	}
	return ""
}

// envNameDefect reports why an environment variable name, or a prefix, is not
// one a POSIX shell can export, or "" when it is.
func envNameDefect(name string) string {
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
			continue
		case r >= '0' && r <= '9':
			if i > 0 {
				continue
			}
			return "it begins with a digit, and a shell reads no such word as a variable name"
		}
		return fmt.Sprintf("it contains %q, and an environment variable name holds letters, "+
			"digits and underscores only, which is what a shell can export", string(r))
	}
	return ""
}

func classify(t reflect.Type) leafKind {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.String {
			return kindStringList
		}
		return kindContainer
	case reflect.Map:
		return kindContainer
	}
	return kindScalar
}

// booleanLeaf reports whether a leaf takes its value the way a boolean does.
func booleanLeaf(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Bool
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// decodesFromText reports whether a struct type takes its value from a string of
// its own, and is therefore a leaf rather than a level of the path.
func decodesFromText(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// defaultValue reads a field's current value out of the prototype. Maps and
// slices are copied, so nothing reached through the manifest writes back into a
// shared prototype.
func defaultValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			break
		}
		copied := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			copied.SetMapIndex(iter.Key(), iter.Value())
		}
		return copied.Interface()
	case reflect.Slice:
		if v.IsNil() {
			break
		}
		copied := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(copied, v)
		return copied.Interface()
	}
	return v.Interface()
}

func joinPath(prefix, rest string) string {
	switch {
	case prefix == "":
		return rest
	case rest == "":
		return prefix
	}
	return prefix + "." + rest
}

func typeName(t reflect.Type) string {
	if t.PkgPath() != "" && t.Name() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	return t.String()
}
