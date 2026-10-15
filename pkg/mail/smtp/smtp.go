// Package smtp delivers the mail sending seam over a generic SMTP relay.
//
// It is a carrier module and nothing else: it turns a rendered message into one
// SMTP transaction — one connection, MAIL FROM, RCPT TO, DATA — calls the relay
// once and classifies what comes back into the sending sentinels. It holds no
// template, no queue and no retry, and it knows no vendor beyond the plain
// submission protocol, so the same carrier reaches Aliyun DirectMail, Tencent
// SES and any other relay through the host and port it is configured with.
//
// A host takes it in by writing ModuleName into a command. It delivers
// mail.Sender exclusively, so a command names exactly one carrier and two at
// once fail in resolution. The whole leaf package binds one SMTP client library
// and can be dropped without touching any other module.
//
// The credential is registered as a literal redaction rule; the subject, the
// bodies, the addresses and the credential reach no log, no error text and no
// metric.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	netmail "net/mail"
	"strings"

	gomail "github.com/wneessen/go-mail"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dedupe"
	"github.com/vislake/speed/pkg/mail"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "mail.smtp"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// carrierName is the name this sender reports. It is a constant of the code: the
// assembly names one carrier on the command tree and reads the name back to say
// which one ran.
const carrierName = "smtp"

// init registers this module with the process registry, so a host that imports
// the package has the carrier available to name on a command tree.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The mail.Sender claim is exclusive: only one carrier may be in a run, so the
// resolver fails a run that holds both. The stance reads the configuration, so a
// section this module cannot run on — a tls value outside the set, a username
// with no password, a sender that does not parse — is a startup failure ahead of
// every send. The logging capability is taken up as an optional requirement,
// because the AUTH secret is registered as a literal redaction rule and an
// assembly with no logging has nothing to register it with.
func Module() core.Module {
	return mail.NewModule(mail.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

// stance states that the module runs once its configuration is sound: a carrier
// named on a command tree is taken as running. Reading the configuration here
// makes an unsound section a startup failure, one stage ahead of construction.
func stance(cfg *moduleConfig) (core.Enablement, error) {
	if _, err := cfg.resolve(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the AUTH secret, which every send carries.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.Password}
}

// open builds the product over a carrier client.
func open(_ context.Context, cfg *moduleConfig) (mail.Sender, error) {
	conf, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	client, err := newClient(conf)
	if err != nil {
		return nil, err
	}
	return newSender(conf, client), nil
}

// carrier is the one operation this module calls on the vendor client. It is an
// interface rather than the client itself so that no vendor type appears on this
// package's surface.
type carrier interface {
	DialAndSendWithContext(ctx context.Context, msgs ...*gomail.Msg) error
}

// newClient builds the vendor client for one connection posture. No retry is
// configured: the SMTP transaction is not idempotent and a second one would
// deliver and bill a second message.
func newClient(conf resolved) (*gomail.Client, error) {
	tlsConfig := &tls.Config{
		ServerName: conf.host,
		MinVersion: tls.VersionTLS12,
	}
	if conf.tlsSkipVerify {
		// A private relay may present a certificate no public root signs. This
		// is opt-in through tls-skip-verify and off unless the host asks for it.
		tlsConfig.InsecureSkipVerify = true
	}

	opts := []gomail.Option{
		gomail.WithPort(conf.port),
		gomail.WithTLSPolicy(policyFor(conf.tls)),
		gomail.WithTLSConfig(tlsConfig),
	}
	if conf.heloName != "" {
		opts = append(opts, gomail.WithHELO(conf.heloName))
	}
	if conf.username != "" {
		opts = append(opts,
			gomail.WithUsername(conf.username),
			gomail.WithPassword(conf.password),
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
		)
	}
	if conf.tls == tlsImplicit {
		opts = append(opts, gomail.WithSSL())
	}

	client, err := gomail.NewClient(conf.host, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: building the carrier client: %w", ModuleName, err)
	}
	return client, nil
}

// policyFor is the TLS policy for one posture. The implicit posture encrypts
// before the first command, so the policy only matters for the two that start in
// the clear.
func policyFor(mode string) gomail.TLSPolicy {
	switch mode {
	case tlsNone:
		return gomail.NoTLS
	default:
		// starttls (and implicit, where the policy is unused) require the
		// encrypted session and fail rather than fall back to plaintext.
		return gomail.TLSMandatory
	}
}

// sender is this module's product.
type sender struct {
	client carrier
	// from is the default sender in its RFC 5322 form.
	from string
	// allowed holds every accepted sender as a lower-cased addr-spec.
	allowed map[string]bool
	// dupe remembers the sends this instance has made, keyed by the message's
	// stable identifier, so the non-idempotent relay is reached at most once per
	// identifier.
	dupe *dedupe.Table[mail.Receipt]
}

// sender must satisfy the seam it delivers.
var _ mail.Sender = (*sender)(nil)

// newSender builds the product over a carrier client.
func newSender(conf resolved, client carrier) *sender {
	return &sender{
		client:  client,
		from:    conf.from,
		allowed: conf.allowed,
		dupe:    dedupe.New[mail.Receipt](dedupe.DefaultCapacity),
	}
}

// Name reports the carrier this sender is, as the command tree named it.
func (s *sender) Name() string { return carrierName }

// Send hands one message to the relay, at most once per stable identifier.
//
// The message is judged before the carrier is reached: a message the package
// refuses and a sender that is not among the configured ones are both caller
// defects and both definite rejections. A message whose identifier was seen
// before is not sent again — the caller is answered with the earlier call's
// outcome — so the non-idempotent relay is reached at most once per identifier.
func (s *sender) Send(ctx context.Context, m mail.Message) (mail.Receipt, error) {
	if err := m.Validate(); err != nil {
		return mail.Receipt{}, err
	}
	from, err := s.senderFor(m.From)
	if err != nil {
		return mail.Receipt{}, err
	}
	if m.DedupeID == "" {
		// No identifier to guard by, so the message is delivered directly.
		return s.deliver(ctx, m, from)
	}
	entry, first := s.dupe.Begin(m.DedupeID)
	if !first {
		return entry.Wait()
	}
	receipt, err := s.deliver(ctx, m, from)
	s.dupe.Finish(entry, receipt, err)
	return receipt, err
}

// senderFor resolves the message's sender against the configured set and refuses
// one the set does not hold.
//
// An empty message sender takes the configured default; anything else is one the
// caller named and has to be in the set of {from} and the additional allowed
// senders. The set is compared on the addr-spec alone, so a display name does
// not change whether a sender is accepted.
func (s *sender) senderFor(from string) (string, error) {
	if from == "" {
		from = s.from
	}
	addr, err := netmail.ParseAddress(from)
	if err != nil {
		return "", fmt.Errorf("%w: %s: the sender is not a single RFC 5322 address",
			mail.ErrRejected, carrierName)
	}
	if !s.allowed[strings.ToLower(addr.Address)] {
		return "", fmt.Errorf("%w: %s: the sender is not among the configured senders",
			mail.ErrRejected, carrierName)
	}
	return from, nil
}

// deliver assembles the message and hands it to the relay in one call.
//
// The call carries the caller's context and opens its own connection, so each
// send is one connection. Nothing here is retried: the SMTP transaction is not
// idempotent, and a second one would deliver and bill the message twice.
func (s *sender) deliver(ctx context.Context, m mail.Message, from string) (mail.Receipt, error) {
	msg, err := assemble(m, from)
	if err != nil {
		return mail.Receipt{}, err
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return mail.Receipt{}, classify(err)
	}
	return mail.Receipt{ProviderID: msg.GetMessageID()}, nil
}
