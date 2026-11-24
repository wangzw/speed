// Package fs delivers the object-store byte seam over a local directory, as one exclusive backend module.
package fs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const backendName = "fs"

const (
	rootDirMode os.FileMode = 0o750
	objectMode  os.FileMode = 0o600
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
// Exclusive is set on the byte seam, which is what makes a command name exactly one backend: two
// of them enabled at once fail in resolution.
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

func stance(cfg *moduleConfig) (core.Enablement, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return core.Enablement{}, fmt.Errorf("%w: %s.%s names no directory for the backend to keep an "+
			"object's bytes under", objectstore.ErrInvalidConfig, ModuleName, rootItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func open(_ context.Context, cfg *moduleConfig, _ *slog.Logger) (objectstore.Store, error) {
	return &store{root: cfg.resolve().root}, nil
}

type store struct {
	root string
}

var _ objectstore.Store = (*store)(nil)

func (*store) Name() string { return backendName }

func (s *store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := relativePath(key)
	if err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("%w: the byte count %d is negative", objectstore.ErrInvalidRequest, size)
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
	tmp, err := temporaryName(rel)
	if err != nil {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, objectMode)
	if err != nil {
		return fmt.Errorf("opening the temporary file for the object's bytes: %w", err)
	}
	written, copyErr := io.CopyN(f, r, size)
	var extra bool
	if copyErr == nil {
		extra, copyErr = hasMore(r)
	}
	closeErr := f.Close()
	switch {
	case copyErr != nil && (errors.Is(copyErr, io.EOF) || errors.Is(copyErr, io.ErrUnexpectedEOF)):
		_ = root.Remove(tmp)
		return fmt.Errorf("%w: the stream ended after %d of %d bytes",
			objectstore.ErrInvalidRequest, written, size)
	case copyErr != nil:
		_ = root.Remove(tmp)
		return fmt.Errorf("writing the object's bytes: %w", copyErr)
	case extra:
		_ = root.Remove(tmp)
		return fmt.Errorf("%w: the stream carries more than the %d bytes it declared",
			objectstore.ErrInvalidRequest, size)
	case closeErr != nil:
		_ = root.Remove(tmp)
		return fmt.Errorf("writing the object's bytes: %w", closeErr)
	}
	if renameErr := root.Rename(tmp, rel); renameErr != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("replacing the object's bytes: %w", renameErr)
	}
	return nil
}

func temporaryName(rel string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("naming the temporary file for the object's bytes: %w", err)
	}
	return rel + ".part-" + hex.EncodeToString(suffix[:]), nil
}

func hasMore(r io.Reader) (bool, error) {
	var probe [1]byte
	n, err := io.ReadFull(r, probe[:])
	switch {
	case n > 0:
		return true, nil
	case err == nil:
		return false, nil
	case errors.Is(err, io.EOF):
		return false, nil
	default:
		return false, err
	}
}

func (s *store) Stat(ctx context.Context, key string) (objectstore.Blob, error) {
	if err := ctx.Err(); err != nil {
		return objectstore.Blob{}, err
	}
	rel, err := relativePath(key)
	if err != nil {
		return objectstore.Blob{}, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return objectstore.Blob{}, fmt.Errorf("%w: the backend root holds no bytes",
				objectstore.ErrContentMissing)
		}
		return objectstore.Blob{}, fmt.Errorf("opening the backend root: %w", err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return objectstore.Blob{}, fmt.Errorf("%w: the key names no file",
				objectstore.ErrContentMissing)
		}
		return objectstore.Blob{}, fmt.Errorf("reading the object's bytes' size: %w", err)
	}
	return objectstore.Blob{Size: info.Size()}, nil
}

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
