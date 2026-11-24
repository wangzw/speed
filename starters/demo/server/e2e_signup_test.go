package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	maillog "github.com/vislake/speed/pkg/mail/log"
	"github.com/vislake/speed/pkg/tenant"
)

const e2eMailLogMarker = "log mail: a message was sent"

const (
	e2eSignupUsername = "signup-member"
	e2eSignupPassword = "signup-member-password-1"
	e2eSignupEmail    = "signup-member@example.test"
)

type signupDiscovery struct {
	Signup struct {
		Enabled  bool `json:"enabled"`
		Password struct {
			Enabled bool `json:"enabled"`
		} `json:"password"`
	} `json:"signup"`
}

type profileVerification struct {
	Verification struct {
		State string `json:"state"`
		Email string `json:"email"`
	} `json:"verification"`
}

type registrationAnswer struct {
	State        string `json:"state"`
	AccountID    string `json:"account_id"`
	Verification struct {
		State string `json:"state"`
		Email string `json:"email"`
	} `json:"verification"`
}

// TestE2EBaselineHasNoRegistrationEntry holds that the baseline serves the methods document with
// name-and-password registration closed.
func TestE2EBaselineHasNoRegistrationEntry(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "baseline.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seeding the baseline: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	res := get(t, base+"/api/v1/auth/methods")
	if res.status != http.StatusOK {
		t.Fatalf("reading the methods document answered %d:\n%s", res.status, res.body)
	}
	var discovery signupDiscovery
	if err := json.Unmarshal(res.body, &discovery); err != nil {
		t.Fatalf("the methods document is not JSON: %v\n%s", err, res.body)
	}
	if discovery.Signup.Password.Enabled {
		t.Errorf("the baseline advertises name-and-password registration: %s", res.body)
	}
	if discovery.Signup.Enabled {
		t.Errorf("the baseline advertises a registration entry: %s", res.body)
	}
}

// TestE2EPasswordSignupVerifiesItsEmailAndBuildsAPersonalTenant walksthrough registration, the
// personal tenant, the emailed code, verification and the unchanged tenant list.
func TestE2EPasswordSignupVerifiesItsEmailAndBuildsAPersonalTenant(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	serve, ports := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}

	methods := get(t, base+"/api/v1/auth/methods")
	if methods.status != http.StatusOK {
		t.Fatalf("reading the methods document answered %d:\n%s", methods.status, methods.body)
	}
	var discovery signupDiscovery
	if err := json.Unmarshal(methods.body, &discovery); err != nil {
		t.Fatalf("the methods document is not JSON: %v\n%s", err, methods.body)
	}
	if !discovery.Signup.Password.Enabled || !discovery.Signup.Enabled {
		t.Fatalf("the e2e configuration does not advertise name-and-password registration: %s", methods.body)
	}

	registered := send(t, client, http.MethodPost, base+"/api/v1/auth/register", map[string]string{
		"username": e2eSignupUsername,
		"email":    e2eSignupEmail,
		"password": e2eSignupPassword,
	}, nil)
	if registered.status != http.StatusCreated {
		t.Fatalf("registering answered %d:\n%s", registered.status, registered.body)
	}
	var created registrationAnswer
	if err := json.Unmarshal(registered.body, &created); err != nil {
		t.Fatalf("the registration answer is not JSON: %v\n%s", err, registered.body)
	}
	if created.State != "registered" || created.AccountID == "" {
		t.Errorf("the registration answered %+v, want a registered account", created)
	}
	if created.Verification.State != "pending" || created.Verification.Email != e2eSignupEmail {
		t.Errorf("the registration reports verification %+v, want pending for %q",
			created.Verification, e2eSignupEmail)
	}
	if code := e2eMailCodeFor(serve, e2eSignupEmail); code != "" {
		t.Fatalf("registration sent a verification code %q to %s:\n%s",
			code, e2eSignupEmail, serve.output())
	}

	token := signIn(t, base, e2eSignupUsername, e2eSignupPassword)
	auth := map[string]string{"Authorization": "Bearer " + token}

	if state := readVerification(t, client, base, auth); state != "pending" {
		t.Fatalf("the profile reports %q before the address is proved, want pending", state)
	}

	personal := readPersonalTenant(t, client, base, auth)
	if personal.Name == "" || personal.Kind != string(tenant.KindPersonal) {
		t.Errorf("the account's tenant is %+v, want the personal tenant its creation wrote", personal)
	}
	if personal.Name != e2eSignupUsername {
		t.Errorf("the personal tenant's name is %q, want the account's username %q",
			personal.Name, e2eSignupUsername)
	}

	added := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers", map[string]string{
		"kind": "email", "value": e2eSignupEmail,
	}, auth)
	if added.status != http.StatusOK {
		t.Fatalf("adding the address answered %d:\n%s", added.status, added.body)
	}
	var addedAnswer struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(added.body, &addedAnswer); err != nil || addedAnswer.State != "pending" {
		t.Fatalf("adding the address answered %+v (%v), want pending:\n%s", addedAnswer, err, added.body)
	}
	code := e2eMailCode(t, serve)

	verified := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers/verify",
		map[string]string{"code": code}, auth)
	if verified.status != http.StatusOK {
		t.Fatalf("verifying the code answered %d:\n%s", verified.status, verified.body)
	}
	var verifiedAnswer struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(verified.body, &verifiedAnswer); err != nil || verifiedAnswer.State != "added" {
		t.Fatalf("verifying the code answered %+v (%v), want added:\n%s", verifiedAnswer, err, verified.body)
	}
	if state := readVerification(t, client, base, auth); state != "verified" {
		t.Fatalf("the profile reports %q after the code was accepted, want verified", state)
	}

	again := readPersonalTenant(t, client, base, auth)
	if again.Name != personal.Name || again.Kind != personal.Kind {
		t.Errorf("the tenant after verification is %+v, want the existing one %+v", again, personal)
	}
}

