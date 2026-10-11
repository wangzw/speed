package account

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// A secret carries the prefix and 256 bits, and only its digest is kept: what the
// table holds is the SHA-256 in hex, never the secret.
func TestASecretCarriesItsPrefixAndOnlyItsDigestIsKept(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		secretID, secret, err := h.p.CreateServiceSecret(ctx, service, "ci", nil)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		if !strings.HasPrefix(secret, "spd_cs_") {
			t.Errorf("the secret %q does not carry the prefix spd_cs_", secret)
		}
		var row struct{ Hash string }
		if err = h.handle.Raw("SELECT secret_hash AS hash FROM "+tableServiceKeys+" WHERE id = ?", secretID).Scan(&row).Error; err != nil {
			t.Fatalf("reading the row: %v", err)
		}
		if row.Hash != secretDigest(secret) || strings.Contains(row.Hash, secret) {
			t.Errorf("the table holds %q, want the digest %q and not the secret", row.Hash, secretDigest(secret))
		}
		// The listing carries the identifier, the label and the times, never the
		// secret.
		infos, err := h.p.ListServiceSecrets(ctx, service)
		if err != nil || len(infos) != 1 || infos[0].ID != secretID || infos[0].Label != "ci" {
			t.Errorf("ListServiceSecrets gave %+v (%v), want the one secret's identifier and label", infos, err)
		}
	})
}

// A presented secret is found by its digest and verified against the account, the
// secret identifier and the expiry.
func TestAVerifiedSecretReturnsTheAccountTheSecretAndTheExpiry(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		expiry := h.clock.read().Add(time.Hour)
		secretID, secret, err := h.p.CreateServiceSecret(ctx, service, "ci", &expiry)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		account, gotSecret, gotExpiry, err := h.p.VerifyServiceSecret(ctx, service.String(), secret)
		if err != nil || account != service || gotSecret != secretID || !utc(gotExpiry).Equal(utc(expiry)) {
			t.Errorf("VerifyServiceSecret gave %q, %q, %v, %v; want the account, the identifier and the expiry", account, gotSecret, gotExpiry, err)
		}
		// A verify stamps the time of last use.
		var row secretRow
		if err := h.handle.Where("id = ?", secretID).Take(&row).Error; err != nil {
			t.Fatalf("reading the row: %v", err)
		}
		if row.LastUsedAt == nil {
			t.Error("a successful verify did not stamp the time of last use")
		}
	})
}

// A revoked secret, an expired secret and a wrong client identifier are all
// refused with ErrInvalidCredentials.
func TestARefusedSecretIsInvalidCredentials(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		other := h.service("other")

		secretID, secret, err := h.p.CreateServiceSecret(ctx, service, "ci", nil)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		for _, c := range []struct {
			name     string
			clientID string
			secret   string
		}{
			{"a wrong client identifier", other.String(), secret},
			{"a wrong secret", service.String(), "spd_cs_not-a-real-secret"},
			{"a secret without the prefix", service.String(), "naked-secret"},
		} {
			if _, _, _, err = h.p.VerifyServiceSecret(ctx, c.clientID, c.secret); !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Errorf("%s gave %v, want ErrInvalidCredentials", c.name, err)
			}
		}
		// Revoked.
		if err = h.p.RevokeServiceSecret(ctx, service, secretID); err != nil {
			t.Fatalf("revoking: %v", err)
		}
		if _, _, _, err = h.p.VerifyServiceSecret(ctx, service.String(), secret); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a revoked secret gave %v, want ErrInvalidCredentials", err)
		}

		// Expired.
		expiry := h.clock.read().Add(time.Hour)
		_, shortLived, err := h.p.CreateServiceSecret(ctx, service, "short", &expiry)
		if err != nil {
			t.Fatalf("making a short-lived secret: %v", err)
		}
		h.clock.advance(2 * time.Hour)
		if _, _, _, err := h.p.VerifyServiceSecret(ctx, service.String(), shortLived); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("an expired secret gave %v, want ErrInvalidCredentials", err)
		}
	})
}

// Only a service account has secrets: a user account is refused.
func TestOnlyAServiceAccountHasSecrets(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		user := h.user("person", "correct horse battery")
		if _, _, err := h.p.CreateServiceSecret(ctx, user, "ci", nil); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("making a secret for a user gave %v, want ErrInvalidArgument", err)
		}
	})
}

// An expiry that is not after now is refused when the secret is made.
func TestASecretExpiryMustBeInTheFuture(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		past := h.clock.read().Add(-time.Minute)
		if _, _, err := h.p.CreateServiceSecret(ctx, service, "ci", &past); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("an expiry in the past gave %v, want ErrInvalidArgument", err)
		}
	})
}
