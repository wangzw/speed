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
	}
	for _, c := range cases {
		if got := connectionScoped(c.dsn); got != c.scoped {
			t.Errorf("connectionScoped(%q) = %v, want %v", c.dsn, got, c.scoped)
		}
	}
}
