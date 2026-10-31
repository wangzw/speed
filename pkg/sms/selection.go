package sms

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The SMS capability runs exactly one carrier at run time, chosen by the
// explicit selection item sms.carrier. Several carriers may be compiled into
// one binary; the selection is a run-time decision, not a build-time one.
const (
	// SelectionNamespace is the configuration section the selection item lives
	// under.
	SelectionNamespace = "sms"
	// SelectionItem is the item naming the carrier to run.
	SelectionItem = "carrier"
	// SelectionNone switches the capability off: no carrier runs, the log
	// fallback included, and the sending module, the template registry and the
	// management surface stand themselves down.
	SelectionNone = "none"
)

// The template registry keeps the templates a deployment writes in its own
// configuration section. Every consumer of the SMS capability judges from the
// same section whether the capability has anything to send, so the names are
// constants here beside the selection item.
const (
	// registryNamespace is the configuration section the template registry
	// module owns.
	registryNamespace = "sms.registry"
	// registryTemplatesItem is the item holding the configured template set.
	registryTemplatesItem = "templates"
)

// templatesConfig is the shape the registry's configured template set decodes
// into. The registry module reads the same section; this package names only the
// part that says whether any template was configured at all.
type templatesConfig struct {
	Templates map[string]TemplateSpec `config:"templates"`
}

// OffByMissingTemplates reports whether this run leaves the SMS capability with
// nothing to send: sms.carrier is unset, so the log fallback would carry the
// capability, and no template is configured, so the consumers that build
// messages have none.
//
// A module that depends on the SMS code state machine reads this in Prepare.
// The judgement is all-empty versus half-configured, the same split the carrier
// selection uses: with neither a selection nor a template the deployment did
// not configure SMS, so the module stands itself down with the reason instead
// of failing construction on a template it cannot find. An explicit selection
// is not judged here — a deployment that names a carrier is asking for SMS, and
// a required template it did not write stays that consumer's contract failure
// rather than a silent absence.
//
// An assembly with no configuration reader has no selection to read and no
// template section, so it reports not-off and lets the consumer's
// construction-time check name what is missing. That mirrors OffBySelection: a
// hand-built assembly supplies its capabilities directly and is not judged by a
// configuration it does not have.
func OffByMissingTemplates(reg *core.Registry) (bool, string, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	selected, err := ReadSelection(reader)
	if err != nil {
		return false, "", err
	}
	if selected != "" {
		return false, "", nil
	}
	var cfg templatesConfig
	if err := reader.Decode(registryNamespace, &cfg); err != nil {
		return false, "", err
	}
	if len(cfg.Templates) > 0 {
		return false, "", nil
	}
	return true, "sms.carrier is unset and sms.registry.templates declares no template, so the SMS " +
		"capability has no message to send", nil
}

// CarrierName is the resource a carrier module declares to state the name the
// selection item uses for it, and the name it answers Name with.
//
// The declaration is how a carrier's own Prepare can tell a selection that
// names a carrier this binary does not carry from one that names a compiled
// carrier that merely is not this one. It is a resource rather than a method
// because Prepare runs before any carrier is constructed.
type CarrierName struct {
	// Name is the carrier's name in the selection item, such as "aliyun" or
	// "log".
	Name string
}

// selectionConfig is the shape the selection item decodes into.
type selectionConfig struct {
	Carrier string `config:"carrier"`
}

// SelectionSchema is the input-item declaration the log carrier mounts, so a
// run can write sms.carrier. The log carrier declares it because it is
// compiled into every build; a binary carrying a vendor carrier without the
// log carrier accepts no selection and auto-selects the sole configured
// carrier.
func SelectionSchema() config.Schema {
	return config.Schema{
		Namespace: SelectionNamespace,
		Mounts:    []config.Mount{{Value: &selectionConfig{}}},
		Items: map[string]config.Item{
			SelectionItem: {
				Description: "the SMS carrier this run sends through: a compiled carrier's name, " +
					"or none to switch the SMS capability off",
			},
		},
	}
}

