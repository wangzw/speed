package account

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
)

// mustChangeAccount creates a user account with a password that has to be
// changed at the next login.
func (h *harness) mustChangeAccount(name, plaintext string) authn.AccountID {
	h.t.Helper()
	created, err := h.p.Create(context.Background(), authn.CreateAccount{
		Username: name, Password: plaintext, MustChangePassword: true, Via: "admin",
	})
	if err != nil {
		h.t.Fatalf("creating %s: %v", name, err)
	}
	return created.ID
}

// legacyEntry is an entry in the form without a leading dollar sign, built from a
// PHC one so that the same password verifies against both.
func legacyEntry(t testing.TB, plaintext string, iterations int) string {
	t.Helper()
	phc, err := password.Params{Algorithm: password.PBKDF2SHA256, PBKDF2Iterations: iterations}.
		Hash(password.Normalize(plaintext))
	if err != nil {
		t.Fatalf("writing a pbkdf2 entry: %v", err)
	}
	parts := strings.Split(phc, "$")
	// parts[2] is "i=<iterations>,l=<length>".
	return "pbkdf2-sha256$" + strings.TrimPrefix(strings.Split(parts[2], ",")[0], "i=") + "$" + parts[3] + "$" + parts[4]
}

// A correct password for an enabled account returns the account, and a credential
// that has to be changed says so.
func TestVerifySucceedsAndReportsMustChange(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.mustChangeAccount("changed", "correct horse battery")
		got, result, err := h.p.Verify(ctx, "changed", "correct horse battery")
		if err != nil || got != id || !result.MustChange {
			t.Errorf("Verify gave %q, %+v, %v; want the account and must-change", got, result, err)
		}
	})
}

// An unknown name, a disabled account, an account with no password and a wrong
// password all answer the one sentinel, and each costs a derivation against the
// decoy, which is what keeps them from being told apart by anything but time.
func TestEnumerationAnswersTheOneSentinelAndDerivesAgainstTheDecoy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		meters := h.withMeters()
		known := h.user("known", "correct horse battery")
		noPassword := h.user("nopassword", "")

		before := meters.histogramCount(deriveInstrument, nil)
		for _, c := range []struct {
			name       string
			identifier string
			password   string
		}{
			{"an unknown name", "nobody", "whatever twelve"},
			{"a wrong password", "known", "wrong wrong wrong"},
			{"an account with no password", "nopassword", "whatever twelve"},
		} {
			if _, _, err := h.p.Verify(ctx, c.identifier, c.password); !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Errorf("%s gave %v, want ErrInvalidCredentials", c.name, err)
			}
		}
		// A disabled account answers the same sentinel.
		_ = h.p.SetStatus(ctx, known, authn.StatusDisabled, authn.ReasonAdmin)
		if _, _, err := h.p.Verify(ctx, "known", "correct horse battery"); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a disabled account gave %v, want ErrInvalidCredentials", err)
		}
		_ = noPassword
		if after := meters.histogramCount(deriveInstrument, nil); after <= before {
			t.Errorf("the derivations went from %d to %d, want each refused check to derive", before, after)
		}
	})
}

// The account-level back-off is silent: past its threshold even the right
// password under another spelling is refused with ErrInvalidCredentials and not a
// 429, so that a changed spelling does not tell an attacker the account exists.
// The identifier-level back-off is visible as a *ThrottledError.
func TestTheAccountThrottleIsSilentAndTheIdentifierThrottleIsVisible(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.user("alice", "correct horse battery")
		for i := 0; i <= defaults.ThrottleFreeAttempts; i++ {
			if _, _, err := h.p.Verify(ctx, "alice", "the wrong password"); !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Fatalf("failure %d gave %v, want ErrInvalidCredentials", i, err)
			}
		}
		// Another spelling of the same account's identifier: the account-level
		// back-off holds it, and the answer is the ordinary refusal, not a 429.
		_, _, err := h.p.Verify(ctx, "alice@example.com", "correct horse battery")
		if !errors.Is(err, authn.ErrInvalidCredentials) || errors.Is(err, authn.ErrThrottled) {
			t.Errorf("the right password under another spelling gave %v, want the silent ErrInvalidCredentials", err)
		}
		// The spelling that failed is held visibly, with a Retry-After.
		_, _, err = h.p.Verify(ctx, "alice", "correct horse battery")
		var throttled *authn.ThrottledError
		if !errors.As(err, &throttled) || throttled.RetryAfter <= 0 {
			t.Errorf("the failing spelling gave %v, want a *ThrottledError with a positive RetryAfter", err)
		}
	})
}

