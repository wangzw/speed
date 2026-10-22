// Package fs delivers the object-storage byte seam over a local directory.
//
// It is a backend module and nothing else. An object's bytes land as files
// under one directory, addressed by the internal key the metadata module
// generates and maps to a path beneath that directory; reading returns the
// file's contents and deleting removes the file. A host takes it in by writing
// ModuleName into a command. The byte seam is delivered exclusively, so a
// command names exactly one backend and two at once fail in resolution.
//
// The whole package depends on the standard library and the objectstore root
// package alone, so an assembly that uses only a local directory carries no
// object-storage SDK. Its bytes are visible only to the process that can reach
// that directory: a multi-replica assembly that shares one set of bytes needs
// the S3 backend, not this one.
//
// The backend holds no credential and interprets no part of a key beyond the
// mapping its own file system needs; the internal key reaches no error text and
// no log line.
package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// ModuleName is this module's name in the registry.
const ModuleName = "objectstore.fs"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// backendName is the name this backend reports through Store.Name. It is a
// constant of the code: an assembly names exactly one backend, and a host
// reading a diagnostic has to be able to tell which.
const backendName = "fs"

// The permissions this backend lays down: a directory only its owner and group
// may enter, and a file of object bytes only its owner may read.
const (
	rootDirMode os.FileMode = 0o750
	objectMode  os.FileMode = 0o600
)

// init registers this module with the process registry. A host chooses its
// backend by importing it, and what enters its go.mod follows the packages it
// imported: a host that takes only this backend binds no object-storage SDK.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is set on the byte seam, which is what makes a command name exactly
// one backend: two of them enabled at once fail in resolution. The stance reads
// the configuration, so a section this backend cannot run on is a startup
// failure ahead of every construction. The logging capability is taken up as an
// optional requirement, declared by the factory every backend shares; this
// backend holds no credential to register and so returns nothing through the
// factory's secrets hook.
func Module() core.Module {
	return objectstore.NewModule(objectstore.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Open:            open,
	})
}

// stance states that the backend runs once its configuration is sound. The
// directory it cannot run without is declared required, so the loader has
// already refused a section that omits it before this is reached; what is left
// here is a value the loader accepted but the backend cannot use — an empty
// root, which names no directory.
func stance(cfg *moduleConfig) (core.Enablement, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return core.Enablement{}, fmt.Errorf("%w: %s.%s names no directory for the backend to keep an "+
			"object's bytes under", objectstore.ErrInvalidConfig, ModuleName, rootItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open builds the store over the configured directory. Production passes the
// directory the host named; the directory itself is created on the first Put,
// so an assembly may start before the root exists. This backend makes no call
// to a service, so it writes no call line and ignores the logger the factory
// hands it.
func open(_ context.Context, cfg *moduleConfig, _ *slog.Logger) (objectstore.Store, error) {
	return &store{root: cfg.resolve().root}, nil
}

// store is this module's product: the byte seam over one local directory.
type store struct {
	// root is the directory every key is resolved under, cleaned once at
	// construction.
	root string
}

// store must satisfy the seam it delivers.
var _ objectstore.Store = (*store)(nil)

// Name reports the name this backend answers under. It is a constant: the
// assembly names exactly one backend, and the name identifies it in a
// diagnostic.
func (*store) Name() string { return backendName }

// Put stores the bytes read from r under key, creating the root and any missing
// parent directory. The size the seam carries is not needed here: this backend
// writes the stream as it arrives and records no length of its own, so it is
// ignored.
//
// The reader is read once and is not held past the call, so two callers may put
// different objects through one store at the same time. A key this backend will
// not map is refused before anything is created; a directory that cannot be made
// or a write that fails reports the file system's own error, which the metadata
// module is what wraps up into the capability's unavailable sentinel. A second
// Put to the same key overwrites the first, which is what the upload step relies
// on when a stored byte set is retried.
func (s *store) Put(ctx context.Context, key string, r io.Reader, _ int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := relativePath(key)
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(s.root, rootDirMode); mkErr != nil {
		return fmt.Errorf("creating the backend root: %w", mkErr)
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return fmt.Errorf("opening the backend root: %w", err)
	}
	defer func() { _ = root.Close() }()
	if dir := filepath.Dir(rel); dir != "." {
		if mkErr := root.MkdirAll(dir, rootDirMode); mkErr != nil {
			return fmt.Errorf("creating the directory for the object's bytes: %w", mkErr)
		}
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, objectMode)
	if err != nil {
		return fmt.Errorf("opening the object's bytes for writing: %w", err)
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("writing the object's bytes: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("writing the object's bytes: %w", closeErr)
	}
	return nil
}

// Open returns a read-only stream over the bytes stored under key. The caller
// closes it.
//
// A key this backend will not map is refused; a key with no bytes behind it
// reports the file system's own not-exist error, which the metadata module is
// what tells apart from an unavailable backend.
func (s *store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, err := relativePath(key)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("opening the backend root: %w", err)
	}
	f, err := root.Open(rel)
	closeErr := root.Close()
	if err != nil {
		return nil, fmt.Errorf("opening the object's bytes for reading: %w", err)
	}
	if closeErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("closing the backend root: %w", closeErr)
	}
	return f, nil
}

// Delete removes the bytes stored under key. It is idempotent: bytes that are
// not there are not an error, and a key this backend will not map is the only
// key it refuses.
func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := relativePath(key)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opening the backend root: %w", err)
	}
	defer func() { _ = root.Close() }()
	if removeErr := root.Remove(rel); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return fmt.Errorf("removing the object's bytes: %w", removeErr)
	}
	return nil
}
