package registry

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

const (
	templatesItem      = "templates"
	pollItem           = "poll-interval"
	sendingEnabledItem = "sending-enabled"
)

var sendingEnabledPath = ConfigNamespace + "." + sendingEnabledItem

const defaultPollInterval = time.Minute

var configDefaults = moduleConfig{
	PollInterval: defaultPollInterval,
}

type settingDefaults struct {
	SendingEnabled bool `config:"sending-enabled"`
}

var settingDefaultValues = settingDefaults{SendingEnabled: true}

type moduleConfig struct {
	Templates map[string]sms.TemplateSpec `config:"templates"`
	// Zero accepts convergence through change events alone.
	PollInterval time.Duration `config:"poll-interval"`
}

type moduleSettings struct {
	declaration sms.Declaration
	names       map[string]struct{}
	specs       map[string]sms.TemplateSpec
	poll        time.Duration
}

func (c moduleConfig) resolve() (moduleSettings, error) {
	if c.PollInterval < 0 {
		return moduleSettings{}, fmt.Errorf("%w: %s.%s is %s, and a poll interval cannot be negative. "+
			"Set it to zero to poll only when a template changes",
			sms.ErrInvalidConfig, ConfigNamespace, pollItem, c.PollInterval)
	}
	settings := moduleSettings{names: make(map[string]struct{}, len(c.Templates)), poll: c.PollInterval}
	if len(c.Templates) == 0 {
		return settings, nil
	}
	decl, err := sms.NewDeclaration(c.Templates)
	if err != nil {
		return moduleSettings{}, fmt.Errorf("%s.%s: %w", ConfigNamespace, templatesItem, err)
	}
	settings.specs = make(map[string]sms.TemplateSpec, len(c.Templates))
	for name, spec := range c.Templates {
		settings.names[name] = struct{}{}
		settings.specs[name] = spec
	}
	settings.declaration = decl
	return settings, nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			templatesItem: {
				Origins: config.OriginPrimary,
				Description: "the templates the operator wrote into the configuration and had the " +
					"vendor approve, keyed by the template name the caller sends under; each holds the " +
					"kind it is for, the ordered parameter names the approved template substitutes, the " +
					"carrier code and the locally rendered body for each language, and the vendor " +
					"registration identifiers it carries. These are read-only for the life of the process",
			},
			pollItem: {
				Description: "how often the runtime template set is reloaded and the vendor's review " +
					"state is read for the templates still pending, or zero to rely on change events alone",
			},
		},
	}
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &settingDefaultValues}},
		Items: map[string]settings.Item{
			sendingEnabledItem: {
				Description: "whether this registry may resolve a message for sending; false answers " +
					"every resolve with the not-sendable sentinel until it is turned back, without a " +
					"restart",
			},
		},
	}
}

func readSettings(reader config.Reader) (moduleSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleSettings{}, err
	}
	return cfg.resolve()
}

type settingsReader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store, logger *slog.Logger) *settingsReader {
	if logger == nil {
		logger = slog.Default()
	}
	return &settingsReader{store: store, logger: logger, reported: make(map[string]string)}
}

// A read that fails answers the declared default, true: this switch exists to stop
// sending on purpose, and a settings outage must not take the whole capability down.
func (r *settingsReader) sendingEnabled() bool {
	if r == nil || r.store == nil {
		return settingDefaultValues.SendingEnabled
	}
	enabled, err := settings.Get[bool](r.store, settings.Global(), sendingEnabledPath)
	if err != nil {
		r.report("the switch cannot be read, so its declared default is used", err.Error())
		return settingDefaultValues.SendingEnabled
	}
	r.report("", "")
	return enabled
}

func (r *settingsReader) report(problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[sendingEnabledPath]
	switch {
	case problem == "":
		delete(r.reported, sendingEnabledPath)
	case !reported || last != detail:
		r.reported[sendingEnabledPath] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", sendingEnabledPath))
	}
}

func normalizedKind(kind sms.Kind) sms.Kind {
	if kind == "" {
		return sms.KindNotification
	}
	return kind
}

func validateAgainstModel(name string, spec sms.TemplateSpec, model sms.CarrierModel, requireCodes bool) error {
	if !model.Content.TemplateCode && len(spec.Codes) > 0 {
		return fmt.Errorf("%w: the template %q declares vendor codes and this carrier does not send by "+
			"code", sms.ErrInvalidConfig, name)
	}
	for _, locale := range sortedKeys(spec.Codes) {
		if spec.Codes[locale] == "" {
			return fmt.Errorf("%w: the template %q has no vendor code for the language %q",
				sms.ErrInvalidConfig, name, locale)
		}
	}
	if requireCodes {
		for _, locale := range effectiveLocales(spec.Codes, spec.Bodies) {
			if spec.Codes[locale] == "" {
				return fmt.Errorf("%w: the template %q has no vendor code for the language %q, and this "+
					"carrier reviews every template per code", sms.ErrInvalidConfig, name, locale)
			}
		}
	}
	accepted := make(map[string]struct{}, len(model.AttributeKeys))
	for _, key := range model.AttributeKeys {
		accepted[key] = struct{}{}
	}
	for _, key := range sortedKeys(spec.Attributes) {
		if _, ok := accepted[key]; !ok {
			return fmt.Errorf("%w: the template %q carries the attribute %q, which this carrier does "+
				"not accept", sms.ErrInvalidConfig, name, key)
		}
	}
	if model.MaxBodyRunes > 0 {
		for _, locale := range sortedKeys(spec.Bodies) {
			if runes := utf8.RuneCountInString(spec.Bodies[locale]); runes > model.MaxBodyRunes {
				return fmt.Errorf("%w: the body of the template %q for the language %q is %d runes, over "+
					"this carrier's bound of %d", sms.ErrInvalidConfig, name, locale, runes, model.MaxBodyRunes)
			}
		}
	}
	return nil
}
