// Package smtp delivers the mail sending seam over a generic SMTP relay. It
// turns a rendered message into one SMTP transaction, calls the relay once and
// classifies the answer; the credential is registered as a literal redaction
// rule.
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

const carrierName = "smtp"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. The mail.Sender claim is exclusive,
// and the stance reads the configuration so a bad section fails startup.
func Module() core.Module {
	return mail.NewModule(mail.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		CarrierName:     carrierName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

// stance judges whether this carrier is configured to run: a wholly unset section
// stands down, and a half-configured one is an error naming the missing items.
func stance(cfg *moduleConfig) (core.Enablement, error) {
	if cfg.Host == "" && cfg.From == "" && cfg.Username == "" && cfg.Password == "" {
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: ModuleName + " has no configuration: no relay host, no sender and no credential",
		}, nil
	}
	var missing []string
	if cfg.Host == "" {
		missing = append(missing, hostItem)
	}
	if cfg.From == "" {
		missing = append(missing, fromItem)
	}
	if len(missing) > 0 {
		return core.Enablement{}, fmt.Errorf("%w: %s is partially configured; %s missing",
			mail.ErrInvalidConfig, ConfigNamespace, strings.Join(missing, ", "))
	}
	if _, err := cfg.resolve(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func secrets(cfg *moduleConfig) []string {
	return []string{cfg.Password}
}

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

type carrier interface {
	DialAndSendWithContext(ctx context.Context, msgs ...*gomail.Msg) error
}

func newClient(conf resolved) (*gomail.Client, error) {
	tlsConfig := &tls.Config{
		ServerName: conf.host,
		MinVersion: tls.VersionTLS12,
	}
	if conf.tlsSkipVerify {
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

func policyFor(mode string) gomail.TLSPolicy {
	switch mode {
	case tlsNone:
		return gomail.NoTLS
	default:
		return gomail.TLSMandatory
	}
}

type sender struct {
	client  carrier
	from    string
	allowed map[string]bool
	dupe    *dedupe.Table[mail.Receipt]
}

var _ mail.Sender = (*sender)(nil)

func newSender(conf resolved, client carrier) *sender {
	return &sender{
		client:  client,
		from:    conf.from,
		allowed: conf.allowed,
		dupe:    dedupe.New[mail.Receipt](dedupe.DefaultCapacity),
	}
}

func (s *sender) Name() string { return carrierName }

// Send hands one message to the relay, at most once per stable identifier. A
// message the package refuses and a sender outside the configured set are both
// rejections.
func (s *sender) Send(ctx context.Context, m mail.Message) (mail.Receipt, error) {
	if err := m.Validate(); err != nil {
		return mail.Receipt{}, err
	}
	from, err := s.senderFor(m.From)
	if err != nil {
		return mail.Receipt{}, err
	}
	if m.DedupeID == "" {
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

// deliver assembles the message and hands it to the relay in one call. Nothing
// here is retried: the SMTP transaction is not idempotent.
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
