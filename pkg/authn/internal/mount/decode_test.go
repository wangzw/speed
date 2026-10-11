package mount

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type decoded struct {
	A int    `json:"a"`
	S string `json:"s"`
}

// decodeWith runs Decode as a handler's first step and reports what it returned,
// what it left in dst, and whether the chain was ended.
func decodeWith(contentType, body string) (code int, ok bool, got decoded, aborted bool, problemType string) {
	var dst decoded
	var res bool
	var ab bool
	w := serve(func(c *gin.Context) {
		res = Decode(c, &dst)
		ab = c.IsAborted()
		if res {
			c.Status(http.StatusNoContent)
		}
	}, http.MethodPost, map[string]string{"Content-Type": contentType}, strings.NewReader(body))
	if w.Body.Len() > 0 {
		problemType, _ = problemBodyOfRaw(w.Body.Bytes())["type"].(string)
	}
	return w.Code, res, dst, ab, problemType
}

// TestDecodeTakesOnlyApplicationJSON holds the media types: the one, with no
// parameter or with charset=utf-8, and a 415 for every other, including the ones
// that only look like it.
func TestDecodeTakesOnlyApplicationJSON(t *testing.T) {
	for _, ct := range []string{
		"application/json", "Application/JSON", "application/json; charset=utf-8", "application/json;charset=UTF-8",
		`application/json; charset="utf-8"`,
	} {
		code, ok, got, _, _ := decodeWith(ct, `{"a":1}`)
		if !ok || code != http.StatusNoContent || got.A != 1 {
			t.Errorf("Content-Type %q: ok = %v, status %d, got %+v; want it taken", ct, ok, code, got)
		}
	}
	for _, ct := range []string{
		"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x",
		"application/json-patch+json", "application/problem+json", "application/jsonp", "text/json",
		"application/json; charset=utf-16", "application/json; charset=iso-8859-1", "application/json; boundary=x",
		"application/json; charset=utf-8; version=2", "application/json;;", "json", "application/", "application/json, text/plain",
		"application/*",
	} {
		code, ok, got, aborted, typ := decodeWith(ct, `{"a":1}`)
		if ok || code != http.StatusUnsupportedMediaType || typ != "urn:speed:authn:unsupported-media-type" || !aborted {
			t.Errorf("Content-Type %q: ok = %v, status %d, type %q, aborted %v; want a 415 with the chain ended", ct, ok, code, typ, aborted)
		}
		if got != (decoded{}) {
			t.Errorf("Content-Type %q: the body was read into %+v although the type was refused", ct, got)
		}
	}
}

// TestDecodeRefusesWhatIsNotExactlyOneObject holds the 400s: a field the target
// does not have, anything after the value, an empty or broken body, and a value
// of the wrong type.
func TestDecodeRefusesWhatIsNotExactlyOneObject(t *testing.T) {
	for name, body := range map[string]string{
		"an unknown field":                              `{"a":1,"extra":2}`,
		"an unknown field alone":                        `{"extra":2}`,
		"known fields in another case, and one unknown": `{"A":1,"S":"x","Extra":1}`,
		"a second object":                               `{"a":1}{"a":2}`,
		"a second object after a space":                 `{"a":1} {"a":2}`,
		"trailing text":                                 `{"a":1} x`,
		"trailing text directly":                        `{"a":1}x`,
		"a trailing comma object":                       `{"a":1},`,
		"a trailing array":                              `{"a":1}[]`,
		"a trailing number":                             `{"a":1} 7`,
		"an empty body":                                 ``,
		"white space alone":                             ` `,
		"not JSON":                                      `a=1&s=2`,
		"a truncated object":                            `{"a":1`,
		"the wrong type":                                `{"a":"one"}`,
		"an array for an object":                        `[1]`,
		"a string for an object":                        `"x"`,
		"a number for an object":                        `7`,
		"an unknown field after a repeated key":         `{"a":1,"a":2,"b":3}`,
	} {
		code, ok, _, aborted, typ := decodeWith("application/json", body)
		if ok || code != http.StatusBadRequest || typ != "urn:speed:authn:bad-request" || !aborted {
			t.Errorf("%s (%q): ok = %v, status %d, type %q, aborted %v; want a 400 bad-request with the chain ended",
				name, body, ok, code, typ, aborted)
		}
	}
}

// TestDecodeAllowsWhiteSpaceAroundTheValue holds the other side of the trailing
// check: white space after the value is not data.
func TestDecodeAllowsWhiteSpaceAroundTheValue(t *testing.T) {
	for _, body := range []string{`{"a":1}`, "{\"a\":1}\n", "  {\"a\":1}  ", "\t\r\n{\"a\":1}\r\n\r\n", `{ "a" : 1 }`} {
		code, ok, got, _, _ := decodeWith("application/json", body)
		if !ok || code != http.StatusNoContent || got.A != 1 {
			t.Errorf("body %q: ok = %v, status %d, got %+v; want it taken", body, ok, code, got)
		}
	}
}

// TestDecodeHoldsTheBodyAtSixtyFourKiB holds the limit at the byte: a body of
// exactly 64 KiB is read, one byte more is refused, and a body far past it is
// refused without being read to the end.
func TestDecodeHoldsTheBodyAtSixtyFourKiB(t *testing.T) {
	if MaxBodyBytes != 64*1024 {
		t.Fatalf("MaxBodyBytes = %d, want 65536", MaxBodyBytes)
	}
	frame := len(`{"s":""}`)
	fill := func(total int) string { return `{"s":"` + strings.Repeat("a", total-frame) + `"}` }

	exact := fill(MaxBodyBytes)
	if len(exact) != MaxBodyBytes {
		t.Fatalf("the fixture is %d bytes", len(exact))
	}
	if code, ok, got, _, _ := decodeWith("application/json", exact); !ok || code != http.StatusNoContent || len(got.S) != MaxBodyBytes-frame {
		t.Errorf("a body of exactly the limit: ok = %v, status %d, %d bytes read", ok, code, len(got.S))
	}
	over := fill(MaxBodyBytes + 1)
	if code, ok, got, aborted, typ := decodeWith("application/json", over); ok || code != http.StatusBadRequest || !aborted || typ != "urn:speed:authn:bad-request" || got.S != "" {
		t.Errorf("a body one byte over: ok = %v, status %d, aborted %v, type %q, read %d bytes", ok, code, aborted, typ, len(got.S))
	}
	if code, ok, _, _, _ := decodeWith("application/json", fill(10*MaxBodyBytes)); ok || code != http.StatusBadRequest {
		t.Errorf("a body ten times the limit: ok = %v, status %d", ok, code)
	}
	// Padding after a valid value counts against the limit too.
	padded := `{"a":1}` + strings.Repeat(" ", MaxBodyBytes)
	if code, ok, _, _, _ := decodeWith("application/json", padded); ok || code != http.StatusBadRequest {
		t.Errorf("a valid value followed by white space past the limit: ok = %v, status %d", ok, code)
	}
}

// TestTheBodyIsNotReadWhenTheTypeIsRefused holds the order: the media type is
// judged first, so a body that would also have been malformed is answered 415 and
// not 400.
func TestTheBodyIsNotReadWhenTheTypeIsRefused(t *testing.T) {
	if code, _, _, _, _ := decodeWith("text/plain", `not json at all`); code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415: the type is judged before the body", code)
	}
	if code, _, _, _, _ := decodeWith("application/json", `not json at all`); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body of the right type", code)
	}
}
