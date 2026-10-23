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
	urlTTLItem       = "url-ttl"

	uploadWindowPath = ConfigNamespace + "." + uploadWindowItem
	objectTTLPath    = ConfigNamespace + "." + objectTTLItem
	urlTTLPath       = ConfigNamespace + "." + urlTTLItem
)

// runtimeTunables carries the three settings and their declared defaults. The
// settings module reads the defaults off it and it never receives a value.
type runtimeTunables struct {
	UploadWindow time.Duration `config:"upload-window"`
	ObjectTTL    time.Duration `config:"object-ttl"`
	URLTTL       time.Duration `config:"url-ttl"`
}

// tunableDefaults is what the module runs on when neither the store nor a
// written row says otherwise: a fifteen minute upload window, no expiry on a
// completed object, and a five minute lifetime on a minted URL.
var tunableDefaults = runtimeTunables{
	UploadWindow: 15 * time.Minute,
	ObjectTTL:    0,
	URLTTL:       5 * time.Minute,
}

// The domains of the three settings. Each is the one statement of its range: the
// settings store holds a write to it, and a read is judged against it again,
// since a row stored before the domain was declared, or written around the
// store, is served as it is.
//
// The upload window and the object lifetime are stored in whole milliseconds, so
// neither admits a positive value below one millisecond: a shorter window
// truncates to zero and refuses every upload, and a shorter lifetime would
// truncate to zero, which means no expiry. Zero is still the object lifetime's
// off value.
//
// The URL lifetime is bounded by one fact at each end. The lower bound is one
// second, because a shorter address is of no use to a browser. The upper bound
// is seven days, the longest an S3 SigV4 pre-signed address lives; the fs
// content address keeps the same bound so both backends answer the same expiry
// semantics. The value actually granted is clamped again to the upload window or
// the object's remaining life.
func uploadWindowInDomain(d time.Duration) bool { return d >= time.Millisecond }

func objectTTLInDomain(d time.Duration) bool { return d == 0 || d >= time.Millisecond }

func urlTTLInDomain(d time.Duration) bool { return d >= time.Second && d <= maxURLTTL }

// settingsSchema declares this module's runtime tunables, each with the domain
// the code acts on.
//
// All three take effect without a restart: the upload window is read at each
// declaration, the object lifetime at each completion, and the URL lifetime at
// each URL mint. The sweep interval is not here: it sets a startup ticker, so a
// change to it still needs a restart.
//
// The items hang from the module's namespace as a mount rather than as the
// schema's namespace, which keeps each item keyed by the full path a write
// names: objectstore.metadata.upload-window, objectstore.metadata.object-ttl and
// objectstore.metadata.url-ttl.
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

// urlTTL is how long a freshly minted direct-access address lives before its
// remaining life, its upload window or the backend maximum shortens it.
func (r *settingsReader) urlTTL(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, urlTTLPath, tunableDefaults.URLTTL, urlTTLInDomain)
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
