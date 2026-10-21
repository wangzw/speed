package metadata

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The runtime tunables this module declares as settings, relative to its
// namespace, and the full paths a read goes by.
const (
	uploadWindowItem = "upload-window"
	objectTTLItem    = "object-ttl"

	uploadWindowPath = ConfigNamespace + "." + uploadWindowItem
	objectTTLPath    = ConfigNamespace + "." + objectTTLItem
)

// runtimeTunables carries the two settings and their declared defaults. The
// settings module reads the defaults off it and it never receives a value.
type runtimeTunables struct {
	UploadWindow time.Duration `config:"upload-window"`
	ObjectTTL    time.Duration `config:"object-ttl"`
}

// tunableDefaults is what the module runs on when neither the store nor a
// written row says otherwise: a fifteen minute upload window and no expiry on a
// completed object.
var tunableDefaults = runtimeTunables{
	UploadWindow: 15 * time.Minute,
	ObjectTTL:    0,
}

// The domains of the two settings. Each is the one statement of its range: the
// settings store holds a write to it, and a read is judged against it again,
// since a row stored before the domain was declared, or written around the
// store, is served as it is.
//
// Both are stored in whole milliseconds, so neither admits a positive value below
// one millisecond: a shorter window truncates to zero and refuses every upload,
// and a shorter lifetime would truncate to zero, which means no expiry. Zero is
// still the object lifetime's off value.
func uploadWindowInDomain(d time.Duration) bool { return d >= time.Millisecond }

func objectTTLInDomain(d time.Duration) bool { return d == 0 || d >= time.Millisecond }

// settingsSchema declares this module's runtime tunables, each with the domain
// the code acts on.
//
// Both take effect without a restart: the upload window is read at each
// declaration and the object lifetime at each completion. The sweep interval is
// not here: it sets a startup ticker, so a change to it still needs a restart.
//
// The items hang from the module's namespace as a mount rather than as the
// schema's namespace, which keeps each item keyed by the full path a write
// names: objectstore.metadata.upload-window and objectstore.metadata.object-ttl.
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
		},
	}
}

// settingsReader reads the two tunables. The store is nil when the assembly has
// none, and every read then answers its declared default, which is a legal
// assembly and says nothing.
type settingsReader struct {
	store settings.Store

	// mu guards reported, which holds for each setting the fault last written, so
	// that a setting read on every call is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store) *settingsReader {
	return &settingsReader{store: store, reported: make(map[string]string)}
}

// uploadWindow is how long a declared object stays open for upload.
func (r *settingsReader) uploadWindow(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, uploadWindowPath, tunableDefaults.UploadWindow, uploadWindowInDomain)
}

// objectTTL is how long a completed object lives before the sweep tombstones it.
func (r *settingsReader) objectTTL(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, objectTTLPath, tunableDefaults.ObjectTTL, objectTTLInDomain)
}

// readTuned reads one tuning setting: one that fails to read, and one outside
// its domain, answers the declared default, and the fault is reported at Error.
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

// report writes the Error line of a setting read in a way other than as it says.
// The same fault is written once and again only when it changes or has been
// cleared in between; an empty problem clears it.
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
