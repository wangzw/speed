package static

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace.
const (
	nameItem     = "name"
	subjectsItem = "subjects"
)

// defaultName is the name this backend reports when the configuration does not
// pin one. It is what an identity's owner qualification is written with, so it
// is short and stable rather than descriptive.
const defaultName = "static"

// configDefaults is what this module is worth without a configuration file.
// There is no default subject table: a credential table guessed into existence
// would admit somebody nobody wrote down, so a table naming no subject refuses
// the startup rather than standing down.
var configDefaults = moduleConfig{Name: defaultName}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Name is what this backend reports as its own name. It appears in an
	// identity's owner qualification, so changing it rewrites every policy
	// string written against a qualified subject.
	Name string `config:"name"`
	// Subjects maps each subject to its password hash, in the format
	// ParseHash reads. The value is a hash rather than a password: this
	// table sits in a configuration file, and a file that carries readable
	// passwords hands over every account the moment it is read.
	Subjects map[string]string `config:"subjects"`
}

// settings is the configuration this module actually runs on: the subject
// table with every hash already parsed, so that a malformed one is a startup
// failure rather than a refusal that only the holder of that account ever sees.
type settings struct {
	name     string
	subjects map[string]hashed
}

// resolve turns decoded configuration into what the module runs on.
func (c moduleConfig) resolve() (settings, error) {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return settings{}, fmt.Errorf("%w: %s.%s is empty, and this backend's name is what an "+
			"identity's owner qualification is written with",
			authn.ErrInvalidClaim, ConfigNamespace, nameItem)
	}
	if strings.Contains(name, authn.Separator) {
		return settings{}, fmt.Errorf("%w: %s.%s is %q, which carries %q. That separator is what "+
			"an owner qualification is split on, so a name carrying it could not be told "+
			"from a name followed by a subject", authn.ErrInvalidClaim, ConfigNamespace,
			nameItem, name, authn.Separator)
	}

	s := settings{name: name, subjects: make(map[string]hashed, len(c.Subjects))}
	for subject, encoded := range c.Subjects {
		if strings.TrimSpace(subject) == "" {
			return settings{}, fmt.Errorf("%w: %s.%s carries a subject that is empty or blanks "+
				"alone, which names nobody", authn.ErrInvalidClaim, ConfigNamespace,
				subjectsItem)
		}
		if strings.Contains(subject, authn.Separator) {
			return settings{}, fmt.Errorf("%w: %s.%s carries the subject %q, which holds %q. A "+
				"subject carrying that separator could not be told from an owner "+
				"qualification; map it to one that does not", authn.ErrInvalidClaim,
				ConfigNamespace, subjectsItem, subject, authn.Separator)
		}
		h, err := ParseHash(encoded)
		if err != nil {
			// The hash is not echoed: it is the secret this table
			// exists to hold.
			return settings{}, fmt.Errorf("%s: %s.%s has an unusable password hash for the "+
				"subject %q: %w", ModuleName, ConfigNamespace, subjectsItem, subject, err)
		}
		s.subjects[subject] = h
	}
	return s, nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			nameItem: {
				Description: "the name this backend reports, which qualifies every identity " +
					"it authenticates",
			},
			subjectsItem: {
				Origins:   config.OriginPrimary,
				Required:  true,
				Sensitive: true,
				Description: "the subjects this backend is the authority for, each mapped to " +
					"its password hash as produced by Hash",
			},
		},
	}
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one table.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}
