package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// The names the read guards are registered under on every handle this package
// opens: one on the query chain, one on the row chain.
const (
	guardReadsCallbackName    = "db:guard-encrypted-reads"
	guardRowReadsCallbackName = "db:guard-encrypted-row-reads"
)

// The destination types the guard tells apart without parsing them.
var (
	scannerType = reflect.TypeFor[sql.Scanner]()
	timeType    = reflect.TypeFor[time.Time]()
)

// destination is what a query is about to scan its columns into, as far as
// the serializer is concerned.
type destination int

const (
	// intoModel is the model itself: GORM scans each column into its field,
	// through the field's serializer.
	intoModel destination = iota
	// intoStruct is a struct that is not the model: GORM scans a column into
	// the field of that struct's own schema, through that field's serializer
	// if it has one.
	intoStruct
	// intoMap is a map, or a slice of them: GORM puts each column's stored
	// value under the column's name.
	intoMap
	// intoText is a value that can hold what the column stores — a string, a
	// byte slice, an interface, a scanner: GORM scans the one column straight
	// into it.
	intoText
	// intoOther is a value that cannot hold text, which a sealed column does
	// not scan into: a number, a boolean, a time. A count lands here.
	intoOther
)

// guardReads refuses a query that would scan an encrypted column into a
// destination the serializer never reaches.
//
// GORM applies a field's serializer only when it scans a column into a field
// of a parsed struct that carries the serializer. A sealed column scanned into
// a map, a plain string or a struct whose field is not marked arrives as the
// text the database holds — the ciphertext — and the query reports success.
// That is the shape of the failure this refuses: nothing downstream can tell a
// ciphertext handed back as the value from the value.
//
// What a query reads is taken from what the caller wrote on the statement: its
// Select, its Omit, the column a Pluck names. A query that selects around the
// sealed column goes through. When the selection cannot be read — none was
// written, or it is native SQL — a map or another struct is taken to receive
// every column, which is what GORM selects for them, and a single value is
// taken to receive whichever column the caller wrote.
func guardReads(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || st.Dest == nil {
		return
	}
	sealed := encryptedFields(st.Schema)
	if len(sealed) == 0 {
		return
	}
	if err := refuseUnsealedRead(st, sealed); err != nil {
		_ = tx.AddError(err)
	}
}

// guardRowReads refuses a Row, Rows or Scan over the model that reads an
// encrypted column.
//
// Those finishers hand the result set on after the last callback has run: to
// database/sql's own Scan, or to GORM's ScanRows, neither of which runs a
// callback. What the columns land in is therefore never on the statement, and a
// read into the model — which the serializer opens — cannot be told from a read
// into a string, a map or an untagged struct, each of which gets the
// ciphertext and reports success. The one judgement left is over what the
// statement reads, so a statement over the model that reads a sealed column is
// refused whatever it was going to be scanned into. The cost is that a Scan or
// a Rows over the model into the model itself is refused along with the rest;
// Find, First, Take and FindInBatches read the same rows with the destination
// on the statement, where the query guard can check it.
//
// A statement that names no model — native SQL, or a table name alone — has no
// schema to judge by, and it goes through: this package does not parse SQL.
//
// Row has no error of its own to report through: what it hands back is the
// row GORM's row step leaves on the statement, and when a callback fails that
// step leaves none, so the caller's Scan would dereference nil with nothing to
// say why. A refused Row hands back a row that reports the refusal through its
// Scan and its Err instead (see refusedRow), so the failure names the field at
// the call that has to change. That row reaches no database and takes no
// connection of the pool, so the refusal is answered at once even where the
// pool has no connection to spare.
func guardRowReads(tx *gorm.DB) {
	st := tx.Statement
	if tx.Error != nil || st.Schema == nil || st.SQL.Len() > 0 {
		return
	}
	for _, field := range encryptedFields(st.Schema) {
		if !reads(st, field, true) {
			continue
		}
		err := fmt.Errorf("db: field %s is encrypted, and this statement reads its column %q through "+
			"Row, Rows or Scan, which hand the result set to a destination this module never sees, "+
			"so it could come back as the ciphertext: read it with Find, First, Take or FindInBatches "+
			"into the model, or into a struct whose field for that column carries serializer:%s, or "+
			"select around the column", fieldName(field), field.DBName, encryptedSerializerName)
		// Rows and Scan set "rows" to true on the statement, Row sets it to
		// false; that setting is how GORM's own row callback tells them
		// apart. The statement's error keeps that step from running, and
		// Row hands back whatever row the statement then holds.
		if setting, ok := tx.Get("rows"); ok {
			if many, isBool := setting.(bool); isBool && !many {
				st.Dest = refusedRow(err)
			}
		}
		_ = tx.AddError(err)
		return
	}
}

