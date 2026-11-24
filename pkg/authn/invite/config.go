package invite

import (
	"math"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

const (
	ttlItem = "ttl"
	ttlKey  = ConfigNamespace + "." + ttlItem

	issueWindowItem  = "issue-window"
	issueAddressItem = "issue-address-limit"
	issueIssuerItem  = "issue-issuer-limit"
	issueGlobalItem  = "issue-global-limit"

	probeWindowItem = "probe-window"
	probeTokenItem  = "probe-token-limit"
	probeClientItem = "probe-client-limit"

	issueWindowPath  = ConfigNamespace + "." + issueWindowItem
	issueAddressPath = ConfigNamespace + "." + issueAddressItem
	issueIssuerPath  = ConfigNamespace + "." + issueIssuerItem
	issueGlobalPath  = ConfigNamespace + "." + issueGlobalItem

	probeWindowPath = ConfigNamespace + "." + probeWindowItem
	probeTokenPath  = ConfigNamespace + "." + probeTokenItem
	probeClientPath = ConfigNamespace + "." + probeClientItem
)

// DefaultTTL is the lifetime an invitation has when no other lifetime is
// configured.
const DefaultTTL = 168 * time.Hour

// DefaultIssueWindow and the other Default* constants are the declared limits
// on issuance and probes.
const (
	DefaultIssueWindow       = time.Hour
	DefaultIssueAddressLimit = 5
	DefaultIssueIssuerLimit  = 50
	DefaultIssueGlobalLimit  = 500

	DefaultProbeWindow      = time.Minute
	DefaultProbeTokenLimit  = 10
	DefaultProbeClientLimit = 30
)

const (
	minTTL = time.Hour
	maxTTL = 2160 * time.Hour
)

// maxWindow is the longest counting window whose double still fits a
// time.Duration; a budget key expires after two windows.
const maxWindow = time.Duration(math.MaxInt64) / 2

type options struct {
	TTL time.Duration `config:"ttl"`

	IssueWindow       time.Duration `config:"issue-window"`
	IssueAddressLimit int           `config:"issue-address-limit"`
	IssueIssuerLimit  int           `config:"issue-issuer-limit"`
	IssueGlobalLimit  int           `config:"issue-global-limit"`

	ProbeWindow      time.Duration `config:"probe-window"`
	ProbeTokenLimit  int           `config:"probe-token-limit"`
	ProbeClientLimit int           `config:"probe-client-limit"`
}

var defaults = options{
	TTL: DefaultTTL,

	IssueWindow:       DefaultIssueWindow,
	IssueAddressLimit: DefaultIssueAddressLimit,
	IssueIssuerLimit:  DefaultIssueIssuerLimit,
	IssueGlobalLimit:  DefaultIssueGlobalLimit,

	ProbeWindow:      DefaultProbeWindow,
	ProbeTokenLimit:  DefaultProbeTokenLimit,
	ProbeClientLimit: DefaultProbeClientLimit,
}

func ttlInDomain(d time.Duration) bool { return d >= minTTL && d <= maxTTL }

func windowInDomain(d time.Duration) bool { return d >= time.Second && d <= maxWindow }

// limitInDomain refuses a limit below one: the increment happens before the
// comparison, so a zero limit would refuse every request.
func limitInDomain(n int) bool { return n >= 1 }

// configSchema declares an empty section so a source writing one of its keys is
// refused by the key's full path.
func configSchema() config.Schema {
	return config.Schema{Namespace: ConfigNamespace}
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			ttlItem: {
				Description: "how long an invitation lives from the moment it is issued, when the " +
					"issue call states no deadline of its own",
				Domain: settings.DomainOf("durations from "+minTTL.String()+" to "+maxTTL.String(), ttlInDomain),
			},
			issueWindowItem: {
				Description: "the fixed window an issuance budget counts over; a new window starts " +
					"from zero and a refused issuance still spends",
				Domain: settings.DomainOf("durations from 1s to "+maxWindow.String(), windowInDomain),
			},
			issueAddressItem: {
				Description: "the invitations one invited address may receive in one issuance window, " +
					"counted over the address's canonical inbox form; a resend is another issuance",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
			},
			issueIssuerItem: {
				Description: "the invitations one issuing account may send in one issuance window; a " +
					"host command with no HTTP identity spends only the address and deployment budgets",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
			},
			issueGlobalItem: {
				Description: "the invitations the whole deployment may send in one issuance window",
				Domain:      settings.DomainOf("the positive integers", limitInDomain),
			},
			probeWindowItem: {
				Description: "the fixed window an acceptance probe budget counts over",
				Domain:      settings.DomainOf("durations from 1s to "+maxWindow.String(), windowInDomain),
			},
			probeTokenItem: {
				Description: "the times one acceptance token may be presented in one probe window; " +
					"the count is keyed by the token's digest and never stores the token",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
			},
			probeClientItem: {
				Description: "the coarse bound on acceptance probes one client address may make in " +
					"one probe window, before the token budget is consulted",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
			},
		},
	}
}
