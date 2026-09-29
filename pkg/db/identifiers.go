package db

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// sqlWhitespace is what SQL text separates tokens with.
const sqlWhitespace = " \t\r\n\f\v"

// postgresIdentifierBytes is how much of an identifier PostgreSQL keeps: a
// longer one is cut to this many bytes, at a character boundary, wherever it
// appears.
const postgresIdentifierBytes = 63

// sealedTarget reports which encrypted field of the model a column written into
// a statement names, and whether the name could be judged at all.
//
// A column reaches the database as text, and the database, not GORM, decides
// which column that text is. GORM resolves a name by field name, by column name
// and through its naming strategy, and writes whatever it cannot resolve as it
// stands — quoted, unless the column is raw. So the judgement is made on the
// text the statement will carry: the column is the one GORM resolves the name
// to when that is an encrypted field, and otherwise the encrypted column the
// text's last identifier names on the statement's engine (see namesColumn).
//
// Text that is not an identifier — an expression, a function call, an escape
// form — cannot be judged here, because this package does not parse SQL, and
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

// writtenText is a column as GORM writes it into a statement, its alias left
// off: quoted by the engine's dialect, unless it is raw.
func writtenText(st *gorm.Statement, column clause.Column) string {
	var written strings.Builder
	st.QuoteTo(&written, clause.Column{Table: column.Table, Name: column.Name, Raw: column.Raw})
	return written.String()
}

// namesColumn reports whether an identifier, as the statement carries it,
// names column on the statement's engine. column is a column of the model,
// which GORM always writes quoted, so it stands for itself.
//
// SQLite matches identifiers without regard to the letter case of ASCII
// letters, quoted or not. PostgreSQL folds the ASCII letters of an unquoted
// identifier to lower case, takes a quoted one as it is written, and keeps the
// first 63 bytes of either: two names PostgreSQL resolves apart are two
// columns there, where SQLite may read them as one. An engine the switch does
// not name — a Dialect value added without a rule here — is judged by both
// rules at once, which matches every name either engine would.
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

// engineDialect is the engine behind a statement, as the engine marker on its
// handle records it, and "" for a handle without one.
func engineDialect(st *gorm.Statement) Dialect {
	if engine, ok := engineOf(st.DB); ok {
		return engine.dialect
	}
	return ""
}

// sqliteNames is SQLite's rule: letters compared without regard to case, and
// only the ASCII ones folded.
func sqliteNames(name, column string) bool {
	return asciiLower(name) == asciiLower(column)
}

// postgresNames is PostgreSQL's rule: an unquoted name folded to lower case,
// and either name cut to the bytes PostgreSQL keeps.
func postgresNames(name string, quoted bool, column string) bool {
	if !quoted {
		name = asciiLower(name)
	}
	return postgresKept(name) == postgresKept(column)
}

// postgresKept is what PostgreSQL keeps of an identifier.
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

// asciiLower folds the ASCII letters of a name to lower case and leaves every
// other byte as it is.
func asciiLower(name string) string {
	folded := []byte(name)
	for i, b := range folded {
		if b >= 'A' && b <= 'Z' {
			folded[i] = b + ('a' - 'A')
		}
	}
	return string(folded)
}

// unjudgedColumn is the refusal of a write whose column this package cannot
// tell apart from an encrypted one.
func unjudgedColumn(st *gorm.Statement, column string) error {
	return fmt.Errorf("db: model %s has encrypted fields, and this statement writes a column named %q, "+
		"which is not an identifier this module can resolve the way the database does, so it cannot "+
		"tell whether the value lands in an encrypted column: name the column by its column or field "+
		"name", st.Schema.Name, column)
}

// columnName reads the column a piece of SQL text names: one identifier, bare
// or quoted, optionally qualified by the names before it, with whitespace around
// the dots. It returns the last identifier with its quoting removed, and false
// for text of any other shape.
func columnName(text string) (string, bool) {
	name, _, ok := lastIdentifier(text)
	return name, ok
}

// lastIdentifier is columnName that also reports whether the identifier it
// returns was quoted, which is what PostgreSQL resolves a name by.
//
// The quoting read is the union of what the two engines accept — double quotes,
// backticks and square brackets, a doubled closing quote standing for itself —
// so a form one engine rejects is read the way the other would resolve it.
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

// identifierPart reads one identifier from the start of text, and returns it
// unquoted, whether it was quoted, and the text after it.
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

// closingQuote is the character that closes an identifier quoted by open.
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

// bareIdentifierByte reports whether a byte can stand in an unquoted
// identifier on either engine: a letter, a digit, an underscore, a dollar sign,
// or a byte of a multi-byte character.
func bareIdentifierByte(b byte) bool {
	return identifierByte(b) || b == '$' || b >= 0x80
}

// trailingAlias reads the name a selection item gives its result column
// without the caller having set one apart: the identifier the item ends with,
// after whitespace, with or without AS before it. It reports false for an item
// that does not end that way.
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

// openingQuote is the character that opens an identifier closed by closing.
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
// commas that stand outside parentheses, string literals and quoted
// identifiers.
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
