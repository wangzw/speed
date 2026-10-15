package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// storeOver builds a backend over a fresh directory under the case's temporary
// area and answers the store and the directory.
func storeOver(t *testing.T) (*store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	return &store{root: root}, root
}

// putBytes stores body under key, failing the case on any error.
func putBytes(t *testing.T, s *store, key string, body string) {
	t.Helper()
	if err := s.Put(t.Context(), key, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

// readAll reads back what is stored under key, failing the case on any error.
func readAll(t *testing.T, s *store, key string) string {
	t.Helper()
	r, err := s.Open(t.Context(), key)
	if err != nil {
		t.Fatalf("Open(%q): %v", key, err)
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			t.Errorf("closing the stream for %q: %v", key, cerr)
		}
	}()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading %q: %v", key, err)
	}
	return string(body)
}

// TestPutAndOpenRoundTrip holds that what is stored under a key is what is read
// back, across a multi-segment key, which is the shape the metadata module
// generates.
func TestPutAndOpenRoundTrip(t *testing.T) {
	s, root := storeOver(t)
	const key = "tenant-1/object-id/uuid.bin"
	putBytes(t, s, key, "the object's bytes")

	if got := readAll(t, s, key); got != "the object's bytes" {
		t.Errorf("read back %q, want the bytes that were stored", got)
	}
	if _, err := os.Stat(filepath.Join(root, "tenant-1", "object-id", "uuid.bin")); err != nil {
		t.Errorf("the bytes are not under the key's path: %v", err)
	}
}

// TestPutCreatesMissingParentDirectories holds that a key whose segments name
// directories that do not exist yet still stores, which is what lets an assembly
// start before the root is laid out.
func TestPutCreatesMissingParentDirectories(t *testing.T) {
	s, _ := storeOver(t)
	putBytes(t, s, "a/b/c/leaf", "nested")

	if got := readAll(t, s, "a/b/c/leaf"); got != "nested" {
		t.Errorf("read back %q, want the nested bytes", got)
	}
}

// TestPutOverwritesAnExistingKey holds the upload step's retry: a second Put to
// one key replaces the bytes the first left.
func TestPutOverwritesAnExistingKey(t *testing.T) {
	s, _ := storeOver(t)
	putBytes(t, s, "k", "the first byte set")
	putBytes(t, s, "k", "the second")

	if got := readAll(t, s, "k"); got != "the second" {
		t.Errorf("read back %q, want the second byte set", got)
	}
}

// TestDeleteIsIdempotent holds that deleting a key removes its bytes and that a
// second delete of the same key is not an error.
func TestDeleteIsIdempotent(t *testing.T) {
	s, root := storeOver(t)
	putBytes(t, s, "gone/soon", "bytes")

	if err := s.Delete(t.Context(), "gone/soon"); err != nil {
		t.Fatalf("the first Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone", "soon")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the bytes are still there after Delete: %v", err)
	}
	if err := s.Delete(t.Context(), "gone/soon"); err != nil {
		t.Errorf("the second Delete answered %v, want success", err)
	}
}

// TestDeleteOfAKeyWithNoBytesSucceeds holds the idempotence from the other side:
// a key never written is deleted without an error, which is what the expiry
// sweep relies on when a Put landed bytes and the row never moved.
func TestDeleteOfAKeyWithNoBytesSucceeds(t *testing.T) {
	s, _ := storeOver(t)
	if err := s.Delete(t.Context(), "never/written"); err != nil {
		t.Errorf("Delete of a key with no bytes answered %v, want success", err)
	}
}

// TestOpenOfAKeyWithNoBytesReportsNotExist holds that a read with no bytes
// behind it reports the file system's own not-exist error, which is the shape
// the metadata module tells apart from an unavailable backend.
func TestOpenOfAKeyWithNoBytesReportsNotExist(t *testing.T) {
	s, _ := storeOver(t)
	_, err := s.Open(t.Context(), "no/such/bytes")
	if err == nil {
		t.Fatal("Open of a key with no bytes succeeded, want a not-exist error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open answered %v, want a not-exist error", err)
	}
}

