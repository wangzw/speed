package http

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The member names encoding/json accepts without a word: a name that matches a
// field only when case is ignored fills that field, and a name written twice
// in one object keeps the last value. The first turns a name the API does not
// have into one it does; the second drops a value the client sent. Both are
// read one way here and possibly another way by whatever read the body before
// this process did, which is what makes them worth refusing.

type memberBase struct {
	ID string `json:"id"`
}

type memberInner struct {
	X int `json:"x"`
}

type memberTarget struct {
	memberBase
	Name  string            `json:"name"`
	Inner memberInner       `json:"inner"`
	List  []memberInner     `json:"list"`
	Tags  map[string]int    `json:"tags"`
	Extra map[string]any    `json:"extra"`
	When  time.Time         `json:"when"`
	Raw   json.RawMessage   `json:"raw"`
	Ptr   *memberInner      `json:"ptr"`
	Named map[string]string `json:"named"`
	Plain int
}

// caseTwins has two fields whose names differ only in case. encoding/json
// prefers the exact match, so each name fills its own field and neither is a
// variant of the other.
type caseTwins struct {
	Lower int `json:"id"`
	Upper int `json:"ID"`
}

func TestDecodeRefusesAMemberNameThatDiffersFromItsFieldOnlyInCase(t *testing.T) {
	for _, body := range []string{
		`{"NAME":"a"}`,
		`{"Id":"1"}`,
		`{"inner":{"X":1}}`,
		`{"list":[{"x":1},{"X":2}]}`,
		`{"ptr":{"X":1}}`,
		`{"plain":1}`,
	} {
		var got memberTarget
		err := Decode(postWithBody(body), &got)
		if !errors.Is(err, ErrMalformedBody) {
			t.Errorf("the body %s was answered with %v, not ErrMalformedBody", body, err)
			continue
		}
		if StatusFor(err) != nethttp.StatusBadRequest {
			t.Errorf("the body %s maps to %d, want 400", body, StatusFor(err))
		}
	}

	var got memberTarget
	err := Decode(postWithBody(`{"NAME":"a"}`), &got)
	if err == nil || !strings.Contains(err.Error(), `"NAME"`) || !strings.Contains(err.Error(), `"name"`) {
		t.Errorf("the failure %v does not name both the member sent and the field it resembles", err)
	}
}

func TestDecodeRefusesAMemberNameWrittenTwice(t *testing.T) {
	for _, body := range []string{
		`{"name":"a","name":"b"}`,
		`{"name":"a","Name":"b"}`,
		`{"inner":{"x":1,"x":2}}`,
		`{"list":[{"x":1,"x":2}]}`,
		`{"tags":{"a":1,"a":2}}`,
		`{"extra":{"a":{"b":1,"b":2}}}`,
	} {
		var got memberTarget
		err := Decode(postWithBody(body), &got)
		if !errors.Is(err, ErrMalformedBody) {
			t.Errorf("the body %s was answered with %v, not ErrMalformedBody", body, err)
		}
	}

	var got memberTarget
	err := Decode(postWithBody(`{"name":"a","name":"b"}`), &got)
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Errorf("the failure %v does not name the member written twice", err)
	}
}

// TestDecodeAcceptsWhatNamesItsFieldsExactly is the other half: the check must
// not refuse a body encoding/json reads as intended.
func TestDecodeAcceptsWhatNamesItsFieldsExactly(t *testing.T) {
	body := `{
		"id": "7",
		"name": "a",
		"inner": {"x": 1},
		"list": [{"x": 2}, {"x": 3}],
		"tags": {"a": 1, "A": 2},
		"extra": {"k": {"v": 1, "V": 2}, "l": [{"q": 1, "Q": 2}]},
		"when": "2026-09-28T12:00:00Z",
		"raw": {"left": 1, "left": 2},
		"ptr": {"x": 4},
		"named": {"x": "y"},
		"Plain": 5
	}`
	var got memberTarget
	if err := Decode(postWithBody(body), &got); err != nil {
		t.Fatalf("a body naming every field exactly was refused: %v", err)
	}
	if got.ID != "7" || got.Name != "a" || got.Inner.X != 1 || len(got.List) != 2 ||
		got.Tags["A"] != 2 || got.Ptr == nil || got.Ptr.X != 4 || got.Plain != 5 || got.When.IsZero() {
		t.Errorf("the body was accepted but decoded into %+v", got)
	}

	var twins caseTwins
	if err := Decode(postWithBody(`{"id":1,"ID":2}`), &twins); err != nil {
		t.Fatalf("two fields whose names differ in case were refused: %v", err)
	}
	if twins != (caseTwins{Lower: 1, Upper: 2}) {
		t.Errorf("the two fields decoded into %+v, want {1 2}", twins)
	}

	var anything any
	if err := Decode(postWithBody(`{"a":1,"A":2}`), &anything); err != nil {
		t.Errorf("an untyped body with two distinct member names was refused: %v", err)
	}
	if err := Decode(postWithBody(`{"a":1,"a":2}`), &anything); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("an untyped body naming one member twice was answered with %v, not ErrMalformedBody", err)
	}
}

type shapeA struct {
	A      int
	Shared int `json:"shared"`
}

type shapeB struct {
	B      int
	Shared int `json:"shared"`
}

type shapeC struct {
	C int `json:"c"`
}

type shapeDeep struct{ shapeC }

// shapeTwice embeds shapeC as shapeDeep does, so shapeC reaches shapes twice
// at one depth and its field cancels out.
type shapeTwice struct{ shapeC }

type shapeHidden struct {
	Visible int `json:"visible"`
}

// shapes gathers the field rules that decide a member name: promotion out of
// an embedded struct and an embedded pointer, two tagged fields of one name at
// one depth cancelling out, one struct reached twice at one depth, a shallower
// field winning, "-", and an unexported embedded struct whose own fields are
// exported.
type shapes struct {
	shapeA
	*shapeB
	shapeDeep
	shapeTwice
	shapeHidden
	Own     int `json:"own"`
	Skip    int `json:"-"`
	C       int
	Renamed int `json:"A"`
	hidden  int
}

// TestTheMemberNamesAreEncodingJSONs holds jsonFields to encoding/json's own
// rules. The package encodes and decodes a struct through one set of field
// rules, so the member names it writes for a value are the names it reads.
func TestTheMemberNamesAreEncodingJSONs(t *testing.T) {
	encoded, err := json.Marshal(shapes{shapeB: &shapeB{}, hidden: 1})
	if err != nil {
		t.Fatalf("encoding the sample: %v", err)
	}
	var written map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &written); err != nil {
		t.Fatalf("reading the sample back: %v", err)
	}

	fields := jsonFields(reflect.TypeFor[shapes]())
	for name := range written {
		if _, ok := fields[name]; !ok {
			t.Errorf("encoding/json writes the member %q, which jsonFields does not know", name)
		}
	}
	for name := range fields {
		if _, ok := written[name]; !ok {
			t.Errorf("jsonFields knows a member %q that encoding/json does not write", name)
		}
	}
}

// TestDecodeStillReportsAnUnknownFieldByName keeps the unknown-field message
// the caller already relies on: a name that matches no field in any case is
// left to the decoder, which names it.
func TestDecodeStillReportsAnUnknownFieldByName(t *testing.T) {
	var got memberTarget
	err := Decode(postWithBody(`{"nickname":"a"}`), &got)
	if !errors.Is(err, ErrMalformedBody) || !strings.Contains(err.Error(), "nickname") {
		t.Errorf("an unknown field came back as %v", err)
	}
}
