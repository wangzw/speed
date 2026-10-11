package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A token is a secret and reaches no log line, no event payload and no error
// text: the log keeps only the digest, and the revocation event carries the
// public identifier and the reason class.
func TestNoTokenReachesTheLogsOrTheEvents(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	ctx := context.Background()

	issued := f.create(newBrowser(alice))
	f.verify(issued.Token)
	noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonAdmin))
	rotated := f.create(newBrowser(alice))
	f.verify(rotated.Token)

	if logs := f.logs.String(); strings.Contains(logs, issued.Token) || strings.Contains(logs, rotated.Token) {
		t.Errorf("a token reached the log:\n%s", logs)
	}
	for _, e := range f.bus.events {
		if got := fmt.Sprint(e.Payload); strings.Contains(got, issued.Token) || strings.Contains(got, rotated.Token) {
			t.Errorf("a token reached the %s payload: %s", e.Type, got)
		}
	}
}

// The metric name and the values of its one attribute are fixed, because a
// dashboard reads them.
func TestTheOperationNamesAreTheFixedSet(t *testing.T) {
	if operationsInstrument != "authn.session.operations" || operationAttribute != "operation" {
		t.Errorf("instrument %q on attribute %q, want authn.session.operations on operation",
			operationsInstrument, operationAttribute)
	}
	want := map[string]string{
		opCreated: "created", opElevated: "elevated", opRevoked: "revoked",
		opRevokedAll: "revoked-all", opPurged: "purged",
	}
	for got, name := range want {
		if got != name {
			t.Errorf("the operation attribute value %q, want %q", got, name)
		}
	}
}
