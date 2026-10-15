package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v5/client"
	"github.com/alibabacloud-go/tea/dara"

	"github.com/vislake/speed/pkg/sms"
)

// testAccessKeySecret is the value the cases configure and then look for in
// everything the module produces.
const testAccessKeySecret = "test-access-key-secret-9f3a2c"

// senderOver builds a sender whose client points at the given endpoint and
// protocol, which is how a case reaches the vendor SDK with a loopback server.
func senderOver(t *testing.T, endpoint, protocol string) *sender {
	t.Helper()
	client, err := dysmsapi.NewClient(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String(testAccessKeySecret),
		Endpoint:        dara.String(endpoint),
		Protocol:        dara.String(protocol),
	})
	if err != nil {
		t.Fatalf("building the carrier client against the loopback endpoint: %v", err)
	}
	return newSender(resolved{signName: "Speed"}, client)
}

// jsonReply answers every request with one body.
func jsonReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// oneResolved is the send the cases drive: a message the registry has already
// resolved to a vendor code and the parameters the approved template
// substitutes.
func oneResolved() sms.Resolved {
	return sms.Resolved{
		To:       "+8613800000000",
		Template: "authn.code.login",
		Locale:   "zh-CN",
		Code:     "SMS_LOGIN_ZH",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Ordered:  []string{"123456", "5"},
		DedupeID: "challenge-1",
	}
}

func TestTheSenderReportsTheCarrierName(t *testing.T) {
	s := newSender(resolved{signName: "Speed"}, nil)
	if got := s.Name(); got != "aliyun" {
		t.Errorf("Name() = %q, want aliyun", got)
	}
	if ModuleName != "sms.aliyun" {
		t.Errorf("ModuleName = %q, want sms.aliyun", ModuleName)
	}
}

// TestTheRequestCarriesTheSignatureTemplateAndParameters holds the request shape
// the vendor interface takes: the approved signature, the resolved destination
// and template code, and the parameters as a JSON object keyed by name.
func TestTheRequestCarriesTheSignatureTemplateAndParameters(t *testing.T) {
	var got struct {
		method, phoneNumbers, signName, templateCode, templateParam string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.method = r.Method
		got.phoneNumbers = r.FormValue("PhoneNumbers")
		got.signName = r.FormValue("SignName")
		got.templateCode = r.FormValue("TemplateCode")
		got.templateParam = r.FormValue("TemplateParam")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","Message":"OK","BizId":"biz-42","RequestId":"req-1"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	receipt, err := s.Send(t.Context(), oneResolved())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("the carrier was called with %s, want POST", got.method)
	}
	if got.phoneNumbers != "+8613800000000" {
		t.Errorf("PhoneNumbers = %q, want the resolved destination +8613800000000", got.phoneNumbers)
	}
	if got.signName != "Speed" {
		t.Errorf("SignName = %q, want the configured signature Speed", got.signName)
	}
	if got.templateCode != "SMS_LOGIN_ZH" {
		t.Errorf("TemplateCode = %q, want the resolved code SMS_LOGIN_ZH", got.templateCode)
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(got.templateParam), &params); err != nil {
		t.Fatalf("TemplateParam = %q, which is not JSON: %v", got.templateParam, err)
	}
	if params["code"] != "123456" || params["ttl_minutes"] != "5" {
		t.Errorf("TemplateParam = %v, want the message parameters", params)
	}
	if receipt.ProviderID != "biz-42" {
		t.Errorf("ProviderID = %q, want the carrier's BizId biz-42", receipt.ProviderID)
	}
}

