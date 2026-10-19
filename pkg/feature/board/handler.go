package board

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// read answers the read route. The document was computed in Start, so a request
// reads no registry, no configuration and no backend, and has no failure path.
// The entry point supplies the input and writes the declared response, no-store
// header included.
func (b *board) read(context.Context, struct{}) (FeatureBoardDocument, error) {
	return b.document, nil
}

// digestOf renders the summary the Start log carries. It is computed over the
// names in order, joined by newlines, so it changes exactly when the set does.
func digestOf(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
