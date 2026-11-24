// Package config collects the input items every registered module declares, layers the command line, the environment, the primary source and the host defaults document into one startup snapshot, and hands each module its own values back. It assembles the host's command tree and registers the config module.
package config

// Schema is the resource type a module declares to state which input items it
// accepts.
type Schema struct {
	// Namespace is where this module's input items are mounted in the config
	// data. An empty namespace puts them at the top level. Two modules may share
	// a namespace as long as their item paths stay disjoint.
	Namespace string
	// Mounts are the carrier structs, relative to the namespace.
	Mounts []Mount
	// Items carries the origins and the metadata of individual input items,
	// keyed by the path relative to the namespace. An item absent from here
	// takes the default origins.
	Items map[string]Item
}

// Mount is one carrier struct together with the path it hangs from. The struct
// is a prototype: it is read for the field structure and for the defaults, and
// never receives the values of a run.
type Mount struct {
	// Path is the prefix relative to the namespace.
	Path string
	// Value is the carrier struct, or a pointer to one. The current value of
	// each field is that field's default.
	Value any
}

// Origin is the set of sources an input item takes its value from.
type Origin uint8

const (
	// OriginPrimary takes the value from the primary config source.
	OriginPrimary Origin = 1 << iota
	// OriginEnv takes the value from an environment variable.
	OriginEnv
	// OriginFlag takes the value from a command-line argument.
	OriginFlag
)

func (o Origin) resolved() Origin {
	if o == 0 {
		return OriginPrimary | OriginEnv
	}
	return o
}

func (o Origin) has(other Origin) bool { return o&other != 0 }

// Item is the origins and the metadata of one input item.
type Item struct {
	// Origins are the sources this item accepts. The zero value stands for
	// OriginPrimary | OriginEnv.
	Origins Origin
	// FlagName is the command-line long name. It is mandatory when Origins
	// contains OriginFlag and is never derived. It is written without the
	// leading dashes and holds no equals sign.
	FlagName string
	// FlagShort is the command-line short name, optional: one ASCII character
	// other than a dash.
	FlagShort string
	// EnvName pins the environment variable name. An empty name is derived from
	// the prefix and the path.
	EnvName string
	// Placeholder is the value placeholder in the help output.
	Placeholder string
	// Description explains the item in the help output. It is mandatory for a
	// sensitive item, whose default is not echoed.
	Description string
	// Required makes Decode fail when no source gave the item a value. The
	// prototype's field value does not count.
	Required bool
	// Sensitive keeps the value out of logs and errors and the default out of
	// the help output.
	Sensitive bool
}

// HostIdentity is the resource a host declares to state the two inputs that
// precede all configuration. At most one may be registered.
type HostIdentity struct {
	// Prefix is the environment variable prefix. With no prefix, derived
	// variable names do not apply while pinned ones still do.
	Prefix string
	// DefaultLocator is the config locator to use when neither the environment
	// nor the command line gives one. An empty locator is a legal configuration
	// with no primary config source.
	DefaultLocator string
}
