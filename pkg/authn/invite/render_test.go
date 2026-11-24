package invite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/invitemail"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/mail"
)

type renderedMail struct {
	messages []mail.Message
}

var _ mail.Sender = (*renderedMail)(nil)

func (s *renderedMail) Name() string { return "rendered" }

func (s *renderedMail) Send(_ context.Context, message mail.Message) (mail.Receipt, error) {
	s.messages = append(s.messages, message)
	return mail.Receipt{ProviderID: "rendered"}, nil
}

func renderingNotifier(t *testing.T, sender mail.Sender) authn.InviteNotifier {
	t.Helper()
	const body = `{"authn":{"invitemail":{"accept-base-url":"https://app.example.com"}}}`
	reader := authntest.ConfigReaderAll(t, body, invitemail.Module(), catalog.Module())

	reg := core.New()
	reg.Register(invitemail.Module())
	reg.Register(catalog.Module())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(core.Module{
		Name:     "stand-in.mail",
		Provides: []core.Provision{{Token: core.Token((*mail.Sender)(nil)), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return sender, nil },
	})

	var notifier authn.InviteNotifier
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageNew,
		Participants: []string{
			invitemail.ModuleName, catalog.ModuleName, config.ModuleName, "stand-in.mail",
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			var resolveErr error
			notifier, resolveErr = core.Resolve[authn.InviteNotifier](reg)
			return resolveErr
		},
	})
	if err != nil {
		t.Fatalf("assembling the invitation mail module: %v", err)
	}
	return notifier
}

// TestTheRenderedInvitationEmailCarriesTheNameNeverTheIdentifier checks that the
// rendered email names the tenant, never the identifier.
func TestTheRenderedInvitationEmailCarriesTheNameNeverTheIdentifier(t *testing.T) {
	h := newHarness(t)
	sender := &renderedMail{}
	h.p.notifier = renderingNotifier(t, sender)
	h.p.tenantNames = fakeNames{"tenant-a": "acme"}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteSent.String() {
		t.Fatalf("email_delivery = %q, want sent", got)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("the carrier received %d message(s), want one", len(sender.messages))
	}
	message := sender.messages[0]
	for name, body := range map[string]string{
		"subject": message.Subject,
		"text":    message.Text,
		"html":    message.HTML,
	} {
		if !strings.Contains(body, "acme") {
			t.Errorf("the %s does not carry the name acme: %s", name, body)
		}
		if strings.Contains(body, "tenant-a") {
			t.Errorf("the %s carries the internal identifier: %s", name, body)
		}
	}
}

// TestTheRenderedInvitationEmailNamesTheInviterAndTheDeadlineInUTC checks that
// the rendered email names the inviter and renders the deadline in UTC.
func TestTheRenderedInvitationEmailNamesTheInviterAndTheDeadlineInUTC(t *testing.T) {
	h := newHarness(t)
	sender := &renderedMail{}
	h.p.notifier = renderingNotifier(t, sender)
	h.p.tenantNames = fakeNames{"tenant-a": "acme"}
	h.accounts.named(adminID, "Demo Operator")

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteSent.String() {
		t.Fatalf("email_delivery = %q, want sent", got)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("the carrier received %d message(s), want one", len(sender.messages))
	}
	message := sender.messages[0]
	instant := answered.Invitation.ExpiresAt.Format(time.RFC3339)
	for name, body := range map[string]string{"text": message.Text, "html": message.HTML} {
		if !strings.Contains(body, "Demo Operator") {
			t.Errorf("the %s does not carry the inviter's display name: %s", name, body)
		}
		if strings.Contains(body, string(adminID)) {
			t.Errorf("the %s carries the inviter's account identifier: %s", name, body)
		}
		if !strings.Contains(body, "UTC") {
			t.Errorf("the %s does not mark the deadline as UTC: %s", name, body)
		}
		if strings.Contains(body, instant) {
			t.Errorf("the %s still carries the RFC 3339 deadline %q: %s", name, instant, body)
		}
	}
}