// illegalKeys are the keys this backend must refuse: each names a key that
// cannot become a sequence of plain file names under the root.
var illegalKeys = map[string]string{
	"empty":            "",
	"empty segment":    "a//b",
	"leading segment":  "/a/b",
	"trailing segment": "a/b/",
	"current dir":      "a/./b",
	"parent dir":       "a/../b",
	"bare parent":      "..",
	"bare current":     ".",
	"would escape":     "../outside",
	"deeper escape":    "a/../../outside",
	"nul":              "a/b\x00c",
	"backslash":        `a\b`,
	"overlong segment": "a/" + strings.Repeat("x", maxSegmentLength+1),
}

// TestAnIllegalKeyIsRefused holds the key grammar to be a boundary: every method
// refuses a key that is not a legal sequence of path elements, through the one
// sentinel, and nothing is created for it.
func TestAnIllegalKeyIsRefused(t *testing.T) {
	s, root := storeOver(t)
	for name, key := range illegalKeys {
		t.Run(name, func(t *testing.T) {
			if err := s.Put(t.Context(), key, strings.NewReader("bytes"), 5); !errors.Is(err, errInvalidKey) {
				t.Errorf("Put(%q) answered %v, want errInvalidKey", key, err)
			}
			if _, err := s.Open(t.Context(), key); !errors.Is(err, errInvalidKey) {
				t.Errorf("Open(%q) answered %v, want errInvalidKey", key, err)
			}
			if err := s.Delete(t.Context(), key); !errors.Is(err, errInvalidKey) {
				t.Errorf("Delete(%q) answered %v, want errInvalidKey", key, err)
			}
			if _, err := os.Stat(root); err == nil {
				t.Errorf("the root was created for the refused key %q", key)
			}
		})
	}
}

// TestAKeyThatWouldEscapeTheRootLeavesNothingOutside holds the security boundary
// as a property rather than a single key: for a set of keys that try to climb
// out of the root, the store refuses every one and writes nothing outside the
// root.
func TestAKeyThatWouldEscapeTheRootLeavesNothingOutside(t *testing.T) {
	s, root := storeOver(t)
	parent := filepath.Dir(root)

	attempts := []string{"../escaped", "a/../../escaped", "..", `..\..\escaped`}
	for _, key := range attempts {
		if err := s.Put(t.Context(), key, strings.NewReader("x"), 1); !errors.Is(err, errInvalidKey) {
			t.Errorf("Put(%q) answered %v, want errInvalidKey", key, err)
		}
	}

	entries, err := os.ReadDir(parent)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading the parent directory: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "escaped" {
			t.Errorf("a refused key landed a file outside the root: %s", filepath.Join(parent, entry.Name()))
		}
	}
}

// TestACancelledContextIsRefused holds that every method honours the call's
// context rather than ignoring it.
func TestACancelledContextIsRefused(t *testing.T) {
	s, _ := storeOver(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.Put(ctx, "k", strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Put answered %v, want context.Canceled", err)
	}
	if _, err := s.Open(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Open answered %v, want context.Canceled", err)
	}
	if err := s.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete answered %v, want context.Canceled", err)
	}
}

// TestTheStoreIsSafeForConcurrentUse holds that one store answers several
// callers at once: the store holds no per-call state, so distinct keys put,
// read and deleted from many goroutines never interfere.
func TestTheStoreIsSafeForConcurrentUse(t *testing.T) {
	s, _ := storeOver(t)
	const workers = 16
	const rounds = 25

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				key := fmt.Sprintf("worker/%d/%d", w, r)
				body := []byte{byte(w), byte(r)}
				if err := s.Put(t.Context(), key, bytes.NewReader(body), int64(len(body))); err != nil {
					t.Errorf("Put(%q): %v", key, err)
					return
				}
				rc, err := s.Open(t.Context(), key)
				if err != nil {
					t.Errorf("Open(%q): %v", key, err)
					return
				}
				got, err := io.ReadAll(rc)
				if cerr := rc.Close(); cerr != nil {
					t.Errorf("closing the stream for %q: %v", key, cerr)
					return
				}
				if err != nil {
					t.Errorf("reading %q: %v", key, err)
					return
				}
				if !bytes.Equal(got, body) {
					t.Errorf("read back %v under %q, want %v", got, key, body)
					return
				}
				if err := s.Delete(t.Context(), key); err != nil {
					t.Errorf("Delete(%q): %v", key, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestNameReportsTheBackend holds that the diagnostic name is this backend's
// constant.
func TestNameReportsTheBackend(t *testing.T) {
	s, _ := storeOver(t)
	if got := s.Name(); got != "fs" {
		t.Errorf("Name = %q, want %q", got, "fs")
	}
}
