package db_test

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
)

// ExampleClose shows the shape a call site uses with a connection it built itself: the close goes
// to a defer beside the Open it belongs to.
func ExampleClose() {
	var handle *gorm.DB

	fmt.Println(db.Close(handle))
	// Output: <nil>
}
