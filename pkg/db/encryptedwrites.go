package db

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// The names the write-side callbacks are registered under, once per chain, on
// every handle this package opens.
const (
	sealWritesCallbackName    = "db:seal-encrypted-writes"
	restoreWritesCallbackName = "db:restore-caller-values"
)

// The statement settings what was replaced waits under until the step that
// builds the SQL has read the replacement: the values sealed into an insert's
// maps, and the map an update was written from.
const (
	restorationsSetting = "db:caller-values-to-restore"
	callerDestSetting   = "db:caller-dest-to-restore"
)

// restoration is one value this package replaced in a caller's map, and what
// goes back once the statement no longer needs the replacement.
type restoration struct {
	values map[string]any
	key    string
	value  any
}

// isEncrypted reports whether a field carries this package's serializer.
func isEncrypted(field *schema.Field) bool {
	switch field.Serializer.(type) {
	case encryptedSerializer, *encryptedSerializer:
		return true
	default:
		return false
	}
}

// encryptedFields lists the fields of a schema that carry this package's
// serializer and map to a column.
func encryptedFields(s *schema.Schema) []*schema.Field {
	var fields []*schema.Field
	for _, field := range s.Fields {
		if field.DBName != "" && isEncrypted(field) {
			fields = append(fields, field)
		}
	}
	return fields
}

// sealCreate seals what an insert writes into an encrypted column when GORM
// would bind it as it stands: the values of a map the rows are inserted from,
// and the assignments of an ON CONFLICT DO UPDATE.
//
// An insert from the model needs no sealing here. GORM reads each field of the
// model through the field's serializer, and that is the path the serializer
// was written for; a map and an assignment list carry values that belong to no
// field, so GORM binds them without it, and the plaintext would reach the
// column with no error — leaving a row the model can no longer read. What an
// insert from the model can still do is write a sealed column a second time,
// through a field of the model that spells it another way, and that insert is
// refused (see refuseSpelledInsert).
func sealCreate(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || len(encryptedFields(st.Schema)) == 0 {
		return
	}
	selectColumns, restricted := st.SelectAndOmitColumns(true, false)
	maps := insertedMaps(st.Dest)
	if maps == nil {
		if err := refuseSpelledInsert(st, selectColumns, restricted); err != nil {
			_ = tx.AddError(err)
			return
		}
	}
	var restorations []restoration
	for _, values := range maps {
		sealed, err := sealMap(st, values, selectColumns, restricted)
		restorations = append(restorations, sealed...)
		if err != nil {
			restore(restorations)
			_ = tx.AddError(err)
			return
		}
	}
	if c, ok := st.Clauses["ON CONFLICT"]; ok {
		if onConflict, ok := c.Expression.(clause.OnConflict); ok && len(onConflict.DoUpdates) > 0 {
			assignments, err := sealAssignments(st, onConflict.DoUpdates)
			if err != nil {
				restore(restorations)
				_ = tx.AddError(err)
				return
			}
			onConflict.DoUpdates = assignments
			c.Expression = onConflict
			st.Clauses["ON CONFLICT"] = c
		}
	}
	keepForRestore(st, restorations)
}

// sealUpdate seals what an update writes into an encrypted column when GORM
// would bind it as it stands: the values of a map, and the assignments of a SET
// clause the caller wrote.
//
// It also refuses an update written from a struct that is not the model. GORM
// reads such a struct through its own schema, so a field without the
// serializer there puts its plaintext into the model's sealed column; that
// struct is the caller's to mark, and nothing here can know it was meant to be.
// An update written from the model's own struct is refused as well when a field
// of it spells a sealed column another way (see refuseUnsealedStruct).
func sealUpdate(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || len(encryptedFields(st.Schema)) == 0 {
		return
	}
	// A SET clause the caller wrote is used as it is, and GORM then reads
	// nothing else.
	if c, ok := st.Clauses["SET"]; ok {
		if set, ok := c.Expression.(clause.Set); ok {
			assignments, err := sealAssignments(st, set)
			if err != nil {
				_ = tx.AddError(err)
				return
			}
			c.Expression = clause.Set(assignments)
			st.Clauses["SET"] = c
		}
		return
	}
	// An update's map is sealed in a copy the statement runs on, and the
	// caller's map is never written. GORM only reads the map an update is
	// written from, so a caller may hand one map to many updates at once; a
	// seal written into it would be read by a concurrent update as the value
	// to write and sealed a second time.
	if values, ok := updatedMap(st.Dest); ok {
		selectColumns, restricted := st.SelectAndOmitColumns(false, true)
		copied := maps.Clone(values)
		replaced, err := sealMap(st, copied, selectColumns, restricted)
		if err != nil {
			_ = tx.AddError(err)
			return
		}
		if len(replaced) > 0 {
			st.Settings.Store(callerDestSetting, st.Dest)
			st.Dest = copied
		}
		return
	}
	if err := refuseUnsealedStruct(st); err != nil {
		_ = tx.AddError(err)
	}
}