// Set judges a new password by the policy, normalizes it, and refuses one that is
// the account's own identifier in any of its forms.
func TestSetJudgesThePolicyAndNormalizes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("policed", "")
		for _, c := range []struct {
			name     string
			password string
			want     error
		}{
			{"too short", "short", authn.ErrPasswordPolicy},
			{"the username", "POLICED", authn.ErrPasswordPolicy},
			{"the email address", "policed@example.com", authn.ErrPasswordPolicy},
			{"a good one", "correct horse battery", nil},
		} {
			err := h.p.Set(ctx, id, c.password, authn.SetPasswordOptions{})
			if !errors.Is(err, c.want) {
				t.Errorf("%s: Set gave %v, want %v", c.name, err, c.want)
			}
		}
		// The stored password verifies after normalization of the input.
		if _, _, err := h.p.Verify(ctx, "policed", "correct horse battery"); err != nil {
			t.Errorf("the password just set does not verify: %v", err)
		}
	})
}

// A call that carries the current password checks it: a wrong one is
// ErrInvalidCredentials and is counted, and the password is set only when it is
// right and different.
func TestSetChecksTheCurrentPasswordWhenCarried(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("carrier", "correct horse battery")
		err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{Current: "the wrong one"})
		if !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Fatalf("a wrong current password gave %v, want ErrInvalidCredentials", err)
		}
		if _, _, verr := h.p.Verify(ctx, "carrier", "correct horse battery"); verr != nil {
			t.Errorf("the password changed on a wrong current one: %v", verr)
		}
		if err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{Current: "correct horse battery"}); err != nil {
			t.Fatalf("setting with the right current password: %v", err)
		}
		if _, _, verr := h.p.Verify(ctx, "carrier", "another good phrase"); verr != nil {
			t.Errorf("the new password does not verify: %v", verr)
		}
	})
}

// A stored entry that cannot be read, as one written around the module would be,
// is answered as a wrong password, reported without any of its text, and never
// derived from.
func TestACorruptStoredRowIsRefusedAndReportedWithoutItsText(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("corrupt", "")
		err := h.handle.Exec("INSERT INTO "+tableCredentials+" (account_id, phc, changed_at, must_change) VALUES (?, ?, ?, ?)",
			id.String(), "$argon2id$v=19$m=999999999,t=1,p=1$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA", h.clock.read(), false).Error
		if err != nil {
			t.Fatalf("writing the corrupt row: %v", err)
		}
		if _, _, err := h.p.Verify(ctx, "corrupt", "correct horse battery"); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a corrupt stored row gave %v, want ErrInvalidCredentials", err)
		}
		if lines := h.logs.lines("cannot be read"); len(lines) == 0 {
			t.Errorf("no Error reports the corrupt row: %s", h.logs.String())
		}
		if strings.Contains(h.logs.String(), "999999999") {
			t.Error("the report echoes the stored entry")
		}
	})
}

// ImportHash takes a legacy entry, writes the same bytes in the PHC shell, and
// the password verifies against it.
func TestImportHashTakesALegacyEntryAndRewritesIt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const plaintext = "legacy horse battery"
		legacy := legacyEntry(t, plaintext, 4096)
		id := h.user("legacy", "")
		if err := h.p.ImportHash(ctx, id, legacy, false); err != nil {
			t.Fatalf("importing a legacy entry: %v", err)
		}
		var row struct{ PHC string }
		if err := h.handle.Raw("SELECT phc FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Scan(&row).Error; err != nil {
			t.Fatalf("reading the credential: %v", err)
		}
		if !strings.HasPrefix(row.PHC, "$pbkdf2-sha256$") {
			t.Errorf("the imported entry is stored as %q, want the PHC shell", row.PHC)
		}
		if _, _, err := h.p.Verify(ctx, "legacy", plaintext); err != nil {
			t.Errorf("the imported password does not verify: %v", err)
		}
	})
}

// An entry that cannot be read is refused without any of its text.
func TestImportHashRefusesWhatItCannotRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("badimport", "")
		for _, entry := range []string{
			"$argon2id$v=19$m=999999999,t=1,p=1$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
			"not a hash at all",
			"$scrypt$nope",
		} {
			err := h.p.ImportHash(ctx, id, entry, false)
			if !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Errorf("importing %q gave %v, want ErrInvalidCredentials", entry, err)
			}
			if err != nil && strings.Contains(err.Error(), "argon2id$") {
				t.Errorf("the refusal echoes the entry: %v", err)
			}
		}
	})
}

