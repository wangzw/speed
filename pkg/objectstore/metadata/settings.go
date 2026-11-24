package metadata

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const (
	uploadWindowItem = "upload-window"
	objectTTLItem    = "object-ttl"
	urlTTLItem       = "url-ttl"

	uploadWindowPath = ConfigNamespace + "." + uploadWindowItem
	objectTTLPath    = ConfigNamespace + "." + objectTTLItem
	urlTTLPath       = ConfigNamespace + "." + urlTTLItem
)

type runtimeTunables struct {
	UploadWindow time.Duration `config:"upload-window"`
	ObjectTTL    time.Duration `config:"object-ttl"`
	URLTTL       time.Duration `config:"url-ttl"`
}

var tunableDefaults = runtimeTunables{
	UploadWindow: 15 * time.Minute,
	ObjectTTL:    0,
	URLTTL:       5 * time.Minute,
}

func uploadWindowInDomain(d time.Duration) bool { return d >= time.Millisecond }

func objectTTLInDomain(d time.Duration) bool { return d == 0 || d >= time.Millisecond }

func urlTTLInDomain(d time.Duration) bool { return d >= time.Second && d <= maxURLTTL }

func settingsSchema() settings.Schema {
	return settings.Schema{
		Mounts: []settings.Mount{{Path: ConfigNamespace, Value: &tunableDefaults}},
		Items: map[string]settings.Item{
			uploadWindowPath: {
				Description: "how long a declared object stays open for upload, which must be at " +
					"least one millisecond; an upload past the window is refused, and the sweep " +
					"tombstones a pending object whose window has closed",
				Domain: settings.DomainOf("the durations of at least one millisecond", uploadWindowInDomain),
			},
			objectTTLPath: {
				Description: "how long a completed object lives before the sweep tombstones it, " +
					"which is either zero or at least one millisecond; zero leaves a completed " +
					"object without an expiry",
				Domain: settings.DomainOf("zero or the durations of at least one millisecond", objectTTLInDomain),
			},
			urlTTLPath: {
				Description: "how long a minted direct-access address lives, from one second to seven " +
					"days; the actual address is clamped again to the object's remaining life or its " +
					"upload window",
				Domain: settings.DomainOf("the durations from one second to seven days", urlTTLInDomain),
			},
		},
	}
}

type settingsReader struct {
	store settings.Store

	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store) *settingsReader {
	return &settingsReader{store: store, reported: make(map[string]string)}
}

func (r *settingsReader) uploadWindow(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, uploadWindowPath, tunableDefaults.UploadWindow, uploadWindowInDomain)
}

func (r *settingsReader) objectTTL(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, objectTTLPath, tunableDefaults.ObjectTTL, objectTTLInDomain)
}

func (r *settingsReader) urlTTL(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, urlTTLPath, tunableDefaults.URLTTL, urlTTLInDomain)
}

func readTuned[T any](r *settingsReader, logger *slog.Logger, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[T](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(logger, path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		r.report(logger, path, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(v))
		return fallback
	}
	r.report(logger, path, "", "")
	return v
}

func (r *settingsReader) report(logger *slog.Logger, path, problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[path]
	switch {
	case problem == "":
		delete(r.reported, path)
	case !reported || last != detail:
		r.reported[path] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}