// restoreCallerValues puts back what the sealing replaced, once the step that
// builds the SQL has read the sealed values: the caller's own map as the
// update's destination, and the values sealMap replaced in an insert's maps.
//
// An insert's replacement is done in the caller's own map rather than in a
// copy because GORM writes into that map after an insert — the key it
// generated, the columns a RETURNING clause gave back — and a copy would take
// those away from the caller. GORM's own write makes a map shared between
// concurrent inserts unsafe with or without this package. What the caller
// finds afterwards is the plaintext it put there and whatever GORM added.
func restoreCallerValues(tx *gorm.DB) {
	if dest, ok := tx.Statement.Settings.LoadAndDelete(callerDestSetting); ok {
		tx.Statement.Dest = dest
	}
	pending, ok := tx.Statement.Settings.LoadAndDelete(restorationsSetting)
	if !ok {
		return
	}
	if restorations, ok := pending.([]restoration); ok {
		restore(restorations)
	}
}

// keepForRestore hands the replaced values to restoreCallerValues.
func keepForRestore(st *gorm.Statement, restorations []restoration) {
	if len(restorations) > 0 {
		st.Settings.Store(restorationsSetting, restorations)
	}
}

// restore puts replaced values back, the last replacement first, so that a map
// that appeared twice ends with the value the caller gave it.
func restore(restorations []restoration) {
	for i := len(restorations) - 1; i >= 0; i-- {
		r := restorations[i]
		r.values[r.key] = r.value
	}
}

// insertedMaps lists the maps an insert takes its rows from, in each shape
// GORM accepts one in.
func insertedMaps(dest any) []map[string]any {
	switch values := dest.(type) {
	case map[string]any:
		return []map[string]any{values}
	case *map[string]any:
		if values != nil && *values != nil {
			return []map[string]any{*values}
		}
	case []map[string]any:
		return values
	case *[]map[string]any:
		if values != nil {
			return *values
		}
	}
	return nil
}

// updatedMap is the map an update takes its assignments from, in either shape
// GORM accepts one in.
func updatedMap(dest any) (map[string]any, bool) {
	switch values := dest.(type) {
	case map[string]any:
		return values, true
	case *map[string]any:
		if values != nil && *values != nil {
			return *values, true
		}
	}
	return nil, false
}

// sealMap replaces, in place, each value of a map that GORM will write into an
// encrypted column, and returns what it replaced so it can be put back.
//
// A key names the column GORM writes for it: the resolved field's column when
// GORM resolves the key — by column name, by field name or through its naming
// strategy — and the key itself when it does not. Which column that is, is
// then judged the way the statement's engine resolves the name (see
// sealedTarget), so a key the engine reads as the sealed column's name —
// quoted, or on SQLite in another letter case — is sealed like the name
// itself, and one the engine reads as another column is not. A value GORM is not going to write,
// because the statement's Select and Omit leave its column out, is neither
// sealed nor refused.
func sealMap(st *gorm.Statement, values map[string]any, selectColumns map[string]bool, restricted bool) ([]restoration, error) {
	var restorations []restoration
	for key, value := range values {
		written := key
		if field := st.Schema.LookUpField(key); field != nil {
			if field.DBName == "" {
				continue
			}
			written = field.DBName
		}
		if chosen, named := selectColumns[written]; (named && !chosen) || (!named && restricted) {
			continue
		}
		field, judged := sealedTarget(st, clause.Column{Name: written})
		if !judged {
			return restorations, unjudgedColumn(st, key)
		}
		if field == nil {
			continue
		}
		sealed, err := sealValue(st, field, value)
		if err != nil {
			return restorations, err
		}
		restorations = append(restorations, restoration{values: values, key: key, value: value})
		values[key] = sealed
	}
	return restorations, nil
}

