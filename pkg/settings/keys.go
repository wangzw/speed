package settings

import (
	"encoding"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/core"
)

// The key table is the map from every declared key to its declaration: the
// type, the default and the metadata. It is built once in New from the
// declarations the registered modules made, and read-only afterwards.
//
// Building it is what turns a declaration into something the two read and
// write paths can be held to. A key that is not here is refused by both of
// them before anything else happens, which is what makes a misspelled key a
// certain failure rather than a value nobody ever reads.

// leafKind is how a declared type is encoded to and decoded from the text
// column. The set is closed: a declaration whose type falls outside it is
// defective in itself.
type leafKind uint8

const (
	kindString leafKind = iota
	kindBool
	kindInt
	kindUint
	kindFloat
	// kindDuration is time.Duration, which is an int64 underneath but takes
	// time.ParseDuration text. It is separated from kindInt because the text
	// form differs: a bare number would mean nanoseconds to the machine and
	// minutes to the reader.
	kindDuration
	// kindText is a type that decodes itself from text, such as time.Time,
	// net.IP or netip.Addr. It is a leaf rather than a level of the path:
	// expanding such a type would walk into unexported fields and make the
	// item disappear. A declaration of one is usable only when the type also
	// encodes itself back to text, which is what a write stores; one that
	// decodes and cannot encode is refused as a declaration defect.
	//
	// A type with a binary form only is not one of them. url.URL implements
	// UnmarshalBinary and no UnmarshalText, so it is expanded field by field
	// like any struct — the way configuration expands it — into keys such as
	// endpoint.scheme and endpoint.host, with no key for the URL as a whole. A
	// URL item is declared as the string it is written as.
	kindText
)

// keyEntry is one declared setting item.
type keyEntry struct {
	// key is the full path: namespace, mount path and field path.
	key string
	// module is the name of the module that declared it, which a conflict and
	// an invalid declaration both have to name.
	module string
	// typ is the declared type, which the target of a read is held to and the
	// text of a write is encoded under.
	typ reflect.Type
	// kind is typ classified for the encoder.
	kind leafKind
	// def is the value a read falls back to when neither the tenant row nor
	// the global row holds one.
	def any
	// defText is def in the declared type's own text form: the text a row
	// holding the default would carry. A read of a type that carries its own
	// text form decodes it rather than handing out def, for the reason Get
	// gives.
	defText string
	// item is the metadata the declaring module gave.
	item Item
}

// keyTable maps every declared key to its declaration. Both read and write
// paths consult it before they touch anything else, which is what makes a key
// with no declaration a certain failure.
type keyTable struct {
	entries map[string]keyEntry
	// listing is every entry as Declared reports it, in the keys' byte
	// order. It is built with the table, since neither changes afterwards.
	listing []DeclaredItem
}

// lookup returns the declaration of a key.
func (t *keyTable) lookup(key string) (keyEntry, bool) {
	e, ok := t.entries[key]
	return e, ok
}

// buildKeyTable collects every declaration and turns it into the key table.
//
// The source is every registered module's Schema, which is the available set:
// a key's validity follows the binary rather than one command's participating
// set, so the module a run never took in, the one it reached for its
// declarations alone and the one that lost a resolution all keep their keys.
// Nothing is subtracted here (architecture invariant 15): this consumer reads
// the available set, and a subtraction would make one key legal under one
// command and illegal under another while the declaration itself did not
// change. Whether a module runs is a fact about the run; the declaration is a
// fact about the binary.
func buildKeyTable(reg *core.Registry) (*keyTable, error) {
	declared := core.Resources[Schema](reg)
	table := &keyTable{entries: make(map[string]keyEntry)}

	// Sorted by module name so that which conflict is reported first does not
	// depend on registration order. The sort is stable because a module may
	// declare more than one schema and their relative order has to survive it.
	slices.SortStableFunc(declared, func(a, b core.Resource[Schema]) int {
		return strings.Compare(a.Module, b.Module)
	})

	for _, resource := range declared {
		entries, err := expandSchema(resource.Module, resource.Value)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			earlier, taken := table.entries[entry.key]
			if taken && earlier.module == entry.module {
				// Two schemas of one module: the same defect as two mounts of
				// one schema reaching one key, and no second party to name.
				return nil, fmt.Errorf("%w: module %q reaches key %q from two of its schemas. "+
					"Two declarations of one key name one thing, and which default it has "+
					"would depend on the order they were collected in",
					ErrInvalidDeclaration, entry.module, entry.key)
			}
			if taken {
				return nil, fmt.Errorf("%w: key %q is declared by module %q and by module %q. "+
					"One of them changes its key or its namespace",
					ErrDeclarationConflict, entry.key, earlier.module, entry.module)
			}
			table.entries[entry.key] = entry
		}
	}
	for _, key := range slices.Sorted(maps.Keys(table.entries)) {
		table.listing = append(table.listing, declaredItem(table.entries[key]))
	}
	return table, nil
}

