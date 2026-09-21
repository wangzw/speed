package settings

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// tableName is the settings table. It is written here rather than derived from
// a Go type because nothing in this module goes through the model path: the
// statements are native SQL, so the name is stated once for the writer and
// once in each dialect's migration file, and the two are kept in step by hand.
const tableName = "settings"

// cell identifies one explicitly written row: a key in one layer, the layer
// being the row's tenant column, empty for the global row.
type cell struct {
	tenant string
	key    string
}

// snapshot is the in-memory copy of every written row, decoded under its
// declaration.
//
// It holds two indexes rather than one because a row whose text does not
// decode must not be silently read as unset: such a row is recorded under its
// cell and reported when a read lands on it, while every other key keeps
// working. Loading therefore never fails over one bad value.
//
// A snapshot is replaced as a whole and never written in place: reads take the
// pointer once and the copy they took does not change under them.
type snapshot struct {
	values map[cell]stored
	broken map[cell]brokenRow
}

// brokenRow is a row that is present but whose text does not decode under its
// declaration.
//
// The text is kept beside the error because a change event reports the
// explicit value of the layer it wrote, and a layer holding a row nobody can
// decode is not the same as a layer holding none. A write that overwrites such
// a row reports what was there; reporting an empty old value would say the
// layer was empty, which is a different fact and one the reader would act on.
type brokenRow struct {
	text string
	err  error
}

// stored is one written row: the value decoded under its declaration, and the
// text it was decoded from.
//
// The text is kept beside the value because a change event reports the
// explicit value of the layer it wrote, and that value is the row as it was —
// re-encoding the decoded value would give a text that means the same but is
// spelled differently from what the row holds ("5m0s" for a row reading
// "300s").
type stored struct {
	value any
	text  string
}

// emptySnapshot is the state the store starts in, before Init loads the table.
// Every read of a key falls through to its declared default, which is also
// what a read of an unwritten key resolves to once the load has happened.
func emptySnapshot() *snapshot {
	return &snapshot{values: make(map[cell]stored), broken: make(map[cell]brokenRow)}
}

// explicitText returns the text of one layer's row, and reports whether that
// layer holds one. It is what a change event reports as the value before a
// write.
func (s *snapshot) explicitText(l Layer, key string) (string, bool) {
	c := cell{tenant: tenantOf(l), key: key}
	if row, ok := s.values[c]; ok {
		return row.text, true
	}
	// A row that is there but broken is still the layer's value.
	if broken, ok := s.broken[c]; ok {
		return broken.text, true
	}
	return "", false
}

// value resolves a read: the named layer's row, then the global row, then the
// declaration's default. A read that names the global layer starts at the
// global row, which is the bottom of the fallback, so it ends there too.
func (s *snapshot) value(entry keyEntry, l Layer) (any, error) {
	if found, ok, err := s.layer(entry.key, l); ok {
		return found, err
	}
	if !isGlobal(l) {
		if found, ok, err := s.layer(entry.key, Global()); ok {
			return found, err
		}
	}
	return entry.def, nil
}

// layer reports what one layer holds for a key. found is false when the layer
// has no row, which is what sends a read to the next layer; a row that failed
// to decode is found, and carries its error.
func (s *snapshot) layer(key string, l Layer) (value any, found bool, err error) {
	c := cell{tenant: tenantOf(l), key: key}
	if broken, ok := s.broken[c]; ok {
		return nil, true, broken.err
	}
	row, ok := s.values[c]
	if !ok {
		return nil, false, nil
	}
	return row.value, true, nil
}

// row is one settings row as it is read back for loading.
type row struct {
	TenantID string `gorm:"column:tenant_id"`
	Key      string `gorm:"column:key"`
	Value    string `gorm:"column:value"`
}

// loadSnapshot reads the whole table and decodes every row under its
// declaration.
//
// Rows whose key has no declaration are skipped rather than recorded: a key
// without a declaration is refused by Get before the snapshot is consulted at
// all, and such rows are the residue Delete exists to clean up. Only a row
// whose declaration is known but whose text does not decode is carried as
// broken.
//
// The failure of the read itself is where ErrTableMissing is decided.
func loadSnapshot(ctx context.Context, handle *gorm.DB, keys *keyTable) (*snapshot, error) {
	var rows []row
	err := handle.WithContext(ctx).Raw("SELECT tenant_id, key, value FROM " + tableName).Scan(&rows).Error
	if err != nil {
		if tableMissing(err, tableName) {
			return nil, fmt.Errorf("%w: %s. Write %s into the declaration set of the command that "+
				"applies migrations, so the table is built with this module's migration set",
				ErrTableMissing, tableName, ModuleName)
		}
		return nil, fmt.Errorf("settings: reading the %s table: %w", tableName, err)
	}

	loaded := emptySnapshot()
	for _, r := range rows {
		entry, declared := keys.lookup(r.Key)
		if !declared {
			continue
		}
		c := cell{tenant: r.TenantID, key: r.Key}
		decoded, err := decodeValue(entry, r.Value)
		if err != nil {
			loaded.broken[c] = brokenRow{text: r.Value, err: err}
			continue
		}
		loaded.values[c] = stored{value: decoded, text: r.Value}
	}
	return loaded, nil
}

// tableMissing reports whether a read failed because the table is not there.
//
// This module decides it from the driver's own text rather than from a typed
// error, because there is none to catch: the database module's sentinels cover
// connecting, plugins, applying migrations and the migration lock, and it
// deliberately states there is no fifth. Only the absent table is folded in
// here; every other read failure is passed through as its implementation named
// it, so that a connection failure or a permission problem does not get the
// fixing action that belongs to the missing table.
func tableMissing(err error, table string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	name := strings.ToLower(table)
	// SQLite says "no such table: settings"; PostgreSQL says
	// `relation "settings" does not exist` and tags it with SQLSTATE 42P01.
	if strings.Contains(text, "42p01") {
		return true
	}
	if !strings.Contains(text, name) {
		return false
	}
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}
