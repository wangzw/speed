package otel

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// TestALaterLayerOverridesAnEarlierOne pins the direction of the merge
// the whole resource is built with.
func TestALaterLayerOverridesAnEarlierOne(t *testing.T) {
	low := resource.NewSchemaless(attribute.String("name", "low"), attribute.String("only.low", "a"))
	mid := resource.NewSchemaless(attribute.String("name", "mid"), attribute.String("only.mid", "b"))
	high := resource.NewSchemaless(attribute.String("name", "high"))

	got, err := layered(low, mid, high)
	if err != nil {
		t.Fatalf("layering three schemaless resources: %v", err)
	}
	want := map[string]string{"name": "high", "only.low": "a", "only.mid": "b"}
	for key, value := range want {
		v, ok := got.Set().Value(attribute.Key(key))
		if !ok || v.AsString() != value {
			t.Errorf("%s is %q (present %v), want %q in %v", key, v.AsString(), ok, value, got.Attributes())
		}
	}
	if got.Len() != len(want) {
		t.Errorf("the layered resource holds %d attributes, want %d: %v", got.Len(), len(want), got.Attributes())
	}
}

// TestLayersOnDifferentSchemasAreRefused pins that the conflict the
// upstream reports is not dropped.
func TestLayersOnDifferentSchemasAreRefused(t *testing.T) {
	first := resource.NewWithAttributes("https://example.test/schemas/1", attribute.String("name", "first"))
	second := resource.NewWithAttributes("https://example.test/schemas/2", attribute.String("name", "second"))

	got, err := layered(first, second)
	if !errors.Is(err, resource.ErrSchemaURLConflict) {
		t.Fatalf("layering resources on two schemas returned %v, want the upstream's conflict", err)
	}
	if got != nil {
		t.Errorf("the refused layering still returned %v", got.Attributes())
	}
}