// declaredItem builds the listing entry of one declared item.
//
// It is the single place such an entry is built, because withholding a
// sensitive item's default is a property of the listing rather than of a
// caller: the listing exists to reach a client — an administrative endpoint's
// response, a command's output — and a credential compiled in as a default
// does not earn that reach by being declared. A second place building an entry
// is a second place that can forget to withhold it.
func declaredItem(entry keyEntry) DeclaredItem {
	item := DeclaredItem{
		Key:     entry.key,
		Module:  entry.module,
		Type:    entry.typ,
		Default: entry.defText,
		Item:    entry.item,
	}
	if entry.item.Sensitive {
		item.Default = ""
	}
	return item
}

// expandSchema turns one module's declaration into its setting items: it walks
// the mounted structs down to the leaves and derives the paths, and refuses
// what a single declaration can be wrong about on its own.
func expandSchema(module string, s Schema) ([]keyEntry, error) {
	if defect := pathDefect(s.Namespace); defect != "" {
		return nil, fmt.Errorf("%w: module %q declares namespace %q: %s",
			ErrInvalidDeclaration, module, s.Namespace, defect)
	}
	e := &expander{module: module, schema: s, keys: make(map[string]bool)}
	for i, mount := range s.Mounts {
		if err := e.mount(i, mount); err != nil {
			return nil, err
		}
	}
	if err := e.checkItemKeys(); err != nil {
		return nil, err
	}
	return e.entries, nil
}

// expander carries the state of one module's expansion.
type expander struct {
	module  string
	schema  Schema
	entries []keyEntry
	keys    map[string]bool // the relative keys the expansion produced
}

// mount starts the walk of one carrier struct.
func (e *expander) mount(index int, m Mount) error {
	if defect := pathDefect(m.Path); defect != "" {
		return fmt.Errorf("%w: module %q mounts at %q: %s",
			ErrInvalidDeclaration, e.module, m.Path, defect)
	}
	v := reflect.ValueOf(m.Value)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return fmt.Errorf("%w: module %q gives %T as mount %d, and a mount carries a struct or a "+
			"pointer to one: its fields are where the keys and their defaults come from",
			ErrInvalidDeclaration, e.module, m.Value, index)
	}
	return e.walk(v, m.Path, []reflect.Type{v.Type()})
}

