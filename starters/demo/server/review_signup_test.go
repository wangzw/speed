//go:build review

package main

// The review build's name-and-password registration walkthrough: the baseline
// advertises no registration entry, the review build does, a registration sends
// no mail, the signed-in account adds its address, the code read out of the
// review mail log verifies it, and the profile reports registered and verified
// before the personal tenant is ensured.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reviewMailLogMarker is what the review mail carrier writes for each message;
// the case searches the child's output for it.
const reviewMailLogMarker = "review mail: a verification message was sent"

// The account the walkthrough registers. None of these is an existing seed row.
const (
	reviewSignupUsername = "reviewer1"
	reviewSignupPassword = "review-signup-password-1"
	reviewSignupEmail    = "reviewer1@example.test"
)

// signupDiscovery is the part of the methods document this case reads: whether
// the name-and-password register route is open.
type signupDiscovery struct {
	Signup struct {
		Enabled  bool `json:"enabled"`
		Password struct {
			Enabled bool `json:"enabled"`
		} `json:"password"`
	} `json:"signup"`
}

// profileVerification is the part of /auth/me this case reads: the registration
// state, and the address it reports.
type profileVerification struct {
	Verification struct {
		State string `json:"state"`
		Email string `json:"email"`
	} `json:"verification"`
}

// registrationAnswer is the register route's success answer.
type registrationAnswer struct {
	State        string `json:"state"`
	AccountID    string `json:"account_id"`
	Verification struct {
		State string `json:"state"`
		Email string `json:"email"`
	} `json:"verification"`
}