// A check that holds rewrites a stored entry weaker than the current cost, and
// the rewrite is a compare-and-swap on the old entry, so two checks that both
// found it weak write it once.
func TestAWeakEntryIsRewrittenOnceUnderConcurrentChecks(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("weak", "")
		if err := h.p.ImportHash(ctx, id, cheapHash(t, "correct horse battery"), false); err != nil {
			t.Fatalf("importing a weak entry: %v", err)
		}
		var before struct{ PHC string }
		_ = h.handle.Raw("SELECT phc FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Scan(&before).Error

		meters := h.withMeters()
		const callers = 4
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, _, err := h.p.Verify(ctx, "weak", "correct horse battery"); err != nil {
					t.Errorf("a concurrent verify returned %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()

		var after struct{ PHC string }
		if err := h.handle.Raw("SELECT phc FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Scan(&after).Error; err != nil {
			t.Fatalf("reading the credential: %v", err)
		}
		if after.PHC == before.PHC {
			t.Error("a weak entry was not rewritten after a successful check")
		}
		if upgraded := meters.counter(verificationsInstrument, map[string]string{outcomeAttribute: outcomeUpgraded}); upgraded != 1 {
			t.Errorf("the upgrades are %d, want the compare-and-swap to write once", upgraded)
		}
	})
}

// Clear removes the credential, so that the account has none and has to set one.
func TestClearRemovesTheCredential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("cleared", "correct horse battery")
		if err := h.p.Clear(ctx, id); err != nil {
			t.Fatalf("clearing: %v", err)
		}
		if n := h.count(tableCredentials, "account_id = ?", id.String()); n != 0 {
			t.Errorf("%d credential row(s) left, want none", n)
		}
		if _, _, err := h.p.Verify(ctx, "cleared", "correct horse battery"); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("verifying a cleared account gave %v, want ErrInvalidCredentials", err)
		}
	})
}

// emailIdentifierID is the identifier ID of an account's email address, which a
// reset names in SetPasswordOptions.RequireIdentifier.
func emailIdentifierID(t *testing.T, h *harness, id authn.AccountID) string {
	t.Helper()
	identifiers, err := h.p.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("listing identifiers: %v", err)
	}
	for _, i := range identifiers {
		if i.Kind == authn.IdentifierEmail {
			return i.ID
		}
	}
	t.Fatalf("the account %s has no email identifier", id)
	return ""
}

// RequireActive re-judges the account inside the write transaction: a disabled
// account is ErrAccountUnavailable and keeps its password.
func TestSetRequireActiveRefusesADisabledAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("inactive", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{RequireActive: true})
		if !errors.Is(err, authn.ErrAccountUnavailable) {
			t.Errorf("Set gave %v, want ErrAccountUnavailable", err)
		}
		if err = h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		if _, _, verr := h.p.Verify(ctx, "inactive", "correct horse battery"); verr != nil {
			t.Errorf("the password changed on a refused write: %v", verr)
		}
	})
}

// RequireExisting keeps a reset from setting a first password: an account with no
// credential is ErrAccountUnavailable and no row is created.
func TestSetRequireExistingRefusesAnAccountWithoutACredential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("credentialless", "")
		err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{RequireExisting: true})
		if !errors.Is(err, authn.ErrAccountUnavailable) {
			t.Errorf("Set gave %v, want ErrAccountUnavailable", err)
		}
		if n := h.count(tableCredentials, "account_id = ?", id.String()); n != 0 {
			t.Errorf("%d credential row(s) were written, want none", n)
		}
		if err = h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{}); err != nil {
			t.Errorf("Set without the requirement: %v", err)
		}
	})
}