// walk expands one struct. rel is the path reached so far, relative to the
// namespace; chain is the struct types on the way in, which is what turns a
// self-referencing declaration into an error instead of a hang.
//
// The path rules are configuration's, to the letter: an embedded field is
// inlined, a config tag wins over a json tag, and a name derived from a field
// becomes lower kebab case. A parameter moving from configuration to settings
// therefore keeps its key.
func (e *expander) walk(v reflect.Value, rel string, chain []reflect.Type) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			// Unexported: no declaration can reach it.
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
		if defect := segmentDefect(name); defect != "" {
			return fmt.Errorf("%w: module %q derives key %q from field %s of %s: %s",
				ErrInvalidDeclaration, e.module, name, f.Name, typeName(t), defect)
		}
		key := joinPath(rel, name)
		switch f.Type.Kind() {
		case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
			continue // no declaration can give these a text form
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
// check is on types rather than on a depth limit: a cycle is a defect for
// certain, while depth is not, and a limit picked out of the air would one day
// stop a legitimate declaration. The same type on two sibling branches is not
// a cycle.
func (e *expander) descend(v reflect.Value, key string, chain []reflect.Type, f reflect.StructField) error {
	st := v.Type()
	if slices.Contains(chain, st) {
		return fmt.Errorf("%w: module %q reaches %s again through field %s at %s, so expanding "+
			"the declaration does not terminate. Drop the field, or mirror the part of it that "+
			"is meant to be a setting",
			ErrInvalidDeclaration, e.module, typeName(st), f.Name, pathLabel(key))
	}
	return e.walk(v, key, append(chain, st))
}

// leaf records one setting item, checking that its type is one the closed set
// admits.
func (e *expander) leaf(key string, fv reflect.Value, ft reflect.Type) error {
	kind, ok := classifyLeaf(ft)
	if !ok {
		return fmt.Errorf("%w: module %q declares %q as %s, which is outside the closed type "+
			"set. A setting item is a string, a bool, an integer, a float, a time.Duration or a "+
			"type that carries its own text form; a mapping or a list is carried as JSON text in "+
			"a string item instead",
			ErrInvalidDeclaration, e.module, key, typeName(ft))
	}
	if kind == kindText && !encodesToText(ft) {
		return fmt.Errorf("%w: module %q declares %q as %s, which decodes itself from text and "+
			"has no MarshalText to write itself back, so no write of the key could store it. "+
			"Give the type a MarshalText, or declare the item as the string it is written as",
			ErrInvalidDeclaration, e.module, key, typeName(ft))
	}
	if e.keys[key] {
		// Two mounts of one declaration reaching one key: no second party is
		// involved, and this declaration alone is enough to judge it unusable.
		return fmt.Errorf("%w: module %q reaches key %q twice, from two mounts or through two "+
			"paths in one. Two declarations of one key name one thing, and which default it "+
			"has would depend on the order they were walked in",
			ErrInvalidDeclaration, e.module, key)
	}
	e.keys[key] = true
	entry := keyEntry{
		key:    joinPath(e.schema.Namespace, key),
		module: e.module,
		typ:    ft,
		kind:   kind,
		def:    defaultValue(fv),
		item:   e.schema.Items[key],
	}
	text, err := defaultText(entry)
	if err != nil {
		return fmt.Errorf("%w: module %q declares %q: %w", ErrInvalidDeclaration, e.module, key, err)
	}
	entry.defText = text
	if defect := domainDefect(entry); defect != "" {
		return fmt.Errorf("%w: module %q declares %q: %s", ErrInvalidDeclaration, e.module, key, defect)
	}
	if defect := tenantDefect(entry, e.schema.TenantFacing); defect != "" {
		return fmt.Errorf("%w: module %q declares %q: %s", ErrInvalidDeclaration, e.module, key, defect)
	}
	e.entries = append(e.entries, entry)
	return nil
}

// tenantDefect reports why an item's tenant-facing declaration is not one a
// tenant surface may render, or "" when it is.
//
// The tenant surface is fail-closed on both switches: the zero TenantAccess and
// the zero Schema.TenantFacing each keep an item on the platform surface, and an
// item that states a tenant access under a schema that does not declare itself
// tenant-facing is a defect rather than an item exposed on the strength of one
// field. The metadata is judged with the access because a surface renders the
// item from it: a title, a description with its English fallback, and a control
// are required. A switch control additionally requires the item to be declared
// as a plain bool — classified as kindBool, not merely bool underneath — since a
// switch renders a boolean state and the tenant listing re-reads a switch's
// stored text with strconv.ParseBool. A named bool that carries its own text
// form is kindText and can write text ParseBool refuses, so admitting it would
// fail the encoding of a whole tenant listing on every read. Danger and Effect
// are optional, since an item may carry neither.
//
// The judgement runs when the key table is built, which is part of a module's
// New, so a defective declaration fails the startup and no request surface
// exists to observe it. The message names only the declaring module's own key.
func tenantDefect(entry keyEntry, tenantFacing bool) string {
	switch entry.item.TenantAccess {
	case TenantNone:
		return ""
	case TenantRead, TenantReadWrite:
	default:
		return fmt.Sprintf("its tenant access %q is outside the closed set: TenantNone, "+
			"TenantRead or TenantReadWrite", entry.item.TenantAccess)
	}
	if !tenantFacing {
		return fmt.Sprintf("its tenant access is %q, and the schema does not declare "+
			"TenantFacing. An item reaches a tenant surface only under a schema that says "+
			"it is tenant-facing", entry.item.TenantAccess)
	}
	switch {
	case entry.item.TitleKey == "":
		return "it is tenant-facing and declares no TitleKey, which a tenant surface renders " +
			"as the item's title"
	case entry.item.DescriptionKey == "":
		return "it is tenant-facing and declares no DescriptionKey, which a tenant surface " +
			"renders as the item's description"
	case entry.item.Description == "":
		return "it is tenant-facing and declares no Description, the text a surface falls back " +
			"to when it has no localizer"
	}
	if entry.item.Control == ControlNone {
		return "it is tenant-facing and declares no Control, so a tenant surface would not " +
			"know what to render"
	}
	if entry.item.Control != ControlSwitch {
		return fmt.Sprintf("its control %q is outside the closed set: ControlSwitch",
			entry.item.Control)
	}
	if entry.kind != kindBool {
		return fmt.Sprintf("its control %q is a switch, and the item is declared as %s, which is "+
			"not encoded as a plain boolean: a switch renders a boolean state and the tenant "+
			"listing reads its text with strconv.ParseBool, so the declared type must be bool",
			entry.item.Control, typeName(entry.typ))
	}
	if entry.item.Danger != DangerNone && entry.item.Danger != DangerCritical {
		return fmt.Sprintf("its danger %q is outside the closed set: DangerNone or DangerCritical",
			entry.item.Danger)
	}
	if entry.item.Effect != EffectNone && entry.item.Effect != EffectImmediate {
		return fmt.Sprintf("its effect %q is outside the closed set: EffectNone or EffectImmediate",
			entry.item.Effect)
	}
	return ""
}

// domainDefect reports why an item's declared domain is not one a write can
// be held to, or "" when it is. The zero Domain, which admits every value of
// the declared type, is one.
//
// A domain is judged against the item it is declared for: it has to take the
// declared type itself, since a write hands the predicate a value of exactly
// that type; it has to state a rule, since a refusal names it; and it has to
// admit the declared default. A default outside the domain is a value no
// write could store back once a row has replaced it, and DeclaredItem.Default
// is promised to be text Set accepts. The default is judged as a read of it
// hands it out, decoded from its text. A sensitive item's default is withheld
// from the message.
func domainDefect(entry keyEntry) string {
	judge := entry.item.Domain.judge
	switch {
	case judge == nil:
		return ""
	case judge.typ != entry.typ:
		return fmt.Sprintf("its domain is built over %s, and the item is declared as %s. A write "+
			"hands the domain a value of the declared type, so it is built with DomainOf[%s]",
			typeName(judge.typ), typeName(entry.typ), typeName(entry.typ))
	case judge.rule == "":
		return "its domain states no rule. A write outside the domain is refused naming the rule, " +
			"so DomainOf is given the set it admits in words"
	case judge.admits == nil:
		return "its domain has no predicate, so no value written could be judged against it"
	}
	value, err := decodeValue(entry, entry.defText)
	if err != nil {
		// Unreachable: defaultText has already read a text-form default back,
		// and the scalar kinds read back what their formatters write.
		return "its default does not read back as its declared type"
	}
	if judge.admits(value) {
		return ""
	}
	if entry.item.Sensitive {
		return fmt.Sprintf("its default is outside its own domain, %s, so no write could store it "+
			"back. The default is withheld: the item is marked sensitive", judge.rule)
	}
	return fmt.Sprintf("its default, %q, is outside its own domain, %s, so no write could store "+
		"it back", entry.defText, judge.rule)
}

// defaultText writes a declared default in the declared type's own text form,
// the text a row holding that value would carry, and checks that the text
// reads back.
//
// A default either of those fails for is the declaration's own defect: the
// type set is closed so that every declared value round-trips through text,
// and a default that does not is one no write could restore once a row has
// replaced it. The scalar kinds cannot fail here, since strconv and
// time.ParseDuration read back what their formatters write; a type that
// carries its own text form can, through its own MarshalText or
// UnmarshalText. A sensitive item's text and cause are withheld, since a
// type's error may quote the value.
func defaultText(entry keyEntry) (string, error) {
	text, err := textOf(entry, reflect.ValueOf(entry.def))
	if err != nil {
		if entry.item.Sensitive {
			return "", fmt.Errorf("its default does not encode to text as %s. The cause is "+
				"withheld: the item is marked sensitive", typeName(entry.typ))
		}
		return "", fmt.Errorf("its default does not encode to text as %s: %w", typeName(entry.typ), err)
	}
	if entry.kind != kindText {
		return text, nil
	}
	decoder, ok := reflect.New(entry.typ).Interface().(encoding.TextUnmarshaler)
	if !ok {
		// Unreachable: a type is kindText because it decodes itself from text.
		return "", fmt.Errorf("%s does not decode itself from text", typeName(entry.typ))
	}
	if err := decoder.UnmarshalText([]byte(text)); err != nil {
		if entry.item.Sensitive {
			return "", fmt.Errorf("its default encodes to a text %s does not read back. The "+
				"text and the cause are withheld: the item is marked sensitive", typeName(entry.typ))
		}
		return "", fmt.Errorf("its default encodes to %q, which %s does not read back: %w",
			text, typeName(entry.typ), err)
	}
	return text, nil
}

// checkItemKeys refuses an Items key that reached no leaf. Every item lives in
// a carrier struct and therefore has a path, so a key that matches none is a
// misspelling — and leaving it alone would drop a Description or a Sensitive
// without a word.
func (e *expander) checkItemKeys() error {
	for _, key := range slices.Sorted(maps.Keys(e.schema.Items)) {
		if !e.keys[key] {
			return fmt.Errorf("%w: module %q gives Items key %q, which matches no field of its "+
				"mounts. The key is relative to the namespace and includes the mount path",
				ErrInvalidDeclaration, e.module, key)
		}
	}
	return nil
}

// fieldName derives the path segment of a field: the config tag first, the
// json tag next, the field name in lower kebab case otherwise. A tag of "-"
// keeps the field out, which is what it means on the structs that carry one.
//
// tagged reports that the name came from a tag rather than from the field
// name, which is what tells an embedded field that is inlined from one that
// names a segment of its own.
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
// embedded struct without an explicit tag is inlined: its fields join the
// enclosing path without a segment of their own. An explicit config or json
// tag names a segment as it does on any other field.
//
// inner is the struct to walk when inline is set; hollow marks an embedded nil
// struct pointer, which is skipped for the same reason a named one is: there
// are no defaults to read out of it. An embedded non-struct such as a defined
// integer type is neither, and keeps contributing a segment named after its
// type.
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

// camelToKebab turns a Go field name into a key segment: PoolSize becomes
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

// pathDefect reports why a dotted path is not a legal one, or "" when it is.
// An empty path is legal: it means the namespace root.
func pathDefect(path string) string {
	if path == "" {
		return ""
	}
	for segment := range strings.SplitSeq(path, ".") {
		if defect := segmentDefect(segment); defect != "" {
			return defect
		}
	}
	return ""
}

// segmentDefect reports why one path segment is not a legal key segment, or ""
// when it is. Segments hold lowercase letters, digits and hyphens, the same
// set configuration admits: the two modules derive one key space, and a key
// that is legal in one of them has to be legal in the other.
func segmentDefect(segment string) string {
	if segment == "" {
		return "it has an empty segment, and every segment names something"
	}
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			continue
		}
		return fmt.Sprintf("segment %q contains %q, and a key segment holds lowercase "+
			"letters, digits and hyphens only", segment, string(r))
	}
	return ""
}

