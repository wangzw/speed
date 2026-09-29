package sqlite

import "testing"

// TestConnectionScopedReadsTheLocatorTheWayTheDriverDoes pins which locators
// hold a handle to one connection. Reading one too narrowly leaves a private
// database under a pool, where tables appear and vanish between connections;
// reading one too widely takes the pool away from a database every connection
// shares.
func TestConnectionScopedReadsTheLocatorTheWayTheDriverDoes(t *testing.T) {
	cases := []struct {
		dsn    string
		scoped bool
	}{
		{"", true},
		{":memory:", true},
		{":memory:?_pragma=foreign_keys(1)", true},
		{"file::memory:", true},
		{"file:", true},
		{"file:scratch?mode=memory", true},
		{"file::memory:?cache=shared", false},
		{"file:scratch?mode=memory&cache=shared", false},
		{"/var/lib/app/app.db", false},
		{"/var/lib/app/app.db?_pragma=foreign_keys(1)", false},
		{"file:/var/lib/app/app.db?mode=rwc", false},
		// Outside a file: URI the parameters are the driver's, cut off
		// before SQLite sees the name, so cache=shared shares nothing.
		{":memory:?cache=shared", true},
		// SQLite takes a parameter named twice from its last occurrence.
		{"file:scratch?mode=rwc&mode=memory", true},
		{"file:scratch?mode=memory&mode=rwc", false},
		// It decodes escapes in names and values, and ends either at an
		// escaped NUL.
		{"file:scratch?mod%65=memory", true},
		{"file:scratch?mode=memor%79", true},
		{"file:scratch?mode=memory%00rwc", true},
		{"file:scratch?mode%00x=memory", true},
	}
	for _, c := range cases {
		if got := connectionScoped(c.dsn); got != c.scoped {
			t.Errorf("connectionScoped(%q) = %v, want %v", c.dsn, got, c.scoped)
		}
	}
}

// TestSharesCacheReadsTheLocatorTheWayTheDriverDoes pins which locators put
// their connections in SQLite's shared-cache mode, which Dialector refuses.
// Reading one too narrowly opens a database whose lock waits nothing bounds.
func TestSharesCacheReadsTheLocatorTheWayTheDriverDoes(t *testing.T) {
	cases := []struct {
		dsn    string
		shared bool
	}{
		{"file::memory:?cache=shared", true},
		{"file:scratch?mode=memory&cache=shared", true},
		{"file:/var/lib/app/app.db?cache=shared", true},
		{"file:/var/lib/app/app.db?cache=private&cache=shared", true},
		{"file:/var/lib/app/app.db?cache=shared&cache=private", false},
		{"file:/var/lib/app/app.db?cach%65=shared", true},
		{"file:/var/lib/app/app.db?cache=shar%65d", true},
		{"file:/var/lib/app/app.db?cache=shared%00private", true},
		{"file:/var/lib/app/app.db?cache%00x=shared", true},
		{"file:/var/lib/app/app.db?cache=private", false},
		{"file:/var/lib/app/app.db?Cache=shared", false},
		{"file:/var/lib/app/app.db?cache=shared+", false},
		{"file:/var/lib/app/app.db", false},
		{":memory:", false},
		{"", false},
		// Outside a file: URI the parameters are the driver's.
		{":memory:?cache=shared", false},
		{"/var/lib/app/app.db?cache=shared", false},
	}
	for _, c := range cases {
		if got := sharesCache(c.dsn); got != c.shared {
			t.Errorf("sharesCache(%q) = %v, want %v", c.dsn, got, c.shared)
		}
	}
}