// refusedRow is a row whose Scan and Err report err, and which reaches no
// database.
//
// Only database/sql makes a *sql.Row that carries an error, and it makes one
// when opening the result set fails. So the row is asked of a pool of this
// package's own whose connector refuses every connection with err: the query
// fails before it reaches a connection, and the row carries err as it was
// given. The pool holds nothing once the row is made, and it is closed then.
func refusedRow(err error) *sql.Row {
	pool := sql.OpenDB(refusingConnector{err: err})
	defer func() { _ = pool.Close() }()
	return pool.QueryRowContext(context.Background(), "")
}

// refusingConnector answers every connection it is asked for with one error.
type refusingConnector struct{ err error }

// Connect refuses the connection with the connector's error.
func (c refusingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }

// Driver is the connector's driver, which opens nothing either.
func (c refusingConnector) Driver() driver.Driver { return refusingDriver(c) }

// refusingDriver is the driver behind refusingConnector.
type refusingDriver struct{ err error }

// Open refuses the connection with the driver's error.
func (d refusingDriver) Open(string) (driver.Conn, error) { return nil, d.err }

// refuseUnsealedRead is the guard's judgement over one statement.
func refuseUnsealedRead(st *gorm.Statement, sealed []*schema.Field) error {
	into, typ := destinationOf(st)
	switch into {
	case intoModel:
		for _, field := range sealed {
			if bound := shadowingField(st, field); bound != nil && reads(st, field, true) {
				return shadowedSeal(st, field, bound)
			}
		}
		return refuseRenamedRead(st, st.Schema, sealed)
	case intoStruct:
		view := &gorm.Statement{DB: st.DB}
		if err := view.Parse(st.Dest); err != nil || view.Schema == nil {
			return nil
		}
		for _, field := range sealed {
			target := view.Schema.LookUpField(field.DBName)
			if target == nil || !target.Readable || isEncrypted(target) || !reads(st, field, true) {
				continue
			}
			return unsealedRead(field, fmt.Sprintf("%s.%s", view.Schema.Name, target.Name))
		}
		return refuseRenamedRead(st, view.Schema, sealed)
	case intoMap:
		for _, field := range sealed {
			if reads(st, field, true) {
				return unsealedRead(field, "a map")
			}
		}
	case intoText:
		for _, field := range sealed {
			if reads(st, field, false) {
				return unsealedRead(field, "a "+typ.String())
			}
		}
	}
	return nil
}

