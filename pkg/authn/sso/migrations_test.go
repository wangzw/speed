package sso

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationCommentsHoldNoSemicolons guards the migration splitter used
// above. It cuts a file at every semicolon, so a semicolon inside a --
// comment leaves the tail of that comment as a statement that runs as SQL.
func TestMigrationCommentsHoldNoSemicolons(t *testing.T) {
	root := ".."
	scanned := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".sql" {
			return nil
		}
		if !strings.Contains(filepath.ToSlash(path), "/migrations/") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "--") {
				continue
			}
			if strings.Contains(trimmed, ";") {
				t.Errorf("%s:%d: a semicolon inside a comment is read as a statement break: %q", path, i+1, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the authn migrations: %v", err)
	}
	if scanned == 0 {
		t.Fatalf("found no migration files under %s", root)
	}
}
