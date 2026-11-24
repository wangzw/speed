package sms

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

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

const (
	registryNamespace     = "sms.registry"
	registryTemplatesItem = "templates"
)

type templatesConfig struct {
	Templates map[string]TemplateSpec `config:"templates"`
}

// OffByMissingTemplates reports whether this run leaves the SMS capability with
// nothing to send: sms.carrier is unset and no template is configured. An
// explicit selection is not judged, and neither is an assembly with no reader.
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
// selection item uses for it and the name it answers Name with.
type CarrierName struct {
	// Name is the carrier's name in the selection item, such as "aliyun" or
	// "log".
	Name string
}

type selectionConfig struct {
	Carrier string `config:"carrier"`
}

// SelectionSchema is the input-item declaration the log carrier mounts, so a run
// can write sms.carrier.
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

// OffBySelection reports whether sms.carrier switches the capability off, and the
// reason a dependent states when it stands down.
func OffBySelection(reg *core.Registry) (bool, string, error) {
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

// stanceFor turns the selection item and the carrier configuration into the
// Prepare stance. The selection wins: an unnamed carrier stands down unjudged, a
// named one must be runnable, and the fallback states StateAuto with no selection.
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
