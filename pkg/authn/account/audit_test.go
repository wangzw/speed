package account

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A secret never reaches a statement, so it never reaches a statement-level audit row.
func TestNoSecretReachesAStatement(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		audit := h.withAudit()
		const password = "correct horse battery"
		rec := h.recordSQL()

		id := h.mustChangeAccount("secret-keeper", password)
		if err := h.p.Set(ctx, id, "another good phrase", authn.SetPasswordOptions{}); err != nil {
			t.Fatalf("setting a password: %v", err)
		}
		service := h.service("robot")
		_, secret, err := h.p.CreateServiceSecret(ctx, service, "ci", nil)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}

		statements := rec.take()
		rawCredential := false
		for _, statement := range statements {
			if strings.Contains(statement, password) || strings.Contains(statement, "another good phrase") ||
				strings.Contains(statement, secret) {
				t.Errorf("a statement carries a secret in the clear: %s", statement)
			}
			if strings.HasPrefix(statement, "INSERT INTO "+tableCredentials) ||
				strings.HasPrefix(statement, "INSERT INTO "+tableServiceKeys) {
				rawCredential = true
			}
		}
		if !rawCredential {
			t.Error("the credential and the secret were not written by a raw statement, so a model-path audit could see them")
		}
		for _, entry := range audit.written() {
			if strings.Contains(entry.Action+entry.Object+entry.Outcome, secret) {
				t.Errorf("an audit entry carries a secret: %+v", entry)
			}
		}
	})
}
