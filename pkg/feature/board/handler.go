package board

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func (b *board) read(context.Context, struct{}) (FeatureBoardDocument, error) {
	return b.document, nil
}

func digestOf(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
