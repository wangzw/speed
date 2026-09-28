package db

import (
	"database/sql"
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
// Row has no error to report through. When a callback fails, GORM hands back
// no row at all, and scanning that nil row panics with nothing to say why; a
// refused Row panics with the refusal instead, so the failure names the field
// at the call that has to change.
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
		// apart.
		if setting, ok := tx.Get("rows"); ok {
			if many, isBool := setting.(bool); isBool && !many {
				panic(err)
			}
		}
		_ = tx.AddError(err)
		return
	}
}

// refuseUnsealedRead is the guard's judgement over one statement.
func refuseUnsealedRead(st *gorm.Statement, sealed []*schema.Field) error {
	into, typ := destinationOf(st)
	switch into {
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
	switch {
	case typ == st.Schema.ModelType:
		return intoModel, typ
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
	texts, known := selection(st)
	if !known {
		for _, omitted := range st.Omits {
			if omitted == field.DBName || st.Schema.LookUpField(omitted) == field {
				return false
			}
		}
		return unknownReads
	}
	for _, text := range texts {
		if mentions(st.Schema, text, field) {
			return true
		}
	}
	return false
}

// selection collects the texts a statement's selection is written in, and
// reports whether it carries one that can be read. Native SQL carries none: the
// statement is the caller's text, and this package does not parse SQL.
func selection(st *gorm.Statement) ([]string, bool) {
	if st.SQL.Len() > 0 {
		return nil, false
	}
	var texts []string
	if c, ok := st.Clauses["SELECT"]; ok && c.Expression != nil {
		sel, ok := c.Expression.(clause.Select)
		if !ok {
			return nil, false
		}
		for _, column := range sel.Columns {
			texts = append(texts, column.Name)
		}
		if sel.Expression != nil {
			expr, ok := sel.Expression.(clause.Expr)
			if !ok {
				return nil, false
			}
			texts = append(texts, expr.SQL)
		}
	}
	texts = append(texts, st.Selects...)
	return texts, len(texts) > 0
}

// mentions reports whether one selection text reads a field's column: a
// wildcard, the field or column named on its own, or the column name standing
// as a word inside an expression. The last is a lexical test and not a parse —
// it finds `email` in `lower(email)` and in `contacts.email` and does not find
// it in `email_index` — and it errs towards reading, which is the direction
// that refuses rather than leaks.
func mentions(s *schema.Schema, text string, field *schema.Field) bool {
	text = strings.TrimSpace(text)
	if text == "*" || strings.HasSuffix(text, ".*") {
		return true
	}
	if s.LookUpField(text) == field {
		return true
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
