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

type leafKind uint8

const (
	kindString leafKind = iota
	kindBool
	kindInt
	kindUint
	kindFloat
	kindDuration
	// A leaf type that decodes itself from text, usable only when it also encodes
	// itself back to text.
	kindText
)

type keyEntry struct {
	key     string
	module  string
	typ     reflect.Type
	kind    leafKind
	def     any
	defText string
	item    Item
}

type keyTable struct {
	entries map[string]keyEntry
	listing []DeclaredItem
}

func (t *keyTable) lookup(key string) (keyEntry, bool) {
	e, ok := t.entries[key]
	return e, ok
}

func buildKeyTable(reg *core.Registry) (*keyTable, error) {
	declared := core.Resources[Schema](reg)
	table := &keyTable{entries: make(map[string]keyEntry)}

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

// declaredItem builds the listing entry of one declared item, the single place a
// sensitive item's default is withheld.
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

type expander struct {
	module  string
	schema  Schema
	entries []keyEntry
	keys    map[string]bool
}

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
			continue
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
			continue
		case reflect.Pointer:
			if f.Type.Elem().Kind() == reflect.Struct && !decodesFromText(f.Type) {
				if fv.IsNil() {
					continue
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

// descend expands a nested struct, refusing a type already on the way in; a
// cycle is a defect while depth is not.
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
	if entry.item.Effect != EffectNone && entry.item.Effect != EffectImmediate &&
		entry.item.Effect != EffectNextSignIn {
		return fmt.Sprintf("its effect %q is outside the closed set: EffectNone, "+
			"EffectImmediate or EffectNextSignIn", entry.item.Effect)
	}
	return ""
}

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

func pathLabel(key string) string {
	if key == "" {
		return "the mount root"
	}
	return fmt.Sprintf("%q", key)
}

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

// classifyLeaf decides how a declared type is encoded to text, refusing a pointer
// before anything else is asked about it.
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

func defaultValue(v reflect.Value) any {
	return detached(v).Interface()
}

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
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()

var durationType = reflect.TypeFor[time.Duration]()

func decodesFromText(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func encodesToText(t reflect.Type) bool {
	return t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}