// RequireIdentifier re-reads the identifier inside the write transaction: one
// that belongs to another account, or to none, is ErrAccountUnavailable, and the
// account's own is accepted.
func TestSetRequireIdentifierRefusesAnIdentifierOfAnotherAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		owner := h.user("owner", "correct horse battery")
		other := h.user("other", "correct horse battery")
		emailID := emailIdentifierID(t, h, owner)
		for _, c := range []struct{ name, identifier string }{
			{"another account's identifier", emailID},
			{"an identifier that does not exist", "no-such-identifier"},
		} {
			err := h.p.Set(ctx, other, "another good phrase", authn.SetPasswordOptions{RequireIdentifier: c.identifier})
			if !errors.Is(err, authn.ErrAccountUnavailable) {
				t.Errorf("%s gave %v, want ErrAccountUnavailable", c.name, err)
			}
		}
		if _, _, err := h.p.Verify(ctx, "other", "correct horse battery"); err != nil {
			t.Errorf("the password changed on a refused write: %v", err)
		}
		if err := h.p.Set(ctx, owner, "another good phrase", authn.SetPasswordOptions{RequireIdentifier: emailID}); err != nil {
			t.Errorf("the account's own identifier gave %v", err)
		}
	})
}

// AdvanceValidAfter moves the instant in the write transaction and leaves the
// rest of the account alone, and the event names the reset and its channel.
func TestSetAdvancesValidAfterAndLeavesTheAccountAlone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("resetter", "correct horse battery")
		emailID := emailIdentifierID(t, h, id)
		var before accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&before).Error; err != nil {
			t.Fatalf("reading the account: %v", err)
		}
		at := h.clock.read()
		err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{
			By: "reset", Channel: "email", RequireActive: true, RequireExisting: true,
			RequireIdentifier: emailID, AdvanceValidAfter: true,
		})
		if err != nil {
			t.Fatalf("the reset write: %v", err)
		}
		var after accountRow
		if err = h.handle.Where("id = ?", id.String()).Take(&after).Error; err != nil {
			t.Fatalf("reading the account back: %v", err)
		}
		want := at.Truncate(time.Second).Add(time.Second)
		if !utc(after.ValidAfter).Equal(want) {
			t.Errorf("valid_after is %v, want %v", after.ValidAfter, want)
		}
		if after.Status != before.Status || after.SignupState != before.SignupState ||
			after.MFARequired != before.MFARequired || after.Protected != before.Protected ||
			after.DisplayName != before.DisplayName || after.Attributes != before.Attributes {
			t.Errorf("the reset changed the account beyond valid_after: before %+v, after %+v", before, after)
		}
		if n := h.count(tableCredentials, "account_id = ?", id.String()); n != 1 {
			t.Errorf("%d credential row(s), want one", n)
		}
		payload := bindingEvent[authn.PasswordChanged](t, h, authn.EventPasswordChanged)
		if payload.By != "reset" || payload.Channel != "email" {
			t.Errorf("the event is %+v, want by=reset channel=email", payload)
		}
	})
}

// By is inferred from the current password when it is not given, and a value
// outside the closed set is refused before anything is written.
func TestSetAttributesTheChangeAndRefusesAnUnknownAttribution(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("attributed", "correct horse battery")
		if err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{}); err != nil {
			t.Fatalf("an administrator's set: %v", err)
		}
		payload := bindingEvent[authn.PasswordChanged](t, h, authn.EventPasswordChanged)
		if payload.By != "admin" || payload.Channel != "" {
			t.Errorf("the event is %+v, want by=admin and no channel", payload)
		}
		h.bus.events = nil
		if err := h.p.Set(ctx, id, "a third good phrase", authn.SetPasswordOptions{Current: "another good phrase"}); err != nil {
			t.Fatalf("a self-service set: %v", err)
		}
		payload = bindingEvent[authn.PasswordChanged](t, h, authn.EventPasswordChanged)
		if payload.By != "self" {
			t.Errorf("the event is %+v, want by=self", payload)
		}
		h.bus.events = nil
		for _, c := range []struct {
			name string
			opt  authn.SetPasswordOptions
		}{
			{"an attribution outside Set's set", authn.SetPasswordOptions{By: "import"}},
			{"an unknown channel", authn.SetPasswordOptions{By: "reset", Channel: "carrier-pigeon"}},
			{"a reset with no channel", authn.SetPasswordOptions{By: "reset"}},
			{"a channel outside the reset path", authn.SetPasswordOptions{By: "admin", Channel: "email"}},
			{"a self change carrying a channel", authn.SetPasswordOptions{By: "self", Channel: "sms"}},
		} {
			if err := h.p.Set(ctx, id, "a fourth good phrase", c.opt); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("%s gave %v, want ErrInvalidArgument", c.name, err)
			}
		}
		if events := h.bus.of(authn.EventPasswordChanged); len(events) != 0 {
			t.Errorf("%d password-changed events on refused writes, want none", len(events))
		}
		if _, _, err := h.p.Verify(ctx, "attributed", "a third good phrase"); err != nil {
			t.Errorf("the refused writes changed the password: %v", err)
		}
	})
}

