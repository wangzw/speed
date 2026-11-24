// Package file delivers the file:// transport: it reads the primary config source off the local filesystem and names the format by the file's extension.
package file

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is the name this transport registers under.
const ModuleName = "config.source.file"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, for registries that do not inherit the
// process-level registrations. It holds a resource and nothing else.
func Module() core.Module {
	return core.Module{Name: ModuleName, Resources: []any{source{}}}
}

var _ config.Source = source{}

type source struct{}

func (source) Scheme() string { return "file" }

var formatsByExtension = map[string]string{
	".json": "json",
	".yaml": "yaml",
	".yml":  "yaml",
}

func (source) Fetch(_ context.Context, locator *url.URL) (data []byte, format string, err error) {
	name, err := localPath(locator)
	if err != nil {
		return nil, "", err
	}
	format, err = formatOf(name)
	if err != nil {
		return nil, "", err
	}
	//nolint:gosec // G304: the locator comes from the host's own configuration, and localPath has already refused every shape but a path on this machine.
	data, err = os.ReadFile(name)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s could not be read: %w", config.ErrSourceUnavailable, name, err)
	}
	return data, format, nil
}

func localPath(locator *url.URL) (string, error) {
	if locator.Opaque != "" {
		name, err := url.PathUnescape(locator.Opaque)
		if err != nil {
			return "", fmt.Errorf("%w: %q carries a percent escape that does not decode: %w",
				config.ErrMalformedLocator, locator.String(), err)
		}
		return name, nil
	}
	if !isLocalHost(locator.Host) {
		return "", fmt.Errorf("%w: %q names the host %q, and this transport reads the local "+
			"filesystem alone. A local path is written file:///etc/app.yaml, file://localhost/"+
			"etc/app.yaml or file:app.yaml; a config source on another machine needs the "+
			"transport that serves it",
			config.ErrMalformedLocator, locator.String(), locator.Host)
	}
	if locator.Path == "" {
		return "", fmt.Errorf("%w: %q names no path, as in file:///etc/app.yaml or file:app.yaml",
			config.ErrMalformedLocator, locator.String())
	}
	return locator.Path, nil
}

func isLocalHost(host string) bool {
	return host == "" || strings.EqualFold(host, "localhost")
}

func formatOf(name string) (string, error) {
	ext := strings.ToLower(path.Ext(name))
	if format, known := formatsByExtension[ext]; known {
		return format, nil
	}
	if ext == "" {
		return "", fmt.Errorf("%w: %s has no file extension, and the extension is what names "+
			"the format of a file source. Name the file .json, .yaml or .yml",
			config.ErrUndeterminedFormat, name)
	}
	return "", fmt.Errorf("%w: %s ends in %s, which names no format. The recognised extensions "+
		"are .json, .yaml and .yml; the content is never sniffed, because valid JSON is also "+
		"valid YAML", config.ErrUndeterminedFormat, name, ext)
}
