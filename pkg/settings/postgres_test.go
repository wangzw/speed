package settings

import (
	"context"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/db"
)

type ordering struct {
	AZ     string `config:"a-z"`
	AB     string `config:"ab"`
	ADashB string `config:"a-b"`
	AA     string `config:"aa"`
}

var orderingDialects = []dialectUnderTest{
	{name: "sqlite", dialect: db.SQLite, open: open},
}

// TestListOrdersByTheKeysBytesOnEveryDialect checks that List orders by key bytes on either dialect and under any collation.
func TestListOrdersByTheKeysBytesOnEveryDialect(t *testing.T) {
	want := []string{"order.a-b", "order.a-z", "order.aa", "order.ab"}
	if !slices.IsSorted(want) {
		t.Fatalf("the expected order %v is not the keys' byte order", want)
	}
	for _, d := range orderingDialects {
		t.Run(d.name, func(t *testing.T) {
			keys := tableOf(t, declaring("order", Schema{Namespace: "order", Mounts: []Mount{{Value: &ordering{}}}}))
			store := storeOn(t, d.open(t), d.dialect, keys, newRecordingBus())
			ctx := context.Background()
			for _, key := range []string{"order.ab", "order.aa", "order.a-z", "order.a-b"} {
				if err := store.Set(ctx, Write{Layer: Global(), Key: key, Value: "x"}); err != nil {
					t.Fatalf("writing %s: %v", key, err)
				}
			}
			entries, err := store.List(ctx, Global())
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			got := make([]string, 0, len(entries))
			for _, e := range entries {
				got = append(got, e.Key)
			}
			if !slices.Equal(got, want) {
				t.Errorf("List returned %v, want the byte order %v", got, want)
			}
		})
	}
}
