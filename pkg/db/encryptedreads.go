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

const (
	guardReadsCallbackName    = "db:guard-encrypted-reads"
	guardRowReadsCallbackName = "db:guard-encrypted-row-reads"
)

var (
	scannerType = reflect.TypeFor[sql.Scanner]()
	timeType    = reflect.TypeFor[time.Time]()
)

type destination int

const (
	intoModel destination = iota
	intoStruct
	intoMap
	intoText
	intoOther
)

// guardReads refuses a query that would scan an encrypted column into a destination the serializer never reaches, where the ciphertext arrives as the value. What a query reads is taken from its Select, Omit and the column a Pluck names; a selection around the sealed column goes through.
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

// guardRowReads refuses a Row, Rows or Scan over the model that reads an encrypted column, since those finishers run after the last callback and the destination is never on the statement. A Scan or Rows over the model into the model itself is refused too; use Find, First, Take or FindInBatches. A statement naming no model is not judged, and a refused Row hands back a row that reports the refusal through Scan and Err.
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
		// Rows and Scan set "rows" true on the statement, Row false; the
		// statement's error then keeps GORM's row step from running.
		if setting, ok := tx.Get("rows"); ok {
			if many, isBool := setting.(bool); isBool && !many {
				st.Dest = refusedRow(err)
			}
		}
		_ = tx.AddError(err)
		return
	}
}

// refusedRow is a row whose Scan and Err report err, asked of a pool whose connector refuses every connection with err.
func refusedRow(err error) *sql.Row {
	pool := sql.OpenDB(refusingConnector{err: err})
	defer func() { _ = pool.Close() }()
	return pool.QueryRowContext(context.Background(), "")
}

type refusingConnector struct{ err error }

func (c refusingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }

func (c refusingConnector) Driver() driver.Driver { return refusingDriver(c) }

type refusingDriver struct{ err error }

func (d refusingDriver) Open(string) (driver.Conn, error) { return nil, d.err }

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

// refuseRenamedRead refuses a read into a struct that takes an encrypted column, or a value computed from it, under another name: GORM fills that field without the serializer, so the ciphertext arrives as its value. A field that cannot hold text is left alone.
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

func readsAsItself(text string) bool {
	text = strings.TrimSpace(text)
	if text == "*" || strings.HasSuffix(text, ".*") {
		return true
	}
	_, isColumn := columnName(text)
	return isColumn
}

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

func holdsText(field *schema.Field) bool {
	if field.Serializer != nil {
		return true
	}
	into, _ := classify(field.IndirectFieldType)
	return into == intoText
}

func unsealedRead(field *schema.Field, into string) error {
	return fmt.Errorf("db: field %s is encrypted, and this query reads its column %q into %s, which "+
		"GORM fills without the serializer, so what came back would be the ciphertext: read it into "+
		"the model, or into a struct whose field for that column carries serializer:%s",
		fieldName(field), field.DBName, into, encryptedSerializerName)
}

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

// reads reports whether the statement reads a field's column. unknownReads is the answer when the statement carries no readable selection.
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

type selected struct {
	text  string
	alias string
	sql   string
}

// selection collects a statement's selection items and reports whether it carries one that can be read. A text naming several items is cut at its top-level commas; native SQL carries none, as this package does not parse it.
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

// mentions reports whether a selection item reads a field's column. The expression test is lexical, not a parse: it finds `email` in `lower(email)` but not in `email_index`, and errs towards reading, which refuses rather than leaks.
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

func identifierByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
