package invite

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/invitemail"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/mail"
)

// renderedMail is a mail.Sender that keeps every message it was handed, so a
// case can read the body the invitation email actually rendered.
type renderedMail struct {
	messages []mail.Message
}

var _ mail.Sender = (*renderedMail)(nil)

func (s *renderedMail) Name() string { return "rendered" }

func (s *renderedMail) Send(_ context.Context, message mail.Message) (mail.Receipt, error) {
	s.messages = append(s.messages, message)
	return mail.Receipt{ProviderID: "rendered"}, nil
}

// renderingNotifier assembles the real authn.invitemail module over the mail
// seam, so a notice is rendered into the message a recipient would read rather
// than into a recorded notice.
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

// TestTheRenderedInvitationEmailCarriesTheNameNeverTheIdentifier holds the
// outbound rule at the rendering level: the body the recipient actually reads
// names the tenant by its name, and the internal identifier the path guard
// left in the issue request reaches neither the subject nor either body. The
// fixture's identifier and name differ, so a rendered message carrying either
// one is distinguishable.
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
