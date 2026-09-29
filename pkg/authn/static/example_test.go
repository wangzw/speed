package static_test

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn/static"
)

// ExampleHashAt writes a table entry at a cost a test can afford. The count
// travels inside the entry, so the backend reading the table verifies it at
// that count through the same path a production entry takes.
func ExampleHashAt() {
	entry, err := static.HashAt("correct horse", 1_000)
	if err != nil {
		fmt.Println(err)
		return
	}
	// The salt is drawn fresh on every call, so only the fields that do not
	// depend on it are printed: the scheme and the count.
	fields := strings.Split(entry, "$")
	fmt.Println(fields[0], fields[1])

	_, err = static.ParseHash(entry)
	fmt.Println(err)
	// Output:
	// pbkdf2-sha256 1000
	// <nil>
}