// refuseRenamedRead refuses a read into a struct — the model or another — that
// takes an encrypted column, or a value computed from it, under a name other
// than the column's own.
//
// GORM scans each result column into the field its name resolves to, and a
// selection item that reads a sealed column under an alias, or inside an
// expression, gives its result column another name: `email AS email_index`
// lands in the model's EmailIndex, which has no serializer, and the ciphertext
// is handed back as that field's value. So for every item of a readable
// selection that mentions a sealed column other than as the column itself, the
// names its result column can carry are worked out — the alias the caller set
// apart or ended the item with, and the function's name, which is what
// PostgreSQL calls an unaliased call — and a field of the destination under one
// of those names refuses the read when it can take text and does not carry
// this package's serializer. A field that cannot take text — a count, a
// length, a test for NULL — is left to receive what it can.
//
// A selection that cannot be read — none written, or native SQL — has nothing
// renamed that this package can see; the name lookups the caller of this
// function makes cover it.
func refuseRenamedRead(st *gorm.Statement, dest *schema.Schema, sealed []*schema.Field) error {
	items, known := selection(st)
	if !known {
		return nil
	}
	for _, item := range items {
		if item.alias == "" && readsAsItself(item.text) {
			continue
		}
		for _, field := range sealed {
			if !mentions(st, item, field) {
				continue
			}
			for _, name := range resultNames(item) {
				for _, candidate := range []string{name, strings.ToLower(name)} {
					receiver := dest.LookUpField(candidate)
					if receiver == nil || !receiver.Readable || isEncrypted(receiver) || !holdsText(receiver) {
						continue
					}
					return fmt.Errorf("db: field %s is encrypted, and this query reads its column %q "+
						"under the name %q into %s.%s, which GORM fills without the serializer, so what "+
						"came back would be the ciphertext or a value computed from it: select the column "+
						"under its own name into a field that carries serializer:%s, or select around it",
						fieldName(field), field.DBName, name, dest.Name, receiver.Name, encryptedSerializerName)
				}
			}
		}
	}
	return nil
}

// readsAsItself reports whether a selection item reads columns under their own
// names: a wildcard, or a column reference with no alias.
func readsAsItself(text string) bool {
	text = strings.TrimSpace(text)
	if text == "*" || strings.HasSuffix(text, ".*") {
		return true
	}
	_, isColumn := columnName(text)
	return isColumn
}

// resultNames lists the names a selection item's result column can carry.
func resultNames(item selected) []string {
	if item.alias != "" {
		return []string{item.alias}
	}
	var names []string
	if alias, ok := trailingAlias(item.text); ok {
		names = append(names, alias)
	}
	text := strings.TrimSpace(item.text)
	if open := strings.IndexByte(text, '('); open > 0 {
		if function, ok := columnName(text[:open]); ok {
			names = append(names, function)
		}
	}
	return names
}

// holdsText reports whether a field can take a column's stored text: a string,
// a byte slice, an interface, a scanner, or a field whose serializer — one of
// another package — is handed the stored value as it is.
func holdsText(field *schema.Field) bool {
	if field.Serializer != nil {
		return true
	}
	into, _ := classify(field.IndirectFieldType)
	return into == intoText
}

// unsealedRead is the refusal of a read that would hand back the ciphertext.
func unsealedRead(field *schema.Field, into string) error {
	return fmt.Errorf("db: field %s is encrypted, and this query reads its column %q into %s, which "+
		"GORM fills without the serializer, so what came back would be the ciphertext: read it into "+
		"the model, or into a struct whose field for that column carries serializer:%s",
		fieldName(field), field.DBName, into, encryptedSerializerName)
}

// destinationOf classifies what a query scans into, and returns the element
// type it found.
func destinationOf(st *gorm.Statement) (destination, reflect.Type) {
	typ := reflect.TypeOf(st.Dest)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array) && typ.Elem().Kind() != reflect.Uint8 {
		typ = typ.Elem()
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
	}
	if typ == st.Schema.ModelType {
		return intoModel, typ
	}
	return classify(typ)
}

// classify sorts a type that is not the model by what a column scanned into
// it becomes.
func classify(typ reflect.Type) (destination, reflect.Type) {
	switch {
	case typ.Kind() == reflect.Map:
		return intoMap, typ
	case reflect.PointerTo(typ).Implements(scannerType):
		return intoText, typ
	case typ == timeType:
		return intoOther, typ
	case typ.Kind() == reflect.Struct:
		return intoStruct, typ
	case typ.Kind() == reflect.String, typ.Kind() == reflect.Interface,
		typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8:
		return intoText, typ
	default:
		return intoOther, typ
	}
}