// classifyLeaf decides how a declared type is encoded to text, and reports
// false for a type outside the closed set.
//
// A pointer is refused before anything else is asked about it. decodesFromText
// is true of *T whenever T takes its value from text, since the method set of
// *T includes T's pointer-receiver methods — so asking it first would admit
// *time.Time and *net.IP as leaves. They cannot round-trip: the declared type
// is the pointer, and reflect.New of a pointer type gives a pointer to a
// pointer, which decodes nothing. A pointer buys nothing here anyway, which is
// the reason the set excludes it: whether a key was ever written is answered
// by the layers, so the third state a pointer would express has no reader.
func classifyLeaf(t reflect.Type) (leafKind, bool) {
	if t.Kind() == reflect.Pointer {
		return 0, false
	}
	if decodesFromText(t) {
		return kindText, true
	}
	if t == durationType {
		return kindDuration, true
	}
	switch t.Kind() {
	case reflect.String:
		return kindString, true
	case reflect.Bool:
		return kindBool, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return kindInt, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return kindUint, true
	case reflect.Float32, reflect.Float64:
		return kindFloat, true
	}
	return 0, false
}

// defaultValue reads a field's current value out of the prototype. That value
// is the declaration's default: it is what a read falls back to when no row
// holds the key, so "not set" and "set to the zero value" stay apart.
//
// A value whose kind carries a reference is detached rather than kept as it
// is. The closed type set admits no container, but it does admit a type that
// carries its own text form and is a slice underneath — net.IP is one — and a
// default read straight out of the prototype would share that slice with it.
// Two registries may be decoded against one prototype, and the key table
// keeps nothing the prototype's owner can still write into. What a read hands
// out does not come from here for such a type: Get decodes it afresh from the
// default's text.
func defaultValue(v reflect.Value) any {
	return detached(v).Interface()
}

