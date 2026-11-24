package db

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const sqlWhitespace = " \t\r\n\f\v"

// postgresIdentifierBytes is how much of an identifier PostgreSQL keeps: a
// longer one is cut to this many bytes, at a character boundary, wherever it
// appears.
const postgresIdentifierBytes = 63

// sealedTarget reports which encrypted field of the model a column written into
// a statement names. Text that is not an identifier cannot be judged here, so
// false is the answer for it.
func sealedTarget(st *gorm.Statement, column clause.Column) (*schema.Field, bool) {
	if field := st.Schema.LookUpField(column.Name); field != nil && field.DBName != "" && isEncrypted(field) {
		return field, true
	}
	name, quoted, ok := lastIdentifier(writtenText(st, column))
	if !ok {
		return nil, false
	}
	for _, field := range encryptedFields(st.Schema) {
		if namesColumn(st, name, quoted, field.DBName) {
			if bound := st.Schema.FieldsByDBName[field.DBName]; bound != nil && isEncrypted(bound) {
				return bound, true
			}
			return field, true
		}
	}
	return nil, true
}

func writtenText(st *gorm.Statement, column clause.Column) string {
	var written strings.Builder
	st.QuoteTo(&written, clause.Column{Table: column.Table, Name: column.Name, Raw: column.Raw})
	return written.String()
}

// namesColumn reports whether an identifier, as the statement carries it, names
// column on the statement's engine. An engine without a rule of its own is
// judged by both rules at once.
func namesColumn(st *gorm.Statement, name string, quoted bool, column string) bool {
	switch engineDialect(st) {
	case SQLite:
		return sqliteNames(name, column)
	case Postgres:
		return postgresNames(name, quoted, column)
	default:
		return sqliteNames(name, column) || postgresNames(name, quoted, column)
	}
}

func engineDialect(st *gorm.Statement) Dialect {
	if engine, ok := engineOf(st.DB); ok {
		return engine.dialect
	}
	return ""
}

func sqliteNames(name, column string) bool {
	return asciiLower(name) == asciiLower(column)
}

func postgresNames(name string, quoted bool, column string) bool {
	if !quoted {
		name = asciiLower(name)
	}
	return postgresKept(name) == postgresKept(column)
}

func postgresKept(name string) string {
	if len(name) <= postgresIdentifierBytes {
		return name
	}
	cut := postgresIdentifierBytes
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}

func asciiLower(name string) string {
	folded := []byte(name)
	for i, b := range folded {
		if b >= 'A' && b <= 'Z' {
			folded[i] = b + ('a' - 'A')
		}
	}
	return string(folded)
}

func unjudgedColumn(st *gorm.Statement, column string) error {
	return fmt.Errorf("db: model %s has encrypted fields, and this statement writes a column named %q, "+
		"which is not an identifier this module can resolve the way the database does, so it cannot "+
		"tell whether the value lands in an encrypted column: name the column by its column or field "+
		"name", st.Schema.Name, column)
}

func columnName(text string) (string, bool) {
	name, _, ok := lastIdentifier(text)
	return name, ok
}

// lastIdentifier also reports whether the identifier was quoted. The quoting it
// reads is the union of what the two engines accept, so a form one engine
// rejects is read the way the other would resolve it.
func lastIdentifier(text string) (name string, quoted, ok bool) {
	rest := strings.Trim(text, sqlWhitespace)
	for {
		part, wasQuoted, remainder, ok := identifierPart(rest)
		if !ok {
			return "", false, false
		}
		remainder = strings.TrimLeft(remainder, sqlWhitespace)
		if remainder == "" {
			return part, wasQuoted, true
		}
		if remainder[0] != '.' {
			return "", false, false
		}
		rest = strings.TrimLeft(remainder[1:], sqlWhitespace)
	}
}

func identifierPart(text string) (part string, quoted bool, rest string, ok bool) {
	if text == "" {
		return "", false, "", false
	}
	if closing, isQuote := closingQuote(text[0]); isQuote {
		var name strings.Builder
		for i := 1; i < len(text); i++ {
			if text[i] != closing {
				name.WriteByte(text[i])
				continue
			}
			if closing != ']' && i+1 < len(text) && text[i+1] == closing {
				name.WriteByte(closing)
				i++
				continue
			}
			return name.String(), true, text[i+1:], name.Len() > 0
		}
		return "", false, "", false
	}
	end := 0
	for end < len(text) && bareIdentifierByte(text[end]) {
		end++
	}
	if end == 0 {
		return "", false, "", false
	}
	return text[:end], false, text[end:], true
}

func closingQuote(open byte) (byte, bool) {
	switch open {
	case '"', '`':
		return open, true
	case '[':
		return ']', true
	default:
		return 0, false
	}
}

func bareIdentifierByte(b byte) bool {
	return identifierByte(b) || b == '$' || b >= 0x80
}

func trailingAlias(text string) (string, bool) {
	text = strings.TrimRight(text, sqlWhitespace)
	if text == "" {
		return "", false
	}
	start := len(text)
	if opening, quoted := openingQuote(text[len(text)-1]); quoted {
		start = strings.LastIndexByte(text[:len(text)-1], opening)
		if start < 0 {
			return "", false
		}
	} else {
		for start > 0 && bareIdentifierByte(text[start-1]) {
			start--
		}
	}
	if start == 0 || start == len(text) || !strings.ContainsRune(sqlWhitespace, rune(text[start-1])) {
		return "", false
	}
	if strings.Trim(text[:start], sqlWhitespace) == "" {
		return "", false
	}
	part, _, rest, ok := identifierPart(text[start:])
	if !ok || rest != "" {
		return "", false
	}
	return part, true
}

func openingQuote(closing byte) (byte, bool) {
	switch closing {
	case '"', '`':
		return closing, true
	case ']':
		return '[', true
	default:
		return 0, false
	}
}

// splitSelection cuts a selection written as one text into its items, at the
// commas outside parentheses, string literals and quoted identifiers.
func splitSelection(text string) []string {
	var (
		items []string
		depth int
		quote byte
		start int
	)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']'
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			items = append(items, text[start:i])
			start = i + 1
		}
	}
	return append(items, text[start:])
}