type personalTenantView struct {
	Name string
	Kind string
}

func readPersonalTenant(t *testing.T, client *http.Client, base string,
	headers map[string]string,
) personalTenantView {
	t.Helper()
	read := send(t, client, http.MethodGet, base+"/api/v1/auth/me/tenants", nil, headers)
	if read.status != http.StatusOK {
		t.Fatalf("GET the caller's tenants answered %d:\n%s", read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the tenant list is not JSON: %v\n%s", err, read.body)
	}
	for _, entry := range answer.Tenants {
		if entry.Kind == string(tenant.KindPersonal) {
			return personalTenantView{Name: entry.Name, Kind: entry.Kind}
		}
	}
	t.Fatalf("the caller's tenant list carries no personal tenant:\n%s", read.body)
	return personalTenantView{}
}

func readVerification(t *testing.T, client *http.Client, base string, auth map[string]string) string {
	t.Helper()
	res := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, auth)
	if res.status != http.StatusOK {
		t.Fatalf("reading the profile answered %d:\n%s", res.status, res.body)
	}
	var profile profileVerification
	if err := json.Unmarshal(res.body, &profile); err != nil {
		t.Fatalf("the profile is not JSON: %v\n%s", err, res.body)
	}
	return profile.Verification.State
}

func e2eMailCode(t *testing.T, p *process) string {
	t.Helper()
	const field = "verification_code="
	limit := startLimit()
	deadline := time.Now().Add(limit)
	for {
		for _, line := range strings.Split(p.output(), "\n") {
			if !strings.Contains(line, e2eMailLogMarker) {
				continue
			}
			at := strings.LastIndex(line, field)
			if at < 0 {
				continue
			}
			value, _, _ := strings.Cut(line[at+len(field):], " ")
			value = strings.Trim(value, `"`)
			if value != "" {
				return value
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no mail log record carrying a verification code within %s:\n%s",
				limit, p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func e2eMailCodeFor(p *process, address string) string {
	const codeField = "verification_code="
	for _, line := range strings.Split(p.output(), "\n") {
		if !strings.Contains(line, e2eMailLogMarker) {
			continue
		}
		if !strings.Contains(line, "to="+address) && !strings.Contains(line, `to="`+address+`"`) {
			continue
		}
		at := strings.LastIndex(line, codeField)
		if at < 0 {
			continue
		}
		value, _, _ := strings.Cut(line[at+len(codeField):], " ")
		if value = strings.Trim(value, `"`); value != "" {
			return value
		}
	}
	return ""
}

// TestE2ESixDigitRunReadsOnlyAnExactRun holds that only an exact six-digit run is read as a code.
func TestE2ESixDigitRunReadsOnlyAnExactRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, want string
	}{
		{"exactly six", "your code is 123456 now", "123456"},
		{"seven digits", "1234567", ""},
		{"seven then six", "1234567 and 654321", "654321"},
		{"none", "no digits here", ""},
		{"empty", "", ""},
		{"six at the end", "code: 987654", "987654"},
	} {
		if got := maillog.SixDigitRun(tc.text); got != tc.want {
			t.Errorf("%s: maillog.SixDigitRun(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}

// TestE2EVerificationCodeReadsTheFirstBodyCarryingOne holds that the plain text body is read first and
// a body with no code contributes nothing.
func TestE2EVerificationCodeReadsTheFirstBodyCarryingOne(t *testing.T) {
	t.Parallel()
	if got := maillog.VerificationCode("no digits", "the code is 246810"); got != "246810" {
		t.Errorf("verificationCode fell through to the HTML body as %q, want 246810", got)
	}
	if got := maillog.VerificationCode("no digits", "still none"); got != "" {
		t.Errorf("verificationCode over two bodies with no code = %q, want none", got)
	}
	if got := maillog.VerificationCode("1234567"); got != "" {
		t.Errorf("verificationCode took a seven-digit run as %q, want none", got)
	}
}