// TestTheStatusCodesFallToUncertain is the per-code vector: until a code is
// verified against the vendor's list, every status the carrier can answer falls
// to the uncertain sentinel, and OK is the only success.
func TestTheStatusCodesFallToUncertain(t *testing.T) {
	codes := []string{
		"isv.BUSINESS_LIMIT_CONTROL",
		"isv.DAY_LIMIT_CONTROL",
		"isv.OUT_OF_SERVICE",
		"isv.SMS_TEMPLATE_ILLEGAL",
		"isv.SMS_SIGNATURE_ILLEGAL",
		"isv.MOBILE_NUMBER_ILLEGAL",
		"isv.TEMPLATE_MISSING_PARAMETERS",
		"isp.RAM_PERMISSION_DENY",
		"SignatureDoesNotMatch",
		"InvalidAccessKeyId.NotFound",
		"",
		"SomethingNobodyListed",
	}
	for _, code := range codes {
		t.Run("code "+code, func(t *testing.T) {
			server := httptest.NewServer(jsonReply(`{"Code":` + mustJSON(code) + `,"Message":"no"}`))
			defer server.Close()
			s := senderOver(t, server.Listener.Addr().String(), "http")
			_, err := s.Send(t.Context(), oneResolved())
			if !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("send answered %v, want the uncertain sentinel for the status %q", err, code)
			}
			if errors.Is(err, sms.ErrThrottled) || errors.Is(err, sms.ErrRejected) {
				t.Errorf("send classified %q as throttled or rejected; no Aliyun status is verified to mean either", code)
			}
			if code != "" && !strings.Contains(err.Error(), code) {
				t.Errorf("the error text %q does not name the status %q", err.Error(), code)
			}
		})
	}

	t.Run("OK succeeds", func(t *testing.T) {
		server := httptest.NewServer(jsonReply(`{"Code":"OK","BizId":"biz-1"}`))
		defer server.Close()
		s := senderOver(t, server.Listener.Addr().String(), "http")
		if _, err := s.Send(t.Context(), oneResolved()); err != nil {
			t.Fatalf("send answered %v, want success for the status OK", err)
		}
	})
}

// TestATransportFailureIsUncertain holds that a call that did not complete — a
// network error and a timeout alike — falls to the uncertain sentinel, the
// direction which never resends a message the carrier may already hold.
func TestATransportFailureIsUncertain(t *testing.T) {
	t.Run("a network error", func(t *testing.T) {
		// A port nothing listens on: the dial fails inside the SDK.
		s := senderOver(t, "127.0.0.1:1", "http")
		_, err := s.Send(t.Context(), oneResolved())
		if !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("send answered %v, want the uncertain sentinel", err)
		}
	})

	t.Run("a timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Code":"OK"}`)
		}))
		defer server.Close()
		defer close(release)

		s := senderOver(t, server.Listener.Addr().String(), "http")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := s.Send(ctx, oneResolved())
		if !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("send answered %v, want the uncertain sentinel for a call the context ended", err)
		}
	})
}

// TestACancelledContextDoesNotReachTheCarrier holds that the adapter passes the
// caller's context to the vendor call: a send whose context is already ended is
// refused as uncertain without the endpoint being reached, so a cancelled caller
// does not leave a connection open to the carrier.
func TestACancelledContextDoesNotReachTheCarrier(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Send(ctx, oneResolved())
	if !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("a cancelled send answered %v, want the uncertain sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for a cancelled send, want 0", n)
	}
}

// TestTheCodeAndTheCredentialsReachNoErrorOrLog holds the facts the design
// fixes: the access-key secret only signs the request and is not carried in it,
// and neither the secret nor the message code turns up in the error text, even
// when the carrier echoes them back in its own answer. A status that is not OK
// and a transport failure are both driven, since the two leave by different
// paths.
func TestTheCodeAndTheCredentialsReachNoErrorOrLog(t *testing.T) {
	const code = "135790"
	echoed := `{"Code":"isv.SMS_TEMPLATE_ILLEGAL","Message":"the code ` + code +
		` and the key ` + testAccessKeySecret + ` were rejected"}`

	for _, c := range []struct {
		name   string
		status int
		reply  string
	}{
		{"a status that is not OK", http.StatusOK, echoed},
		{"a transport failure", http.StatusBadRequest, echoed},
	} {
		t.Run(c.name, func(t *testing.T) {
			var request strings.Builder
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				body, _ := io.ReadAll(r.Body)
				request.WriteString(r.Method + " " + r.URL.String() + "\n")
				for name, values := range r.Header {
					request.WriteString(name + ": " + strings.Join(values, ",") + "\n")
				}
				request.Write(body)
				request.WriteString(r.Form.Encode())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.reply)
			}))
			defer server.Close()

			s := senderOver(t, server.Listener.Addr().String(), "http")
			m := oneResolved()
			m.Params = map[string]string{"code": code, "ttl_minutes": "5"}
			_, err := s.Send(t.Context(), m)
			if err == nil {
				t.Fatal("send answered no error")
			}
			if !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("send answered %v, want the uncertain sentinel", err)
			}
			if strings.Contains(request.String(), testAccessKeySecret) {
				t.Errorf("the access-key secret is carried in the request:\n%s", request.String())
			}
			for _, secret := range []string{testAccessKeySecret, code} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the error text %q carries %q", err.Error(), secret)
				}
			}
		})
	}
}

