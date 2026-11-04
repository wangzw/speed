package pow

import (
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// settingsOptions is the carrier struct the settings module reads the declared
// defaults off. It never receives a value.
type settingsOptions struct {
	Difficulty       int           `config:"difficulty"`
	ChallengeTTL     time.Duration `config:"challenge-ttl"`
	GlobalIssueLimit int           `config:"global-issue-limit"`
}

// settingDefaults is what each item reads as when no settings store answers.
var settingDefaults = settingsOptions{
	Difficulty:       DefaultDifficulty,
	ChallengeTTL:     DefaultChallengeTTL,
	GlobalIssueLimit: DefaultGlobalIssueLimit,
}

// difficultyInDomain and challengeTTLInDomain are the declared ranges. A read
// outside one is served as the declared default, since a row stored before the
// domain was declared, or written around the store, is handed out as it is.
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

// The full key paths, as the settings store names them: the namespace the
// schema declares joined to the item's own key.
const (
	difficultyPath       = Namespace + "." + DifficultyKey
	challengeTTLPath     = Namespace + "." + ChallengeTTLKey
	globalIssueLimitPath = Namespace + "." + GlobalIssueLimitKey
)

// difficulty is the configured difficulty, or the declared default.
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

// challengeTTL is the configured lifetime, or the declared default.
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

// globalIssueLimit is the configured whole-deployment bound, or the declared
// default.
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
