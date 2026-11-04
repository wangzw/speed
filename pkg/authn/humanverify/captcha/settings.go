package captcha

import (
	"github.com/vislake/speed/pkg/settings"
)

// settingsOptions is the carrier struct the settings module reads the declared
// defaults off. It never receives a value.
type settingsOptions struct {
	GlobalIssueLimit int `config:"global-issue-limit"`
}

// settingDefaults is what the item reads as when no settings store answers.
var settingDefaults = settingsOptions{
	GlobalIssueLimit: DefaultGlobalIssueLimit,
}

// globalIssueLimitInDomain is the declared range. A read outside it is served
// as the declared default, since a row stored before the domain was declared,
// or written around the store, is handed out as it is.
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

// The full key path, as the settings store names it: the namespace the schema
// declares joined to the item's own key.
const globalIssueLimitPath = Namespace + "." + GlobalIssueLimitKey

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