// TestSetRefusesAnAccountWithNoLoginName holds that a password needs a login name
// to authenticate: an account created without a username whose only identifier is
// a phone number, even one chosen as a sign-in way, is refused, and adding a
// username is what makes the same call succeed.
func TestSetRefusesAnAccountWithNoLoginName(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			Via: "import", Phone: "+8613812345678",
			PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
		})
		if err != nil {
			t.Fatalf("importing a phone-only account: %v", err)
		}
		phone := identifierOfKind(t, h, created.ID, authn.IdentifierPhone)
		if !phone.SignIn {
			t.Fatalf("the phone is %+v, want it chosen as a sign-in way", phone)
		}

		err = h.p.Set(ctx, created.ID, "correct horse battery", authn.SetPasswordOptions{})
		if !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Errorf("setting a password on an account with no login name gave %v, want ErrIdentifierRequired", err)
		}
		if n := h.count(tableCredentials, "account_id = ?", created.ID.String()); n != 0 {
			t.Errorf("%d credential row(s) were written, want none", n)
		}

		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierUsername, "phoneonly",
			authn.Verification{Via: authn.VerifiedViaSelf}); err != nil {
			t.Fatalf("adding a username: %v", err)
		}
		if err = h.p.Set(ctx, created.ID, "correct horse battery", authn.SetPasswordOptions{}); err != nil {
			t.Errorf("setting a password once a username is held gave %v, want none", err)
		}
	})
}

// TestSetRefusesAnAccountWhoseOnlyEmailIsNoSignInWay holds that a proved email
// kept as a contact alone is no login name either: an imported account whose
// source record did not choose it is refused a password until its holder does,
// and the choice is what makes the same call succeed.
func TestSetRefusesAnAccountWhoseOnlyEmailIsNoSignInWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			Via: "import", Email: "contactonly@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			EmailFlags:       authn.IdentifierFlags{Contact: true},
		})
		if err != nil {
			t.Fatalf("importing an account whose address is only a contact: %v", err)
		}
		email := identifierOfKind(t, h, created.ID, authn.IdentifierEmail)
		if email.SignIn || !email.Contact {
			t.Fatalf("the address is %+v, want a contact that is not a sign-in way", email)
		}

		err = h.p.Set(ctx, created.ID, "correct horse battery", authn.SetPasswordOptions{})
		if !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Errorf("setting a password on an account with no login name gave %v, want ErrIdentifierRequired", err)
		}
		if n := h.count(tableCredentials, "account_id = ?", created.ID.String()); n != 0 {
			t.Errorf("%d credential row(s) were written, want none", n)
		}

		if err = h.p.SetIdentifierFlags(ctx, created.ID, email.ID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the address as a sign-in way: %v", err)
		}
		if err = h.p.Set(ctx, created.ID, "correct horse battery", authn.SetPasswordOptions{}); err != nil {
			t.Errorf("setting a password once the address is a sign-in way gave %v, want none", err)
		}
	})
}

// TestVerifyAcceptsAnEmailOnlyWhenItIsChosenAsASignInWay holds that a proved
// email address is no login name until its holder chooses it: a password check
// against it answers as an unknown name does, the username beside it still
// works, and choosing the address makes the same check succeed. The choice is the
// only thing that changes; the address is proved either way.
func TestVerifyAcceptsAnEmailOnlyWhenItIsChosenAsASignInWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const password = "correct horse battery"
		id := h.user("mailer", password)
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "mailer@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}

		if _, _, err := h.p.Verify(ctx, "mailer@example.com", password); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a proved but unchosen address gave %v, want ErrInvalidCredentials", err)
		}
		if got, _, err := h.p.Verify(ctx, "mailer", password); err != nil || got != id {
			t.Errorf("the username beside it gave %q, %v, want the account and nil", got, err)
		}

		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID
		if err := h.p.SetIdentifierFlags(ctx, id, emailID,
			authn.IdentifierFlags{SignIn: true}); err != nil {
			t.Fatalf("choosing the address as a sign-in way: %v", err)
		}
		if got, _, err := h.p.Verify(ctx, "mailer@example.com", password); err != nil || got != id {
			t.Errorf("a chosen address gave %q, %v, want the account and nil", got, err)
		}
	})
}
