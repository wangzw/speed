package settings

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	yamlformat "github.com/vislake/speed/pkg/config/format/yaml"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// disciplineCarrier holds one field of every shape the value discipline has a
// rule for. It is declared to configuration and to this module alike, so the
// two see one set of items with one set of types.
type disciplineCarrier struct {
	Window  time.Duration
	Span    disciplineSpan
	Limit   int
	Small   int8
	Count   uint
	Wide    uint64
	Ratio   float64
	Narrow  float32
	On      bool
	Label   string
	Mode    disciplineMode
	Toggle  disciplineToggle
	Starts  time.Time
	Address net.IP
	Level   slog.Level
}

type (
	disciplineMode   string
	disciplineToggle bool
	// disciplineSpan is built on time.Duration, and reflection sees nothing
	// of that: it is an int64 of its own, and both sides hold it to the
	// integer rules.
	disciplineSpan time.Duration
)

// TestTheWriteDisciplineAgreesWithConfiguration holds the two statements of
// the value discipline to one another.
//
// The design gives settings configuration's discipline and no rule of its own,
// and the code states it twice, because nothing in pkg/config exports it. Each
// case is one value written in a YAML document. Configuration reads it through
// its real loading path; this module takes the same value out of the same
// parser's tree — which is what an administrative endpoint decoding a request
// body hands Set — and holds it to the declaration. The two must both refuse
// it, or both accept it and arrive at the same value.
func TestTheWriteDisciplineAgreesWithConfiguration(t *testing.T) {
	cases := []struct {
		field string
		yaml  string
		// accepted is what both sides are expected to decide, so that a case
		// on which the two agree for the wrong reason still fails.
		accepted bool
	}{
		{"window", `"5m"`, true},
		{"window", `1h30m`, true},
		{"window", `300`, false},
		{"window", `1.5`, false},
		{"window", `"300"`, false},
		{"window", `true`, false},
		{"span", `300`, true},
		{"span", `"5m"`, false},
		{"limit", `10`, true},
		{"limit", `10.0`, true},
		{"limit", `10.5`, false},
		{"limit", `"10"`, true},
		{"limit", `" 10"`, false},
		{"limit", `true`, false},
		{"limit", `9.223372036854775808e18`, false},
		{"limit", `-9.223372036854775808e18`, true},
		{"small", `300`, false},
		{"small", `-128`, true},
		{"count", `-1`, false},
		{"count", `7`, true},
		{"wide", `18446744073709551615`, true},
		{"wide", `1.8e19`, true},
		{"wide", `1.8446744073709551616e19`, false},
		{"ratio", `1`, true},
		{"ratio", `0.125`, true},
		{"ratio", `"0.5"`, true},
		{"ratio", `yes`, false},
		{"ratio", `9007199254740992`, true},
		{"ratio", `9007199254740993`, false},
		{"ratio", `"9007199254740993"`, false},
		{"ratio", `"-9007199254740993"`, false},
		{"narrow", `3.5e38`, false},
		{"narrow", `16777216`, true},
		{"narrow", `16777217`, false},
		{"narrow", `16777217.0`, false},
		{"narrow", `"16777217"`, false},
		{"narrow", `0.1`, true},
		{"narrow", `"0.1"`, true},
		{"narrow", `1e-50`, false},
		{"on", `true`, true},
		{"on", `"true"`, true},
		{"on", `1`, false},
		{"on", `yes`, false},
		{"label", `hello`, true},
		{"label", `10`, false},
		{"mode", `manual`, true},
		{"toggle", `false`, true},
		{"starts", `"2026-09-21T12:30:00Z"`, true},
		{"starts", `"yesterday"`, false},
		{"starts", `5`, false},
		{"address", `"10.0.0.1"`, true},
		{"address", `"nope"`, false},
		{"level", `WARN`, true},
		{"level", `4`, true},
		{"level", `"loud"`, false},
	}

	keys := tableOf(t, declaring("discipline", Schema{
		Namespace: "discipline",
		Mounts:    []Mount{{Value: &disciplineCarrier{}}},
	}))
	for _, c := range cases {
		t.Run(c.field+"="+c.yaml, func(t *testing.T) {
			document := "discipline:\n  " + c.field + ": " + c.yaml + "\n"
			configValue, configErr := decodeThroughConfiguration(t, document, c.field)
			entry, declared := keys.lookup("discipline." + c.field)
			if !declared {
				t.Fatalf("the carrier declares no key for field %q", c.field)
			}
			settingsValue, settingsErr := writeThroughSettings(t, entry, document, c.field)

			if (configErr == nil) != c.accepted {
				t.Fatalf("configuration decided accepted=%v (%v), and the case expects %v",
					configErr == nil, configErr, c.accepted)
			}
			if (settingsErr == nil) != c.accepted {
				t.Fatalf("settings decided accepted=%v (%v), and configuration decided %v",
					settingsErr == nil, settingsErr, c.accepted)
			}
			if settingsErr != nil && !errors.Is(settingsErr, ErrTypeMismatch) {
				t.Errorf("the refusal is %v, want ErrTypeMismatch", settingsErr)
			}
			if c.accepted && !reflect.DeepEqual(configValue, settingsValue) {
				t.Errorf("configuration arrived at %#v and settings at %#v", configValue, settingsValue)
			}
		})
	}
}

// decodeThroughConfiguration loads a YAML document the way a host does and
// decodes the carrier, returning the value the one field holds.
func decodeThroughConfiguration(t *testing.T, document, field string) (any, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the document: %v", err)
	}
	reg := core.New()
	reg.Register(yamlformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{Name: "discipline", Resources: []any{config.Schema{
		Namespace: "discipline",
		Mounts:    []config.Mount{{Value: &disciplineCarrier{}}},
	}}})
	reader, err := config.Load(context.Background(), reg, []string{"--config", "file://" + path})
	if err != nil {
		t.Fatalf("loading the document: %v", err)
	}
	var carrier disciplineCarrier
	if err := reader.Decode("discipline", &carrier); err != nil {
		return nil, err
	}
	return fieldValue(t, carrier, field), nil
}

// writeThroughSettings takes the field's value out of the parsed document,
// encodes it the way Set does and decodes the stored text the way a snapshot
// load does, returning what a read would hand out.
func writeThroughSettings(t *testing.T, entry keyEntry, document, field string) (any, error) {
	t.Helper()
	formats := core.Resources[config.Format](registryWith(t, yamlformat.Module()))
	if len(formats) != 1 {
		t.Fatalf("want the YAML format alone, got %d formats", len(formats))
	}
	tree, err := formats[0].Value.Unmarshal([]byte(document))
	if err != nil {
		t.Fatalf("parsing the document: %v", err)
	}
	section, ok := tree["discipline"].(map[string]any)
	if !ok {
		t.Fatalf("the document has no discipline section: %#v", tree)
	}
	text, err := encodeValue(entry, section[field])
	if err != nil {
		return nil, err
	}
	decoded, err := decodeValue(entry, text)
	if err != nil {
		t.Fatalf("the stored text %q does not decode back: %v", text, err)
	}
	return decoded, nil
}

// fieldValue returns the value of the carrier field a key segment names.
func fieldValue(t *testing.T, carrier disciplineCarrier, segment string) any {
	t.Helper()
	v := reflect.ValueOf(carrier)
	for i := range v.NumField() {
		if camelToKebab(v.Type().Field(i).Name) == segment {
			return v.Field(i).Interface()
		}
	}
	t.Fatalf("the carrier has no field for %q", segment)
	return nil
}
