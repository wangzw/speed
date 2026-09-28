package db

import (
	"fmt"
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

// restorationsSetting is the statement setting the sealed map values wait
// under until the step that builds the SQL has read them.
const restorationsSetting = "db:caller-values-to-restore"

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
// An insert from the model needs nothing here. GORM reads each field of the
// model through the field's serializer, and that is the path the serializer
// was written for; a map and an assignment list carry values that belong to no
// field, so GORM binds them without it, and the plaintext would reach the
// column with no error — leaving a row the model can no longer read.
func sealCreate(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || len(encryptedFields(st.Schema)) == 0 {
		return
	}
	selectColumns, restricted := st.SelectAndOmitColumns(true, false)
	var restorations []restoration
	for _, values := range insertedMaps(st.Dest) {
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
func sealUpdate(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil {
		return
	}
	sealed := encryptedFields(st.Schema)
	if len(sealed) == 0 {
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
	if values, ok := updatedMap(st.Dest); ok {
		selectColumns, restricted := st.SelectAndOmitColumns(false, true)
		restorations, err := sealMap(st, values, selectColumns, restricted)
		if err != nil {
			restore(restorations)
			_ = tx.AddError(err)
			return
		}
		keepForRestore(st, restorations)
		return
	}
	if err := refuseUnsealedStruct(st, sealed); err != nil {
		_ = tx.AddError(err)
	}
}

// restoreCallerValues puts back what sealMap replaced in the caller's maps,
// once the step that builds the SQL has read the sealed values.
//
// The replacement is done in the caller's own map rather than in a copy
// because GORM writes into that map after an insert — the key it generated,
// the columns a RETURNING clause gave back — and a copy would take those away
// from the caller. What the caller finds afterwards is the plaintext it put
// there and whatever GORM added.
func restoreCallerValues(tx *gorm.DB) {
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
// A key reaches a column the way GORM resolves it — by column name or by field
// name — and only when the statement's Select and Omit leave that column in:
// a value GORM is not going to write is neither sealed nor refused.
func sealMap(st *gorm.Statement, values map[string]any, selectColumns map[string]bool, restricted bool) ([]restoration, error) {
	var restorations []restoration
	for key, value := range values {
		field := st.Schema.LookUpField(key)
		if field == nil || field.DBName == "" || !isEncrypted(field) {
			continue
		}
		if chosen, named := selectColumns[field.DBName]; (named && !chosen) || (!named && restricted) {
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
func sealAssignments(st *gorm.Statement, assignments []clause.Assignment) ([]clause.Assignment, error) {
	var out []clause.Assignment
	for i, assignment := range assignments {
		field := st.Schema.LookUpField(assignment.Column.Name)
		if field == nil || field.DBName == "" || !isEncrypted(field) {
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
// reference is kept when the column it names is sealed as well — the value
// arriving is then already a seal, which is how an upsert takes the inserted
// value — and refused otherwise. Anything the database evaluates, an
// expression or a subquery, is refused: it produces its value after anything
// here could have sealed it.
func sealValue(st *gorm.Statement, field *schema.Field, value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case clause.Column:
		if source := st.Schema.LookUpField(v.Name); source != nil && isEncrypted(source) {
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

// refuseUnsealedStruct refuses an update written from a struct that is not
// the model when one of its fields would put a value into an encrypted column
// of the model without the serializer.
//
// Whether the field is written is decided the way GORM decides it: named by
// Select, or holding a value and not left out by Select or Omit. A struct that
// does not reach the sealed column has nothing to refuse.
func refuseUnsealedStruct(st *gorm.Statement, sealed []*schema.Field) error {
	updating := reflect.ValueOf(st.Dest)
	for updating.Kind() == reflect.Pointer {
		if updating.IsNil() {
			return nil
		}
		updating = updating.Elem()
	}
	if updating.Kind() != reflect.Struct || updating.Type() == st.Schema.ModelType {
		return nil
	}
	other := &gorm.Statement{DB: st.DB}
	if err := other.Parse(st.Dest); err != nil || other.Schema == nil {
		return nil
	}
	selectColumns, restricted := st.SelectAndOmitColumns(false, true)
	for _, field := range sealed {
		source := other.Schema.LookUpField(field.DBName)
		if source == nil || source.DBName == "" || isEncrypted(source) || !source.Updatable {
			continue
		}
		chosen, named := selectColumns[field.DBName]
		if (named && !chosen) || (!named && restricted) {
			continue
		}
		if _, zero := source.ValueOf(st.Context, updating); zero && !named {
			continue
		}
		return fmt.Errorf("db: field %s is encrypted, and this update writes its column %q from %s.%s, "+
			"which does not carry serializer:%s, so the value would be stored in clear: mark that "+
			"field serializer:%s as well, or update through the model", fieldName(field), field.DBName,
			other.Schema.Name, source.Name, encryptedSerializerName, encryptedSerializerName)
	}
	return nil
}
