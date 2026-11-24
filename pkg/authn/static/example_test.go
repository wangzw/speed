package static_test

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn/static"
)

// ExampleHashPBKDF2 writes a table entry at a cost a test can afford.
func ExampleHashPBKDF2() {
	entry, err := static.HashPBKDF2("correct horse", 1_000)
	if err != nil {
		fmt.Println(err)
		return
	}
	fields := strings.Split(entry, "$")
	fmt.Println(fields[1], fields[2])

	fmt.Println(static.ParseHash(entry))
	// Output:
	// pbkdf2-sha256 i=1000,l=32
	// <nil>
}