// sealAssignments returns the assignments with every one that writes an
// encrypted column sealed. The caller's slice is left as it was: an assignment
// list is often a value built once and reused, and sealing into it would carry
// one statement's seal into the next.
//
// GORM writes an assignment's column as the caller gave it, so the column is
// judged the way the statement's engine resolves the name, and one this package
// cannot judge — raw text that is not an identifier — is refused rather than
// let through: it may be the encrypted column, and the value would then be
// stored in clear.
func sealAssignments(st *gorm.Statement, assignments []clause.Assignment) ([]clause.Assignment, error) {
	var out []clause.Assignment
	for i, assignment := range assignments {
		field, judged := sealedTarget(st, assignment.Column)
		if !judged {
			return nil, unjudgedColumn(st, assignment.Column.Name)
		}
		if field == nil {
			continue
		}
		sealed, err := sealValue(st, field, assignment.Value)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = slices.Clone(assignments)
		}
		out[i].Value = sealed
	}
	if out == nil {
		return assignments, nil
	}
	return out, nil
}

// sealValue turns a value written into an encrypted column into what binds as
// its seal, or refuses it.
//
// The value is set on a scratch instance of the model and read back through
// the field, which is the shape GORM builds for a field's value on the model's
// own write path: bound as a statement variable it runs the serializer under
// the statement's context, and handed back to the model it sets the plaintext.
// That second half is what a Model(&row).Update leans on, since GORM writes
// the assignment back into the row the caller passed.
//
// Nil stays NULL, which is the absence the serializer maps it to. A column
// reference is kept when the column it names — judged the way the statement's
// engine resolves the name — is sealed as well: the value arriving is then
// already a seal, which is how an upsert takes the inserted value. Any other
// column reference is refused.
// Anything the database evaluates, an expression or a subquery, is refused: it
// produces its value after anything here could have sealed it.
func sealValue(st *gorm.Statement, field *schema.Field, value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case clause.Column:
		if source, judged := sealedTarget(st, v); judged && source != nil {
			return v, nil
		}
		return nil, unsealable(field, value)
	case *gorm.DB, clause.Expression:
		return nil, unsealable(field, value)
	}
	scratch := reflect.New(st.Schema.ModelType).Elem()
	if err := field.Set(st.Context, scratch, value); err != nil {
		return nil, fmt.Errorf("db: field %s is encrypted, and the %T written into it is not a value "+
			"the field holds: %w", fieldName(field), value, err)
	}
	sealed, _ := field.ValueOf(st.Context, scratch)
	return sealed, nil
}

// unsealable is the refusal of a value this package cannot seal.
func unsealable(field *schema.Field, value any) error {
	return fmt.Errorf("db: field %s is encrypted, and this statement writes its column from a %T, "+
		"which the database evaluates after this module could have sealed it, so the column would "+
		"be written in clear: write the value itself, or copy another encrypted column with "+
		"clause.Column", fieldName(field), value)
}

// refuseUnsealedStruct refuses an update written from a struct when one of its
// fields would put a value into an encrypted column of the model without the
// serializer.
//
// What the struct writes is found the way GORM finds it: for each column of
// the model, the struct's field GORM resolves that column's name to, written
// under that field's own column name — named by Select, or holding a value and
// not left out by Select or Omit. The column written is then judged the way the
// statement's engine resolves its name, so a field whose column that engine
// reads as the sealed one — on SQLite, one that differs from it only in letter
// case — reaches it as surely as one that matches it. A struct that does not
// reach a sealed column has nothing to refuse.
//
// The struct may be the model itself. Its sealed fields are written through
// the serializer, and what can reach a sealed column in clear is another of its
// fields that spells the column another way, which the update then writes a
// second time.
func refuseUnsealedStruct(st *gorm.Statement) error {
	updating := reflect.ValueOf(st.Dest)
	for updating.Kind() == reflect.Pointer {
		if updating.IsNil() {
			return nil
		}
		updating = updating.Elem()
	}
	if updating.Kind() != reflect.Struct {
		return nil
	}
	written := st.Schema
	if updating.Type() != st.Schema.ModelType {
		other := &gorm.Statement{DB: st.DB}
		if err := other.Parse(st.Dest); err != nil || other.Schema == nil {
			return nil
		}
		written = other.Schema
	}
	selectColumns, restricted := st.SelectAndOmitColumns(false, true)
	for _, column := range st.Schema.DBNames {
		source := written.LookUpField(column)
		if source == nil || source.DBName == "" || isEncrypted(source) || !source.Updatable {
			continue
		}
		chosen, named := selectColumns[source.DBName]
		if (named && !chosen) || (!named && restricted) {
			continue
		}
		if _, zero := source.ValueOf(st.Context, updating); zero && !named {
			continue
		}
		field, judged := sealedTarget(st, clause.Column{Name: source.DBName})
		if !judged {
			return unjudgedColumn(st, source.DBName)
		}
		if field == nil {
			continue
		}
		if written == st.Schema {
			return spelledTwice(st, field, source)
		}
		return fmt.Errorf("db: field %s is encrypted, and this update writes its column %q from %s, "+
			"which does not carry serializer:%s, so the value would be stored in clear: mark that "+
			"field serializer:%s as well, or update through the model", fieldName(field), source.DBName,
			fieldName(source), encryptedSerializerName, encryptedSerializerName)
	}
	return nil
}

