package captcha

import (
	"github.com/vislake/speed/pkg/settings"
)

type settingsOptions struct {
	GlobalIssueLimit int `config:"global-issue-limit"`
}

var settingDefaults = settingsOptions{
	GlobalIssueLimit: DefaultGlobalIssueLimit,
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
			GlobalIssueLimitKey: {
				Description: "how many captcha challenges all sources together may be issued in one " +
					"one-minute window; a set of source addresses cannot multiply the total work " +
					"past this bound",
				Domain: settings.DomainOf("integers from 1 to 1000000", globalIssueLimitInDomain),
			},
		},
	}
}

const globalIssueLimitPath = Namespace + "." + GlobalIssueLimitKey

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
