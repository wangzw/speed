package board

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// document is the answer's outer shape. Adding a field beside Features is a
// compatible change; changing what Features holds is not.
type document struct {
	Features []string `json:"features"`
}

// serve writes the frozen answer. The bytes were computed in Start, so a request
// reads no registry, no configuration and no backend, and has no failure path.
//
// no-store is part of the contract: the content is a property of the process
// that answered, and may later be narrowed per caller and tenant, so it must not
// be kept across sessions.
func (b *board) serve(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/json", b.body)
}

// digestOf renders the summary the Start log carries. It is computed over the
// names in order, joined by newlines, so it changes exactly when the set does.
func digestOf(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
