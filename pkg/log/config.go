package log

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/vislake/speed/pkg/config"
)

// Config is the set of input items this module accepts.
type Config struct {
	// Level is the lowest level the chain lets through.
	Level string `config:"level"`
	// Outputs is one entry per destination and format pair.
	Outputs []Output `config:"outputs"`
}

// Output is one output: a destination, a format, and the parameters of that destination.
// A destination that does not use a parameter ignores it, so stdout carrying a rotation size does
// nothing.
type Output struct {
	// To is the destination name, one of the built-in closed set.
	To string `config:"to"`
	// Format is the format name, one of the built-in closed set.
	Format string `config:"format"`
	// Path is the file a file destination writes to.
	Path string `config:"path"`

	// MaxSizeMB is the size a file grows to before it is rotated.
	MaxSizeMB *int `config:"max-size-mb"`
	// MaxFiles is how many archives are kept.
	MaxFiles *int `config:"max-files"`
	// MaxAgeDays is how long an archive is kept.
	MaxAgeDays *int `config:"max-age-days"`
}

const (
	destStdout = "stdout"
	destStderr = "stderr"
	destFile   = "file"
)

const (
	formatText = "text"
	formatJSON = "json"
)

const (
	defaultMaxSizeMB  = 100
	defaultMaxFiles   = 10
	defaultMaxAgeDays = 30
)

const configNamespace = "log"

const configPath = configNamespace

var configDefaults = Config{
	Level:   "info",
	Outputs: []Output{{To: destStdout, Format: formatText}},
}

func schema() config.Schema {
	return config.Schema{
		Namespace: configNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			"level": {
				Origins:     config.OriginPrimary | config.OriginEnv | config.OriginFlag,
				FlagName:    "log-level",
				Placeholder: "LEVEL",
				Description: "the lowest level that is logged: debug, info, warn or error",
			},
			"outputs": {
				Origins:     config.OriginPrimary,
				Description: "the destinations and formats to log to",
			},
		},
	}
}

type resolvedConfig struct {
	level   slog.Level
	outputs []resolvedOutput
}

type resolvedOutput struct {
	to         string
	format     string
	path       string
	maxSizeMB  int
	maxFiles   int
	maxAgeDays int
}

func (c Config) resolve() (resolvedConfig, error) {
	level, err := parseLevel(c.Level)
	if err != nil {
		return resolvedConfig{}, err
	}
	out := resolvedConfig{level: level, outputs: make([]resolvedOutput, 0, len(c.Outputs))}
	for i, o := range c.Outputs {
		resolved, err := o.resolve(i)
		if err != nil {
			return resolvedConfig{}, err
		}
		out.outputs = append(out.outputs, resolved)
	}
	return out, nil
}

func (o Output) resolve(index int) (resolvedOutput, error) {
	switch o.To {
	case destStdout, destStderr, destFile:
	default:
		return resolvedOutput{}, fmt.Errorf("%w: output %d gives %q", ErrUnknownDestination, index, o.To)
	}
	switch o.Format {
	case formatText, formatJSON:
	default:
		return resolvedOutput{}, fmt.Errorf("%w: output %d, to %s, gives %q",
			ErrUnknownFormat, index, o.To, o.Format)
	}
	if o.To == destFile && o.Path == "" {
		return resolvedOutput{}, fmt.Errorf("%w: output %d writes to a file and needs a path to write to",
			ErrMissingPath, index)
	}
	for _, p := range [...]struct {
		field string
		given *int
	}{
		{"max-size-mb", o.MaxSizeMB},
		{"max-files", o.MaxFiles},
		{"max-age-days", o.MaxAgeDays},
	} {
		if p.given != nil && *p.given < 0 {
			return resolvedOutput{}, fmt.Errorf("%w: output %d gives %s the value %d; "+
				"0 switches the item off and a positive number sets it",
				ErrInvalidFileParam, index, p.field, *p.given)
		}
	}
	return resolvedOutput{
		to:         o.To,
		format:     o.Format,
		path:       o.Path,
		maxSizeMB:  paramOrDefault(o.MaxSizeMB, defaultMaxSizeMB),
		maxFiles:   paramOrDefault(o.MaxFiles, defaultMaxFiles),
		maxAgeDays: paramOrDefault(o.MaxAgeDays, defaultMaxAgeDays),
	}, nil
}

func paramOrDefault(given *int, def int) int {
	if given == nil {
		return def
	}
	return *given
}

func parseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("%w: the configuration gives %q", ErrInvalidLevel, name)
}
