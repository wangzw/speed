package mail

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The mail capability runs exactly one carrier at run time, chosen by the
// explicit selection item mail.carrier. Several carriers may be compiled into
// one binary; the selection is a run-time decision, not a build-time one.
const (
	// SelectionNamespace is the configuration section the selection item lives
	// under.
	SelectionNamespace = "mail"
	// SelectionItem is the item naming the carrier to run.
	SelectionItem = "carrier"
	// SelectionNone switches the capability off: no carrier runs, the log
	// fallback included, and a module that requires the sending seam stands
	// itself down.
	SelectionNone = "none"
)

// CarrierName is the resource a carrier module declares to state the name the
// selection item uses for it, and the name it answers Name with.
//
// The declaration is how a carrier's own Prepare can tell a selection that
// names a carrier this binary does not carry from one that names a compiled
// carrier that merely is not this one. It is a resource rather than a method
// because Prepare runs before any carrier is constructed.
type CarrierName struct {
	// Name is the carrier's name in the selection item, such as "smtp" or
	// "log".
	Name string
}

// selectionConfig is the shape the selection item decodes into.
type selectionConfig struct {
	Carrier string `config:"carrier"`
}

// SelectionSchema is the input-item declaration the log carrier mounts, so a
// run can write mail.carrier. The log carrier declares it because it is
// compiled into every build; a binary carrying a vendor carrier without the
// log carrier accepts no selection and auto-selects the sole configured
// carrier.
func SelectionSchema() config.Schema {
	return config.Schema{
		Namespace: SelectionNamespace,
		Mounts:    []config.Mount{{Value: &selectionConfig{}}},
		Items: map[string]config.Item{
			SelectionItem: {
				Description: "the mail carrier this run sends through: a compiled carrier's name, " +
					"or none to switch the mail capability off",
			},
		},
	}
}

// ReadSelection reads mail.carrier. An absent item is the empty string, which
// asks for automatic selection: a configured carrier runs, and the log carrier
// is the fallback when none is.
func ReadSelection(reader config.Reader) (string, error) {
	var cfg selectionConfig
	if err := reader.Decode(SelectionNamespace, &cfg); err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Carrier), nil
}

// OffBySelection reports whether mail.carrier switches the capability off, and
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
		return true, "mail.carrier is \"none\", so the mail capability is off in this run", nil
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
// carrier: a typo in mail.carrier would otherwise leave the capability off
// while every dependency stood down as though the deployment had chosen none.
func selectionFrom(reg *core.Registry, carrierName string) (string, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return "", fmt.Errorf("%s: reading the mail carrier selection: %w", carrierName, err)
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
	return "", fmt.Errorf("%w: mail.carrier names %q, which is not a mail carrier compiled into this "+
		"program; this binary carries %s, and %q switches the mail capability off",
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
			Reason: "mail.carrier is \"none\", so the mail capability is switched off for this run",
		}, nil
	case selected != "" && selected != s.CarrierName:
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: fmt.Sprintf("mail.carrier selects %q; this carrier is %q", selected, s.CarrierName),
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
			return core.Enablement{}, fmt.Errorf("%s: mail.carrier selects %q, but it cannot run: %s",
				s.ModuleName, s.CarrierName, stance.Reason)
		}
		return core.Enablement{State: core.StateEnabled}, nil
	}
	if s.Fallback {
		return core.Enablement{State: core.StateAuto}, nil
	}
	return stance, nil
}