// TestReviewBaselineHasNoRegistrationEntry holds the baseline side: the demo
// binary built without the review tag serves the methods document with
// name-and-password registration closed, so the login page renders no register
// entry.
func TestReviewBaselineHasNoRegistrationEntry(t *testing.T) {
	ports := freePorts(t, listenerCount)
	config := writeConfigWith(t, ports, filepath.Join(t.TempDir(), "baseline.db"))
	if res := runCommand(t, config, "sqlite-memory", migrateCommand); res.err != nil {
		t.Fatalf("migrating the baseline: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)

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

// TestReviewPasswordSignupVerifiesItsEmailAndBuildsAPersonalTenant is the
// walkthrough itself: the review build has the register entry, registering sends
// no mail, the account signs in with the password, adding the address sends one
// code, the code read from the review mail log verifies the address, the profile
// turns verified, and the ensure creates the personal tenant.
func TestReviewPasswordSignupVerifiesItsEmailAndBuildsAPersonalTenant(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	config := "file://" + path
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: 10 * time.Second}

	// The review build advertises the register entry the baseline does not.
	methods := get(t, base+"/api/v1/auth/methods")
	if methods.status != http.StatusOK {
		t.Fatalf("reading the methods document answered %d:\n%s", methods.status, methods.body)
	}
	var discovery signupDiscovery
	if err := json.Unmarshal(methods.body, &discovery); err != nil {
		t.Fatalf("the methods document is not JSON: %v\n%s", err, methods.body)
	}
	if !discovery.Signup.Password.Enabled || !discovery.Signup.Enabled {
		t.Fatalf("the review build does not advertise name-and-password registration: %s", methods.body)
	}

	// Registration writes the account and leaves the email awaiting verification.
	// It sends no mail: the marker has not been written yet.
	registered := send(t, client, http.MethodPost, base+"/api/v1/auth/register", map[string]string{
		"username": reviewSignupUsername,
		"email":    reviewSignupEmail,
		"password": reviewSignupPassword,
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
	if created.Verification.State != "pending" || created.Verification.Email != reviewSignupEmail {
		t.Errorf("the registration reports verification %+v, want pending for %q",
			created.Verification, reviewSignupEmail)
	}
	if strings.Contains(serve.output(), reviewMailLogMarker) {
		t.Fatalf("registration sent a verification message:\n%s", serve.output())
	}

	// The account signs in with the password it registered with.
	token := signIn(t, base, reviewSignupUsername, reviewSignupPassword)
	auth := map[string]string{"Authorization": "Bearer " + token}

	if state := readVerification(t, client, base, auth); state != "pending" {
		t.Fatalf("the profile reports %q before the address is proved, want pending", state)
	}

	// The registration limit is in force before the address is proved: the
	// personal ensure answers 409 verification-required and writes no tenant. The
	// ensure after verification below answers created, which is only possible if
	// this refused one wrote nothing.
	refused := send(t, client, http.MethodPost, base+personalRoutePath, nil, auth)
	if refused.status != http.StatusConflict {
		t.Fatalf("the ensure before verification answered %d:\n%s", refused.status, refused.body)
	}
	var refusedProblem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(refused.body, &refusedProblem); err != nil {
		t.Fatalf("the refused ensure's answer is not JSON: %v\n%s", err, refused.body)
	}
	if want := personalProblemPrefix + verificationRequiredCode; refusedProblem.Type != want {
		t.Errorf("the refused ensure carries type %q, want %q", refusedProblem.Type, want)
	}

	// Adding the address sends the code; the review mail carrier writes it to the
	// process log.
	added := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers", map[string]string{
		"kind": "email", "value": reviewSignupEmail,
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
	code := reviewMailCode(t, serve)

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

	// The proved address lifts the registration limit, so the personal tenant can
	// be ensured.
	ensured := send(t, client, http.MethodPost, base+personalRoutePath, nil, auth)
	if ensured.status != http.StatusOK {
		t.Fatalf("the personal ensure answered %d:\n%s", ensured.status, ensured.body)
	}
	var personal struct {
		Tenant struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"tenant"`
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(ensured.body, &personal); err != nil {
		t.Fatalf("the ensure's answer is not JSON: %v\n%s", err, ensured.body)
	}
	if !personal.Created || personal.Tenant.ID == "" || personal.Tenant.Kind != personalKind {
		t.Errorf("the ensure answered %+v, want a created personal tenant", personal)
	}
}

// readVerification reads the profile's registration state.
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

// reviewMailCode polls the child's output for the review mail record and answers
// the verification code it carries. The process writes logfmt, so the value is
// read off the record's last attribute rather than parsed as JSON.
func reviewMailCode(t *testing.T, p *process) string {
	t.Helper()
	const field = "verification_code="
	deadline := time.Now().Add(startLimit)
	for {
		for _, line := range strings.Split(p.output(), "\n") {
			if !strings.Contains(line, reviewMailLogMarker) {
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
			t.Fatalf("no review mail record carrying a verification code within %s:\n%s",
				startLimit, p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestReviewSixDigitRunReadsOnlyAnExactRun holds the shape the reviewer's code has:
// exactly six digits, so a body with none reports nothing and a longer run is
// not taken for a code.
func TestReviewSixDigitRunReadsOnlyAnExactRun(t *testing.T) {
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
		if got := sixDigitRun(tc.text); got != tc.want {
			t.Errorf("%s: sixDigitRun(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}

// TestReviewVerificationCodeReadsTheFirstBodyCarryingOne holds the body order: the
// plain text is read first, and a body with no exact six-digit run contributes
// nothing, so a message with no code reports none.
func TestReviewVerificationCodeReadsTheFirstBodyCarryingOne(t *testing.T) {
	if got := verificationCode("no digits", "the code is 246810"); got != "246810" {
		t.Errorf("verificationCode fell through to the HTML body as %q, want 246810", got)
	}
	if got := verificationCode("no digits", "still none"); got != "" {
		t.Errorf("verificationCode over two bodies with no code = %q, want none", got)
	}
	if got := verificationCode("1234567"); got != "" {
		t.Errorf("verificationCode took a seven-digit run as %q, want none", got)
	}
}
