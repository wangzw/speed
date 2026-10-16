package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vislake/speed/pkg/authn/token"
)

// signingKeyPath is where a copy of this skeleton keeps the token signing key
// its configurations name, relative to the working directory the program is run
// from. It sits under var/, which .gitignore keeps out of the tree, so a
// generated key is never committed.
const signingKeyPath = "var/token-signing-key.pem"

// signingKeyAlg is the algorithm the generated key is minted for. It matches the
// algorithm the sample configurations name.
const signingKeyAlg = "ES256"

// signingKeyMode is the permission the generated key is written with: owner read
// and write alone, which is what a private key gets.
const signingKeyMode os.FileMode = 0o600

// signingKeyDirMode is the permission of the var/ directory the key lands in.
const signingKeyDirMode os.FileMode = 0o700

// ensureSigningKey makes sure a signing key sits at path: it mints one with the
// token package's GenerateKey when nothing is there yet, and leaves an existing
// file untouched. A key regenerated on every start would invalidate every token
// already issued, so an existing file is what the program runs on.
//
// The file is created exclusively. Two runs that start together race for the
// path, and the one that loses finds the winner's key rather than overwriting it.
// Minting the key is the host's place: the token module neither generates nor
// rotates one, it reads the file its configuration names.
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
