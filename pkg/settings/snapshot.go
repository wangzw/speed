package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// tableName is the settings table, stated here and in each dialect migration
// file; the two are kept in step by hand.
const tableName = "settings"

type cell struct {
	tenant string
	key    string
}

// The in-memory copy of every written row, decoded under its declaration;
// replaced as a whole and never written in place.
type snapshot struct {
	values map[cell]stored
	broken map[cell]brokenRow
}

// brokenRow is a row that is present but whose text does not decode; the text is
// kept so a change event reports what was there.
type brokenRow struct {
	text      string
	err       error
	updatedAt time.Time
}

type stored struct {
	value     any
	text      string
	updatedAt time.Time
}

// emptySnapshot is a snapshot holding no rows, the one a load fills; before the
// first load the store holds no snapshot at all.
func emptySnapshot() *snapshot {
	return &snapshot{values: make(map[cell]stored), broken: make(map[cell]brokenRow)}
}

func (s *snapshot) explicitText(l Layer, key string) (string, bool) {
	if s == nil {
		return "", false
	}
	c := cell{tenant: tenantOf(l), key: key}
	if row, ok := s.values[c]; ok {
		return row.text, true
	}
	if broken, ok := s.broken[c]; ok {
		return broken.text, true
	}
	return "", false
}

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

// writtenAt reports when one layer's row was last written; it deliberately does
// not fall back, and a row that did not decode still answers its time.
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

type row struct {
	TenantID  string    `gorm:"column:tenant_id"`
	Key       string    `gorm:"column:key"`
	Value     string    `gorm:"column:value"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

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

// sameRows compares two snapshots by the text and write time of each row, not by
// the decoded value.
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

func tableMissing(err error, table string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	name := strings.ToLower(table)
	if strings.Contains(text, "42p01") {
		return true
	}
	if !strings.Contains(text, name) {
		return false
	}
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}
