package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	// updatedAt is when the row was last written, kept for the same reason
	// stored keeps it: a revision read answers for a row that is there,
	// whatever decoding its value turned out to do.
	updatedAt time.Time
}

// stored is one written row: the value decoded under its declaration, and the
// text it was decoded from.
//
// The text is kept beside the value because a change event reports the
// explicit value of the layer it wrote, and that value is the row as it was —
// re-encoding the decoded value would give a text that means the same but is
// spelled differently from what the row holds ("5m0s" for a row reading
// "300s"). A read of a type that carries its own text form decodes the text
// again rather than handing out the value, for the reason Get gives; the
// value decoded at load is what tells a row that decodes from one that does
// not.
type stored struct {
	value any
	text  string
	// updatedAt is when this row was last written. It is kept beside the value
	// so that a reader comparing the value with an instant — the admission
	// layer comparing a session's authentication time with the moment a
	// requirement was switched on — gets both from one snapshot, with no IO and
	// no race between two reads.
	updatedAt time.Time
}

// emptySnapshot is a snapshot holding no rows, the one a load fills.
//
// It is not the state a store starts in. Before the first load the store holds
// no snapshot at all, which is what lets a read tell "nothing has been read
// from the table" apart from "the table holds nothing for this key".
func emptySnapshot() *snapshot {
	return &snapshot{values: make(map[cell]stored), broken: make(map[cell]brokenRow)}
}

// explicitText returns the text of one layer's row, and reports whether that
// layer holds one. It is what a change event reports as the value before a
// write.
//
// A store that has not loaded yet has no snapshot, and a write it makes then
// reports the layer as empty: the write itself goes to the table, and the
// value before it is one this replica never read.
func (s *snapshot) explicitText(l Layer, key string) (string, bool) {
	if s == nil {
		return "", false
	}
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
//
// What it resolves to is the decoded value together with the text it was
// decoded from, the declared default's text for a key no layer covers.
func (s *snapshot) value(entry keyEntry, l Layer) (stored, error) {
	if found, ok, err := s.layer(entry.key, l); ok {
		return found, err
	}
	if !isGlobal(l) {
		if found, ok, err := s.layer(entry.key, Global()); ok {
			return found, err
		}
	}
	return stored{value: entry.def, text: entry.defText}, nil
}

// layer reports what one layer holds for a key. found is false when the layer
// has no row, which is what sends a read to the next layer; a row that failed
// to decode is found, and carries its error.
func (s *snapshot) layer(key string, l Layer) (row stored, found bool, err error) {
	c := cell{tenant: tenantOf(l), key: key}
	if broken, ok := s.broken[c]; ok {
		return stored{}, true, broken.err
	}
	row, ok := s.values[c]
	if !ok {
		return stored{}, false, nil
	}
	return row, true, nil
}

// writtenAt reports when one layer's row for a key was last written, and
// whether that layer holds such a row.
//
// It is the revision read, and it deliberately does not fall back: the answer
// is about the explicit row of the named layer, so a layer holding no row of
// its own answers absent even when a value read would resolve through the
// layer below. A row whose text did not decode still answers its write time,
// because the revision is a fact about the row and knowing it needs no decode.
func (s *snapshot) writtenAt(key string, l Layer) (time.Time, bool) {
	c := cell{tenant: tenantOf(l), key: key}
	if broken, ok := s.broken[c]; ok {
		return broken.updatedAt, true
	}
	row, ok := s.values[c]
	if !ok {
		return time.Time{}, false
	}
	return row.updatedAt, true
}

// row is one settings row as it is read back for loading.
type row struct {
	TenantID  string    `gorm:"column:tenant_id"`
	Key       string    `gorm:"column:key"`
	Value     string    `gorm:"column:value"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
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
	err := handle.WithContext(ctx).Raw("SELECT tenant_id, key, value, updated_at FROM " + tableName).Scan(&rows).Error
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
			loaded.broken[c] = brokenRow{text: r.Value, err: err, updatedAt: r.UpdatedAt}
			continue
		}
		loaded.values[c] = stored{value: decoded, text: r.Value, updatedAt: r.UpdatedAt}
	}
	return loaded, nil
}

// sameRows reports whether two snapshots hold the same rows, read the same
// way.
//
// A row is compared by the text it was decoded from and the time it was
// written: the text is what the table holds, and two rows carrying one text
// decode to one value under one declaration, while a rewrite of the same value
// still moves the row's write time and so changes the revision a reader gets.
// That also keeps a snapshot loaded before a row's declaration changed from
// reading as unchanged, which is right — the rows are the same; what changed
// was how they are read, and that is a startup-time matter.
func sameRows(a, b *snapshot) bool {
	if len(a.values) != len(b.values) || len(a.broken) != len(b.broken) {
		return false
	}
	for c, row := range b.values {
		other, ok := a.values[c]
		if !ok || other.text != row.text || !other.updatedAt.Equal(row.updatedAt) {
			return false
		}
	}
	for c, row := range b.broken {
		other, ok := a.broken[c]
		if !ok || other.text != row.text || !other.updatedAt.Equal(row.updatedAt) {
			return false
		}
	}
	return true
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
