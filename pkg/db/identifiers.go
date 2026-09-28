package db

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// sqlWhitespace is what SQL text separates tokens with.
const sqlWhitespace = " \t\r\n\f\v"

// sealedTarget reports which encrypted field of the model a column written into
// a statement names, and whether the name could be judged at all.
//
// A column reaches the database as text, and the database, not GORM, decides
// which column that text is. GORM resolves a name by field name, by column name
// and through its naming strategy, and writes whatever it cannot resolve as it
// stands — quoted, unless the column is raw. SQLite then matches identifiers
// without regard to letter case, quoted or not; PostgreSQL folds an unquoted one
// to lower case; and a name that arrives already quoted is taken as its quoted
// content by both. So the judgement is made on the text the statement will
// carry: the column is the one GORM resolves the name to when that is an
// encrypted field, and otherwise the encrypted column whose name the text's
// last identifier matches, letters compared without regard to case. That is
// SQLite's rule, and it covers PostgreSQL's, which never matches a name SQLite
// would not.
//
// Text that is not an identifier — an expression, a function call, an escape
// form — cannot be judged here, because this package does not parse SQL, and
// false is the answer for it.
func sealedTarget(st *gorm.Statement, column clause.Column) (*schema.Field, bool) {
	if field := st.Schema.LookUpField(column.Name); field != nil && field.DBName != "" && isEncrypted(field) {
		return field, true
	}
	text := column.Name
	if !column.Raw {
		var quoted strings.Builder
		st.QuoteTo(&quoted, clause.Column{Name: column.Name})
		text = quoted.String()
	}
	name, ok := columnName(text)
	if !ok {
		return nil, false
	}
	for _, field := range encryptedFields(st.Schema) {
		if strings.EqualFold(name, field.DBName) {
			return field, true
		}
	}
	return nil, true
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
//
// The quoting read is the union of what the two engines accept — double quotes,
// backticks and square brackets, a doubled closing quote standing for itself —
// so a form one engine rejects is read the way the other would resolve it.
func columnName(text string) (string, bool) {
	rest := strings.Trim(text, sqlWhitespace)
	for {
		part, remainder, ok := identifierPart(rest)
		if !ok {
			return "", false
		}
		remainder = strings.TrimLeft(remainder, sqlWhitespace)
		if remainder == "" {
			return part, true
		}
		if remainder[0] != '.' {
			return "", false
		}
		rest = strings.TrimLeft(remainder[1:], sqlWhitespace)
	}
}

// identifierPart reads one identifier from the start of text, and returns it
// unquoted along with the text after it.
func identifierPart(text string) (part, rest string, ok bool) {
	if text == "" {
		return "", "", false
	}
	if closing, quoted := closingQuote(text[0]); quoted {
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
			return name.String(), text[i+1:], name.Len() > 0
		}
		return "", "", false
	}
	end := 0
	for end < len(text) && bareIdentifierByte(text[end]) {
		end++
	}
	if end == 0 {
		return "", "", false
	}
	return text[:end], text[end:], true
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
	part, rest, ok := identifierPart(text[start:])
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
