package pow

import (
	"time"

	"github.com/vislake/speed/pkg/settings"
)

type settingsOptions struct {
	Difficulty       int           `config:"difficulty"`
	ChallengeTTL     time.Duration `config:"challenge-ttl"`
	GlobalIssueLimit int           `config:"global-issue-limit"`
}

var settingDefaults = settingsOptions{
	Difficulty:       DefaultDifficulty,
	ChallengeTTL:     DefaultChallengeTTL,
	GlobalIssueLimit: DefaultGlobalIssueLimit,
}

func difficultyInDomain(value int) bool {
	return value >= DifficultyMin && value <= DifficultyMax
}

func challengeTTLInDomain(value time.Duration) bool {
	return value >= ChallengeTTLMin && value <= ChallengeTTLMax
}

func globalIssueLimitInDomain(value int) bool {
	return value >= GlobalIssueLimitMin && value <= GlobalIssueLimitMax
}

// Schema declares the items this provider reads, under the module's own
// subsection of the authentication namespace.
func Schema() settings.Schema {
	return settings.Schema{
		Namespace: Namespace,
		Mounts:    []settings.Mount{{Value: &settingDefaults}},
		Items: map[string]settings.Item{
			DifficultyKey: {
				Description: "the number of leading zero bits a proof-of-work hash must carry; " +
					"higher values take exponentially more work",
				Domain: settings.DomainOf("integers from 18 to 22", difficultyInDomain),
			},
			ChallengeTTLKey: {
				Description: "how long an issued proof-of-work challenge stays valid, which bounds the " +
					"time a client has to solve it",
				Domain: settings.DomainOf("durations from 1m to 15m", challengeTTLInDomain),
			},
			GlobalIssueLimitKey: {
				Description: "how many proof-of-work challenges all sources together may be issued in " +
					"one one-minute window; a set of source addresses cannot multiply the total work " +
					"past this bound",
				Domain: settings.DomainOf("integers from 1 to 1000000", globalIssueLimitInDomain),
			},
		},
	}
}

const (
	difficultyPath       = Namespace + "." + DifficultyKey
	challengeTTLPath     = Namespace + "." + ChallengeTTLKey
	globalIssueLimitPath = Namespace + "." + GlobalIssueLimitKey
)

func (p *Provider) difficulty() int {
	if p.settings == nil {
		return settingDefaults.Difficulty
	}
	var value int
	if err := p.settings.Get(settings.Global(), difficultyPath, &value); err != nil {
		return settingDefaults.Difficulty
	}
	if !difficultyInDomain(value) {
		return settingDefaults.Difficulty
	}
	return value
}

func (p *Provider) challengeTTL() time.Duration {
	if p.settings == nil {
		return settingDefaults.ChallengeTTL
	}
	var value time.Duration
	if err := p.settings.Get(settings.Global(), challengeTTLPath, &value); err != nil {
		return settingDefaults.ChallengeTTL
	}
	if !challengeTTLInDomain(value) {
		return settingDefaults.ChallengeTTL
	}
	return value
}

func (p *Provider) globalIssueLimit() int {
	if p.settings == nil {
		return settingDefaults.GlobalIssueLimit
	}
	var value int
	if err := p.settings.Get(settings.Global(), globalIssueLimitPath, &value); err != nil {
		return settingDefaults.GlobalIssueLimit
	}
	if !globalIssueLimitInDomain(value) {
		return settingDefaults.GlobalIssueLimit
	}
	return value
}
