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

const (
	sealWritesCallbackName    = "db:seal-encrypted-writes"
	restoreWritesCallbackName = "db:restore-caller-values"
)

const (
	restorationsSetting = "db:caller-values-to-restore"
	callerDestSetting   = "db:caller-dest-to-restore"
)

type restoration struct {
	values map[string]any
	key    string
	value  any
}

func isEncrypted(field *schema.Field) bool {
	switch field.Serializer.(type) {
	case encryptedSerializer, *encryptedSerializer:
		return true
	default:
		return false
	}
}

func encryptedFields(s *schema.Schema) []*schema.Field {
	var fields []*schema.Field
	for _, field := range s.Fields {
		if field.DBName != "" && isEncrypted(field) {
			fields = append(fields, field)
		}
	}
	return fields
}

// sealCreate seals what an insert binds as it stands: the values of a map the
// rows are inserted from, and the assignments of an ON CONFLICT DO UPDATE. GORM
// reads a model's fields through the serializer, but a map and an assignment
// list belong to no field and would carry the plaintext into the column. An
// insert from the model that writes a sealed column through a field without the
// serializer is refused (see refuseSpelledInsert).
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

// sealUpdate seals the values of a map and the assignments of a caller-written
// SET clause. A struct update is refused when one of its fields would put a
// plaintext into a sealed column; see refuseUnsealedStruct.
func sealUpdate(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || len(encryptedFields(st.Schema)) == 0 {
		return
	}
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
	// The map is sealed in a copy the statement runs on, never in the caller's:
	// GORM only reads an update's map, so one map may be handed to many
	// concurrent updates, and a seal written into it would be sealed again.
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
// builds the SQL has read it. An insert is sealed in the caller's own map
// because GORM writes generated keys and RETURNING values back into it.
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

func keepForRestore(st *gorm.Statement, restorations []restoration) {
	if len(restorations) > 0 {
		st.Settings.Store(restorationsSetting, restorations)
	}
}

func restore(restorations []restoration) {
	for i := len(restorations) - 1; i >= 0; i-- {
		r := restorations[i]
		r.values[r.key] = r.value
	}
}

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

// sealMap replaces, in place, each value GORM will write into an encrypted
// column, and returns what it replaced. The column is judged the way the
// statement's engine resolves the name; a value Select and Omit leave out is
// untouched, and a value for a sealed column bound to an unsealed field is
// refused (see shadowingField).
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
		if bound := shadowingField(st, field); bound != nil {
			return restorations, shadowedSeal(st, field, bound)
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

// sealAssignments returns the assignments with every encrypted one sealed,
// leaving the caller's slice as it was: an assignment list is often reused, and
// sealing into it would carry one statement's seal into the next.
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
		if bound := shadowingField(st, field); bound != nil {
			return nil, shadowedSeal(st, field, bound)
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

// sealValue turns a value written into an encrypted column into its seal: it is
// set on a scratch model and read back through the field, the shape GORM uses on
// the model's own write path. A column reference is kept only when it names
// another sealed column; an expression or subquery is refused, as the database
// evaluates it after anything here could seal it.
func sealValue(st *gorm.Statement, field *schema.Field, value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case clause.Column:
		source, judged := sealedTarget(st, v)
		if !judged || source == nil {
			return nil, unsealable(field, value)
		}
		if bound := shadowingField(st, source); bound != nil {
			return nil, shadowedSeal(st, source, bound)
		}
		return v, nil
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

func unsealable(field *schema.Field, value any) error {
	return fmt.Errorf("db: field %s is encrypted, and this statement writes its column from a %T, "+
		"which the database evaluates after this module could have sealed it, so the column would "+
		"be written in clear: write the value itself, or copy another encrypted column with "+
		"clause.Column", fieldName(field), value)
}

// refuseUnsealedStruct refuses an update written from a struct with a field
// that would put a plaintext into an encrypted column. The struct need not be
// the model: a field without the serializer on another struct writes the
// model's sealed column in clear.
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
		if source == nil || source.DBName == "" || !source.Updatable {
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
		if bound := shadowingField(st, field); bound != nil {
			return shadowedSeal(st, field, bound)
		}
		if isEncrypted(source) {
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

// refuseSpelledInsert refuses an insert from the model's own rows when a field
// it writes puts a value into a sealed column without the serializer. A field
// spelling the sealed column another way writes it twice in one insert, and
// SQLite keeps the first: declaration order decides whether the plaintext is
// stored.
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
		if bound := shadowingField(st, field); bound != nil {
			return shadowedSeal(st, field, bound)
		}
		return spelledTwice(st, field, source)
	}
	return nil
}

// insertWrites reports whether an insert from the model writes a field, mirroring
// GORM: the field bound to the column when Select and Omit leave it in, and a
// database-default field only when a row gives it a value.
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

func givesValue(st *gorm.Statement, field *schema.Field, rows []reflect.Value) bool {
	for _, row := range rows {
		if _, zero := field.ValueOf(st.Context, row); !zero {
			return true
		}
	}
	return false
}

func spelledTwice(st *gorm.Statement, sealed, source *schema.Field) error {
	return fmt.Errorf("db: field %s is encrypted, and model %s also declares field %s on column %q, which "+
		"the database reads as the same column %q, without serializer:%s: a write through the model "+
		"writes that column twice, and the database may keep the value in clear. Give %s a column of its "+
		"own, or leave it out of the write with Omit", fieldName(sealed), st.Schema.Name, fieldName(source),
		source.DBName, sealed.DBName, encryptedSerializerName, fieldName(source))
}

// shadowingField is the field GORM binds a sealed field's column to when that
// field does not carry the serializer. GORM binds a column two fields name to
// one of them and reads and writes it through that field alone, so a sealed
// field that loses its column is never run: the column is written in clear and
// a read hands it the seal as a value.
func shadowingField(st *gorm.Statement, sealed *schema.Field) *schema.Field {
	bound := st.Schema.FieldsByDBName[sealed.DBName]
	if bound == nil || isEncrypted(bound) {
		return nil
	}
	return bound
}

func shadowedSeal(st *gorm.Statement, sealed, bound *schema.Field) error {
	return fmt.Errorf("db: field %s is encrypted, and GORM binds its column %q to field %s of model %s, "+
		"which does not carry serializer:%s, so the model writes and reads that column in clear and the "+
		"serializer is never run: mark %s serializer:%s as well, or give one of the two fields a column "+
		"of its own", fieldName(sealed), sealed.DBName, fieldName(bound), st.Schema.Name,
		encryptedSerializerName, fieldName(bound), encryptedSerializerName)
}
