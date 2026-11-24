package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vislake/speed/pkg/authn/token"
)

const signingKeyPath = "var/token-signing-key.pem"

const signingKeyAlg = "ES256"

const signingKeyMode os.FileMode = 0o600

const signingKeyDirMode os.FileMode = 0o700

func ensureSigningKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking for the signing key at %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), signingKeyDirMode); err != nil {
		return fmt.Errorf("creating the signing key's directory: %w", err)
	}
	pemBytes, err := token.GenerateKey(signingKeyAlg)
	if err != nil {
		return fmt.Errorf("generating the signing key: %w", err)
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, signingKeyMode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("creating the signing key at %s: %w", path, err)
	}
	if _, err := f.Write(pemBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("writing the signing key at %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("writing the signing key at %s: %w", path, err)
	}
	return nil
}
