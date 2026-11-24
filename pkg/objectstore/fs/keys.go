package fs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	keySeparator     = "/"
	maxSegmentLength = 255
)

var errInvalidKey = errors.New("objectstore.fs: the key is not one this backend maps to a path")

func relativePath(key string) (string, error) {
	segments, err := splitKey(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(segments...), nil
}

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