// ReadSelection reads sms.carrier. An absent item is the empty string, which
// asks for automatic selection: a configured carrier runs, and the log carrier
// is the fallback when none is.
func ReadSelection(reader config.Reader) (string, error) {
	var cfg selectionConfig
	if err := reader.Decode(SelectionNamespace, &cfg); err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Carrier), nil
}

// OffBySelection reports whether sms.carrier switches the capability off, and
// the reason a module that depends on the sending seam states when it does. A
// module that requires the seam takes this up in Prepare: with the capability
// off there may be no provider at all, and a dependent that tried to resolve it
// in New would fail the startup instead of standing itself down.
func OffBySelection(reg *core.Registry) (bool, string, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		// An assembly with no configuration reader has no selection to read, so
		// the capability is not switched off by one. The carriers still judge the
		// selection where they read their own configuration.
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	selected, err := ReadSelection(reader)
	if err != nil {
		return false, "", err
	}
	if selected == SelectionNone {
		return true, "sms.carrier is \"none\", so the SMS capability is off in this run", nil
	}
	return false, "", nil
}

// CarrierNames lists the carrier names this binary has compiled in, in name
// order.
func CarrierNames(reg *core.Registry) []string {
	declared := core.Resources[CarrierName](reg)
	names := make([]string, 0, len(declared))
	for _, carrier := range declared {
		name := strings.TrimSpace(carrier.Value.Name)
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// selectionFrom reads the selection and checks that a name it gives is one a
// compiled carrier answers to.
//
// An unknown name is an error rather than a run that quietly carries no
// carrier: a typo in sms.carrier would otherwise leave the capability off
// while every dependency stood down as though the deployment had chosen none.
func selectionFrom(reg *core.Registry, carrierName string) (string, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return "", fmt.Errorf("%s: reading the SMS carrier selection: %w", carrierName, err)
	}
	selected, err := ReadSelection(reader)
	if err != nil {
		return "", err
	}
	switch selected {
	case "", SelectionNone, carrierName:
		return selected, nil
	}
	names := CarrierNames(reg)
	if slices.Contains(names, selected) {
		return selected, nil
	}
	carried := "none"
	if len(names) > 0 {
		carried = strings.Join(names, ", ")
	}
	return "", fmt.Errorf("%w: sms.carrier names %q, which is not an SMS carrier compiled into this "+
		"program; this binary carries %s, and %q switches the SMS capability off",
		ErrInvalidConfig, selected, carried, SelectionNone)
}

// stanceFor turns the selection item and this carrier's own configuration into
// the stance the module states in Prepare.
//
// The selection wins over the carrier's configuration: a carrier the selection
// does not name stands down without being judged, so a deployment can write a
// half-finished section for a carrier it is not running. A carrier the
// selection does name has to be runnable, and a configuration gap there is an
// error rather than a silent absence.
//
// With no selection, a carrier that judges itself configured states
// StateEnabled. The fallback carrier states StateAuto instead: it runs only
// when no other provider was explicitly enabled, and core's exclusive
// resolution stands it down when one was. That is what makes the log carrier
// the channel of last resort without a second coordination mechanism.
func (s Spec[C]) stanceFor(reg *core.Registry) (core.Enablement, error) {
	selected, err := selectionFrom(reg, s.CarrierName)
	if err != nil {
		return core.Enablement{}, err
	}
	switch {
	case selected == SelectionNone:
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: "sms.carrier is \"none\", so the SMS capability is switched off for this run",
		}, nil
	case selected != "" && selected != s.CarrierName:
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: fmt.Sprintf("sms.carrier selects %q; this carrier is %q", selected, s.CarrierName),
		}, nil
	}

	cfg, err := s.read(reg)
	if err != nil {
		return core.Enablement{}, err
	}
	stance, err := s.Stance(cfg)
	if err != nil {
		return core.Enablement{}, err
	}
	if selected == s.CarrierName {
		if stance.State == core.StateDisabled {
			return core.Enablement{}, fmt.Errorf("%s: sms.carrier selects %q, but it cannot run: %s",
				s.ModuleName, s.CarrierName, stance.Reason)
		}
		return core.Enablement{State: core.StateEnabled}, nil
	}
	if s.Fallback {
		return core.Enablement{State: core.StateAuto}, nil
	}
	return stance, nil
}
