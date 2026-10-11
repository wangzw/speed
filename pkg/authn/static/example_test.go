package static_test

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn/static"
)

// ExampleHashPBKDF2 writes a table entry at a cost a test can afford. The count
// travels inside the entry, so the backend reading the table verifies it at that
// count through the same path a production entry takes.
func ExampleHashPBKDF2() {
	entry, err := static.HashPBKDF2("correct horse", 1_000)
	if err != nil {
		fmt.Println(err)
		return
	}
	// The salt and the hash depend on the salt drawn, so only the part that does
	// not is printed: the algorithm and the parameters.
	fields := strings.Split(entry, "$")
	fmt.Println(fields[1], fields[2])

	fmt.Println(static.ParseHash(entry))
	// Output:
	// pbkdf2-sha256 i=1000,l=32
	// <nil>
}
