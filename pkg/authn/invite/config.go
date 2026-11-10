package invite

import (
	"math"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The items this module accepts, relative to its namespace, and the full path a
// failure names. They are settings, not configuration: the limits are read at
// each decision, so a deployment can widen or tighten them without a restart.
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

// DefaultTTL is the lifetime an invitation has when none is configured. The
// design fixes the key but not the number, so this is this module's declared
// default.
const DefaultTTL = 168 * time.Hour

// The declared defaults of the send and probe budgets. An issuance is one
// message the deployment pays for, so the address budget is deliberately small;
// the issuer budget leaves an operator room to invite a team; the deployment
// budget is the ceiling a mistaken or hostile operator cannot pass. A probe is
// one request against a token nobody has proved themselves for, so the token
// budget is small and the client bound is the coarse backstop a source needs
// before it has identified itself.
const (
	DefaultIssueWindow       = time.Hour
	DefaultIssueAddressLimit = 5
	DefaultIssueIssuerLimit  = 50
	DefaultIssueGlobalLimit  = 500

	DefaultProbeWindow      = time.Minute
	DefaultProbeTokenLimit  = 10
	DefaultProbeClientLimit = 30
)

// The domain of the configured lifetime: below an hour an invitation is a
// nuisance, and past a quarter the value stops meaning anything.
const (
	minTTL = time.Hour
	maxTTL = 2160 * time.Hour
)

// maxWindow is the longest counting window whose double still fits a
// time.Duration. A budget key is created with an expiry of two windows, and the
// primitives read an expiry of zero or less as "no expiry at all".
const maxWindow = time.Duration(math.MaxInt64) / 2

// options carries the settings this module owns and their declared defaults.
type options struct {
	// TTL is how long an invitation lives from the moment it is issued, when
	// the issue call states no deadline of its own.
	TTL time.Duration `config:"ttl"`

	IssueWindow       time.Duration `config:"issue-window"`
	IssueAddressLimit int           `config:"issue-address-limit"`
	IssueIssuerLimit  int           `config:"issue-issuer-limit"`
	IssueGlobalLimit  int           `config:"issue-global-limit"`

	ProbeWindow      time.Duration `config:"probe-window"`
	ProbeTokenLimit  int           `config:"probe-token-limit"`
	ProbeClientLimit int           `config:"probe-client-limit"`
}

// defaults is the prototype the settings module reads the declared defaults off.
// It never receives a value.
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

// ttlInDomain is the domain of the lifetime setting. A read is judged against it
// as well as a write, since a row written before the domain was declared, or
// written around the store, is served as it is.
func ttlInDomain(d time.Duration) bool { return d >= minTTL && d <= maxTTL }

// windowInDomain is the domain of a counting window: a window that is not
// positive leaves the bucket index undefined, and one past maxWindow overflows
// the doubled expiry.
func windowInDomain(d time.Duration) bool { return d >= time.Second && d <= maxWindow }

// limitInDomain is the domain of a budget: a limit of zero would refuse every
// request, since the increment happens before the decision, so the layer refuses
// to act on one. A budget an operator wants off is widened, not zeroed.
func limitInDomain(n int) bool { return n >= 1 }

// configSchema declares this module's config section. The section holds no
// input item: the lifetime that used to live here is a setting now, but the
// section stays declared so that a source still writing one of its keys is
// refused by that key's full path rather than by its parent.
func configSchema() config.Schema {
	return config.Schema{Namespace: ConfigNamespace}
}

// settingsSchema declares this module's runtime parameters. The namespace is this
// module's own name: two modules declaring one path is a startup failure even when
// only one of them runs.
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