// TestTheCarrierIsCalledExactlyOnceEvenWhenItFails holds the no-retry rule: the
// vendor interface is not idempotent, so a failing call is never made again,
// neither by this module nor by the SDK's own retry, which is switched off.
func TestTheCarrierIsCalledExactlyOnceEvenWhenItFails(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		// A server error, the shape the SDK's retry would have picked up.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"Code":"InternalError"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	if _, err := s.Send(t.Context(), oneResolved()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("send answered %v, want the uncertain sentinel", err)
	}
	if calls != 1 {
		t.Errorf("the carrier was called %d times, want exactly one: the interface is not idempotent", calls)
	}
}

// mustJSON encodes a status body, so a code with a quote or a backslash still
// produces valid JSON.
func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestNoVendorTypeReachesTheExportedSurface holds that the vendor SDK is an
// implementation detail: the package exports the module name, its config
// namespace and the module descriptor and nothing else, and neither signature
// names a vendor type, so a host that imports this package to register it never
// has to name the SDK.
func TestNoVendorTypeReachesTheExportedSurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's directory: %v", err)
	}

	var exported []string
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed++
		vendorAliases := vendorImportNames(file)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A method on an unexported type is not on the
				// package's surface.
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				exported = append(exported, "func "+d.Name.Name)
				if namesVendorType(d.Type, vendorAliases) {
					t.Errorf("the signature of %s names a vendor type", d.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if names := exportedSpec(d.Tok, spec); len(names) > 0 {
						exported = append(exported, names...)
					}
				}
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no non-test source file was parsed, so the surface was not read")
	}

	want := []string{"const ModuleName", "const ConfigNamespace", "func Module"}
	if len(exported) != len(want) {
		t.Fatalf("exported surface = %v, want %v", exported, want)
	}
	for _, w := range want {
		if !containsString(exported, w) {
			t.Errorf("exported surface = %v, want it to hold %q", exported, w)
		}
	}
}

// vendorImportNames returns the local names a file gives the vendor packages,
// so a selector on one of them can be told apart from a type of this package.
func vendorImportNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.Contains(path, "alibabacloud-go") {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			name = parts[len(parts)-1]
		}
		names[name] = true
	}
	return names
}

// namesVendorType reports whether a type expression is a selector on a vendor
// package.
func namesVendorType(node ast.Node, aliases map[string]bool) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && aliases[ident.Name] {
			found = true
		}
		return true
	})
	return found
}

// exportedSpec names the exported declarations a spec introduces, under the
// keyword of the declaration it belongs to.
func exportedSpec(tok token.Token, spec ast.Spec) []string {
	var names []string
	switch s := spec.(type) {
	case *ast.ValueSpec:
		kind := "var"
		if tok == token.CONST {
			kind = "const"
		}
		for _, name := range s.Names {
			if name.IsExported() {
				names = append(names, kind+" "+name.Name)
			}
		}
	case *ast.TypeSpec:
		if s.Name.IsExported() {
			names = append(names, "type "+s.Name.Name)
		}
	}
	return names
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// One identifier reaches the carrier once, and a second call is answered with the
// first call's outcome rather than sending the message again. The interface is
// not idempotent, so a re-call would deliver and bill a second message.
func TestOneIdentifierReachesTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","BizId":"biz-7"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	m := oneResolved()

	firstReceipt, firstErr := s.Send(t.Context(), m)
	secondReceipt, secondErr := s.Send(t.Context(), m)

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier, want 1", n)
	}
	if firstErr != nil || secondErr != nil {
		t.Fatalf("the sends failed: %v and %v", firstErr, secondErr)
	}
	if firstReceipt != secondReceipt {
		t.Errorf("the second call was answered %+v, want the first call's %+v", secondReceipt, firstReceipt)
	}
}

// Distinct identifiers each reach the carrier: a resend builds a new challenge
// and so a new identifier, which is not held back by the one before it.
func TestDistinctIdentifiersEachReachTheCarrier(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","BizId":"biz-7"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	for _, id := range []string{"chal-a", "chal-b", "chal-c"} {
		m := oneResolved()
		m.DedupeID = id
		if _, err := s.Send(t.Context(), m); err != nil {
			t.Fatalf("sending %s: %v", id, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("the carrier was called %d times for three identifiers, want 3", n)
	}
}

// Calls for one identifier that arrive together still reach the carrier once: the
// first reserves it and the rest wait for that call's outcome.
func TestConcurrentCallsForOneIdentifierReachTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","BizId":"biz-7"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Send(t.Context(), oneResolved()); err != nil {
				t.Errorf("sending: %v", err)
			}
		}()
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier under concurrency, want 1", n)
	}
}