// refuseSpelledInsert refuses an insert from the model's own rows when one of
// the fields it writes spells a sealed column another way: a field without the
// serializer whose column the database reads as the sealed one.
//
// GORM writes each field it writes as a column of its own, so such a field
// writes the sealed column a second time in one insert, once through the
// serializer and once in clear, and SQLite keeps the first of the two: the
// declaration order decides whether the plaintext is stored. Which fields an
// insert writes is found the way GORM finds them (see insertWrites): a
// field GORM does not write is no value for any column, whatever column it
// names.
func refuseSpelledInsert(st *gorm.Statement, selectColumns map[string]bool, restricted bool) error {
	rows := modelRows(st)
	for _, source := range st.Schema.Fields {
		if source.DBName == "" || isEncrypted(source) || !insertWrites(st, source, selectColumns, restricted, rows) {
			continue
		}
		field, judged := sealedTarget(st, clause.Column{Name: source.DBName})
		if !judged {
			return unjudgedColumn(st, source.DBName)
		}
		if field == nil {
			continue
		}
		return spelledTwice(st, field, source)
	}
	return nil
}

// insertWrites reports whether an insert from the model's rows writes a field,
// found the way GORM finds it. GORM writes each column of the model once,
// through the field it binds the column to, when Select chooses it or Select
// and Omit leave it in — a timestamp GORM fills is written even when they
// leave it out — unless the field's default is the database's. It then writes
// every field whose default is the database's, bound to its column or not,
// that Select chooses or Select and Omit leave in, when a row gives it a
// value. Any other field is not written: a field declaring a column another
// field is bound to, an embedded struct's among them, never reaches it.
func insertWrites(st *gorm.Statement, field *schema.Field, selectColumns map[string]bool, restricted bool,
	rows []reflect.Value,
) bool {
	chosen, named := selectColumns[field.DBName]
	databaseDefault := field.HasDefaultValue && field.DefaultValueInterface == nil
	if !databaseDefault && st.Schema.FieldsByDBName[field.DBName] == field {
		stamped := field.AutoCreateTime > 0 || field.AutoUpdateTime > 0
		if (named && chosen) || (!named && (!restricted || stamped)) {
			return true
		}
	}
	if slices.Contains(st.Schema.FieldsWithDefaultDBValue, field) && ((named && chosen) || (!named && !restricted)) {
		return givesValue(st, field, rows)
	}
	return false
}

// modelRows lists the rows an insert from the model takes its values from, in
// each shape GORM accepts them in: one struct, or a slice or an array of them,
// by value or by pointer.
func modelRows(st *gorm.Statement) []reflect.Value {
	v := reflect.Indirect(st.ReflectValue)
	switch v.Kind() {
	case reflect.Struct:
		return []reflect.Value{v}
	case reflect.Slice, reflect.Array:
		rows := make([]reflect.Value, 0, v.Len())
		for i := range v.Len() {
			if row := reflect.Indirect(v.Index(i)); row.Kind() == reflect.Struct {
				rows = append(rows, row)
			}
		}
		return rows
	default:
		return nil
	}
}

// givesValue reports whether any row gives a field a value, which is when
// GORM writes a field whose default is the database's.
func givesValue(st *gorm.Statement, field *schema.Field, rows []reflect.Value) bool {
	for _, row := range rows {
		if _, zero := field.ValueOf(st.Context, row); !zero {
			return true
		}
	}
	return false
}

// spelledTwice is the refusal of a write through the model that writes a
// sealed column a second time, through a field of the model without the
// serializer whose column the database reads as the sealed one.
func spelledTwice(st *gorm.Statement, sealed, source *schema.Field) error {
	return fmt.Errorf("db: field %s is encrypted, and model %s also declares field %s on column %q, which "+
		"the database reads as the same column %q, without serializer:%s: a write through the model "+
		"writes that column twice, and the database may keep the value in clear. Give %s a column of its "+
		"own, or leave it out of the write with Omit", fieldName(sealed), st.Schema.Name, fieldName(source),
		source.DBName, sealed.DBName, encryptedSerializerName, fieldName(source))
}