// detached returns a value whose kind carries a reference as a copy of its
// own, so that the key table and the prototype it was read from share no
// storage. It copies the top level only, which is all reflection can see of a
// type that carries its own text form.
func detached(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		copied := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			copied.SetMapIndex(iter.Key(), iter.Value())
		}
		return copied
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		copied := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(copied, v)
		return copied
	}
	return v
}

// joinPath appends a segment path to a prefix, either of which may be empty.
func joinPath(prefix, rest string) string {
	switch {
	case prefix == "":
		return rest
	case rest == "":
		return prefix
	}
	return prefix + "." + rest
}

// typeName renders a type for an error message, keeping the package path so
// the reader can find the declaration.
func typeName(t reflect.Type) string {
	if t.PkgPath() != "" && t.Name() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}

// textUnmarshalerType is the interface a type uses to decode itself from text.
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// textMarshalerType is the interface a type uses to encode itself to text.
var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()

// durationType is the type Duration text is held to.
var durationType = reflect.TypeFor[time.Duration]()

// decodesFromText reports whether a type takes its value from a text of its
// own, either directly or through a pointer method set. Such a type is a leaf
// rather than a level of the path.
func decodesFromText(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// encodesToText reports whether a type writes itself to text, on either
// receiver: math/big.Int declares MarshalText on its pointer. It is the other
// half of what a type that carries its own text form needs here, since a row
// is written by it and read back by decodesFromText's half.
func encodesToText(t reflect.Type) bool {
	return t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}
