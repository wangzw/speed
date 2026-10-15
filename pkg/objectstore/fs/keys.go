package fs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The internal key's grammar, as this backend reads it in order to map a key to
// a path. The key is a sequence of segments joined by keySeparator; each segment
// names one path element under the backend root. The grammar is the capability's
// private convention and the metadata module is what generates a key in it; this
// backend only interprets a key as far as mapping it to a path requires, and it
// exposes no part of the key in what it returns.
//
// A segment can hold any rune a file name may hold, within the length cap. The
// separator is the one small piece of the grammar this backend fixes, because a
// key that reaches the file system cannot be allowed to spell a path element
// boundary through a rune it smuggled past the split.
const (
	// keySeparator divides an internal key into its segments.
	keySeparator = "/"
	// maxSegmentLength caps one segment, in bytes. It is the common file-name
	// limit, which is what keeps a key this backend accepts one a file system
	// will hold.
	maxSegmentLength = 255
)

// errInvalidKey reports a key this backend will not map to a path under its
// root: an empty key, a key with an empty or traversal-carrying segment, a
// segment holding a separator or NUL, or a segment over the length cap. It is a
// caller defect — the metadata module generates the key — so it is a distinct
// package sentinel rather than one of the capability's own.
var errInvalidKey = errors.New("objectstore.fs: the key is not one this backend maps to a path")

// relativePath maps an internal key to a path relative to this backend's root,
// refusing a key it will not map.
//
// Every segment is validated before anything is joined, and a path built only of
// validated segments cannot leave the root: no segment is empty or a directory
// traversal, none carries a separator or NUL, so the joined path has exactly the
// segments it was given and no more. The operations then reach the file system
// through an os.Root, which refuses any name that would resolve outside the root
// as well, so a key this check ever let through still could not address a file
// outside.
func relativePath(key string) (string, error) {
	segments, err := splitKey(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(segments...), nil
}

// splitKey breaks a key into its segments, refusing one that is not a legal
// sequence of path elements.
func splitKey(key string) ([]string, error) {
	if key == "" {
		return nil, fmt.Errorf("%w: the key is empty", errInvalidKey)
	}
	segments := strings.Split(key, keySeparator)
	for _, segment := range segments {
		if err := checkSegment(segment); err != nil {
			return nil, err
		}
	}
	return segments, nil
}

// checkSegment refuses a segment this backend will not turn into one path
// element.
//
// A segment has to name a plain file name: it cannot be empty, cannot be the
// current or parent directory, cannot carry a path separator of any of the
// shapes this backend might meet (the key separator, the host's own separator,
// or a backslash a differently-built host would read as one), cannot carry a
// NUL, and cannot exceed the length cap. These are the conditions that keep a
// key from addressing a file outside the root or a directory rather than a file.
func checkSegment(segment string) error {
	switch {
	case segment == "":
		return fmt.Errorf("%w: a segment is empty", errInvalidKey)
	case segment == "." || segment == "..":
		return fmt.Errorf("%w: a segment names a directory, not a file", errInvalidKey)
	case len(segment) > maxSegmentLength:
		return fmt.Errorf("%w: a segment is longer than %d bytes", errInvalidKey, maxSegmentLength)
	case strings.ContainsRune(segment, 0):
		return fmt.Errorf("%w: a segment carries a NUL", errInvalidKey)
	case strings.Contains(segment, keySeparator),
		strings.ContainsRune(segment, filepath.Separator),
		strings.Contains(segment, `\`):
		return fmt.Errorf("%w: a segment carries a path separator", errInvalidKey)
	}
	return nil
}