// reads reports whether the statement reads a field's column.
//
// unknownReads is the answer when the statement carries no selection that can
// be read: true for a destination GORM fills with every column, false for one
// that takes the single column the caller named.
func reads(st *gorm.Statement, field *schema.Field, unknownReads bool) bool {
	items, known := selection(st)
	if !known {
		for _, omitted := range st.Omits {
			if omitted == field.DBName {
				return false
			}
			if named := st.Schema.LookUpField(omitted); named != nil && named.DBName == field.DBName {
				return false
			}
		}
		return unknownReads
	}
	for _, item := range items {
		if mentions(st, item, field) {
			return true
		}
	}
	return false
}

// selected is one item of a statement's selection: the text it is written in,
// the name the caller set apart for its result column, if any, and the item
// as GORM writes it into the statement.
type selected struct {
	text  string
	alias string
	sql   string
}

// selection collects the items a statement's selection is written in, and
// reports whether it carries one that can be read. A text naming several
// items is cut at its top-level commas. Native SQL carries none: the statement
// is the caller's text, and this package does not parse SQL.
//
// Each item carries the text GORM puts into the statement for it: a column of
// a SELECT clause quoted unless it is raw, a Select entry that names a field
// or a column as that column quoted, and anything else as it was written.
func selection(st *gorm.Statement) ([]selected, bool) {
	if st.SQL.Len() > 0 {
		return nil, false
	}
	var items []selected
	if c, ok := st.Clauses["SELECT"]; ok && c.Expression != nil {
		sel, ok := c.Expression.(clause.Select)
		if !ok {
			return nil, false
		}
		for _, column := range sel.Columns {
			items = append(items, selected{text: column.Name, alias: column.Alias, sql: writtenText(st, column)})
		}
		if sel.Expression != nil {
			expr, ok := sel.Expression.(clause.Expr)
			if !ok {
				return nil, false
			}
			for _, text := range splitSelection(expr.SQL) {
				items = append(items, selected{text: text, sql: text})
			}
		}
	}
	for _, written := range st.Selects {
		if field := st.Schema.LookUpField(written); field != nil && field.DBName != "" {
			items = append(items, selected{text: written, sql: writtenText(st, clause.Column{Name: field.DBName})})
			continue
		}
		for _, text := range splitSelection(written) {
			items = append(items, selected{text: text, sql: text})
		}
	}
	return items, len(items) > 0
}

// mentions reports whether one selection item reads a field's column: a
// wildcard, a column reference the statement's engine resolves to that column
// (see namesColumn), or the column name standing as a word inside an
// expression. The last is a lexical test and not a parse — it finds `email` in
// `lower(email)` and in `email AS e`, and does not find it in `email_index` —
// and it errs towards reading, which is the direction that refuses rather than
// leaks.
func mentions(st *gorm.Statement, item selected, field *schema.Field) bool {
	text := strings.TrimSpace(item.sql)
	if text == "*" || strings.HasSuffix(text, ".*") {
		return true
	}
	if name, quoted, ok := lastIdentifier(text); ok {
		return namesColumn(st, name, quoted, field.DBName)
	}
	return containsWord(strings.ToLower(text), strings.ToLower(field.DBName))
}

// containsWord reports whether word occurs in text with no identifier
// character on either side of it.
func containsWord(text, word string) bool {
	for offset := 0; offset <= len(text)-len(word); {
		index := strings.Index(text[offset:], word)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(word)
		if (start == 0 || !identifierByte(text[start-1])) && (end == len(text) || !identifierByte(text[end])) {
			return true
		}
		offset = start + 1
	}
	return false
}

// identifierByte reports whether a byte can be part of an unquoted SQL
// identifier.
func identifierByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
