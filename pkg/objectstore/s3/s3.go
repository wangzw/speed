// Package s3 delivers the object-storage byte seam over an S3-compatible object
// store.
//
// It is a backend module and nothing else. An object's bytes land as an object in
// one bucket, addressed by the internal key the metadata module generates and
// mapped to an object key; reading returns the object's body and deleting removes
// it. A host takes it in by writing ModuleName into a command. The byte seam is
// delivered exclusively, so a command names exactly one backend and two at once
// fail in resolution.
//
// The whole leaf package binds github.com/minio/minio-go/v7 and can be dropped
// without touching any other module: the SDK is imported here and nowhere else,
// so an assembly that uses only the local-directory backend carries no
// object-storage SDK.
//
// The backend holds the credentials that sign every call and interprets no part
// of a key beyond the object-key mapping its own service needs; the internal key
// reaches no error text and no log line, and the credentials are registered as
// literal redaction rules by the factory before this package's Open builds the
// client.
//
// It delivers the direct-URL capability beside the byte seam: a browser can
// reach an object's bytes through a signed address that does not pass through
// this process. The read address carries the media type and file name the
// caller asked for as response overrides, and the write address is a presigned
// PUT. A lifetime outside a second and seven days is refused rather than handed
// to the SDK.
//
// Every byte operation and every signing call is logged once, success or
// failure, with the operation, the bucket, a hash of the internal key, its
// duration and, on a failure, the cause and the service's status — so an
// unreachable service or a refused call can be told apart after the fact. The
// key itself, the object's bytes, the credentials and a signed address — which
// carries the object key and the access-key identifier — reach no field of that
// line.
package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
)

// ModuleName is this module's name in the registry.
const ModuleName = "objectstore.s3"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// backendName is the name this backend reports through Store.Name. It is a
// constant of the code: an assembly names exactly one backend, and a host reading
// a diagnostic has to be able to tell which.
const backendName = "s3"

// init registers this module with the process registry. A host chooses its
// backend by importing it, and what enters its go.mod follows the packages it
// imported: a host that takes only the local-directory backend binds no
// object-storage SDK.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is set on the byte seam, which is what makes a command name exactly
// one backend: two of them enabled at once fail in resolution. The logging
// capability is taken up as an optional requirement, declared by the factory
// every backend shares. The two credentials are handed to the factory through the
// secrets hook, which registers them as literal redaction rules before Open runs.
func Module() core.Module {
	return objectstore.NewModule(objectstore.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		// The S3 backend can hand a browser an address that reaches the bucket
		// without this process, so it declares the direct-URL capability beside
		// the byte seam.
		Direct: true,
		Open:   open,
	})
}

// stance states that the backend runs once its configuration is sound. Every item
// it cannot run without — the endpoint, the bucket and the two credentials — is
// declared required, so the loader has already refused a section that omits one
// before this is reached. What is left for the loader to accept but the backend
// to reject — an endpoint carrying a scheme the backend does not know — is caught
// when the client is built.
func stance(*moduleConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the access-key identifier and its secret, which sign every call.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKey, cfg.SecretKey}
}

// open builds the store over the configured service. logger is the assembly's
// logging capability named after this module, or the process default when the
// assembly delivered none; the store reports every call it makes through it.
func open(_ context.Context, cfg *moduleConfig, logger *slog.Logger) (objectstore.Store, error) {
	client, err := clientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &store{client: client, bucket: cfg.Bucket, logger: logger}, nil
}

// clientFor builds the S3 client from the decoded configuration.
//
// The endpoint may carry an explicit http or https scheme, which chooses the
// transport; a scheme-less endpoint is taken as https, the safe default for a
// cloud service or a TLS-terminated one. use-path-style addresses the bucket in
// the request path, which is what a self-hosted service usually needs; left off,
// the SDK picks its own address form. A client the values cannot build is a
// configuration the backend cannot run on, named as the invalid configuration it
// is.
func clientFor(cfg *moduleConfig) (*minio.Client, error) {
	endpoint, secure, err := transportOf(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	}
	if cfg.UsePathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: building the S3 client for %s.%s: %w",
			objectstore.ErrInvalidConfig, ModuleName, endpointItem, err)
	}
	return client, nil
}

// transportOf splits an explicit http or https scheme off the endpoint, answering
// the scheme-less endpoint and whether the transport is secure. An endpoint that
// carries no scheme is taken as https; one that carries a scheme this backend
// does not know is refused. Only an explicit "http://" or "https://" is read as a
// scheme, so a host-and-port value such as "minio.local:9000" keeps its colon.
func transportOf(endpoint string) (string, bool, error) {
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimPrefix(endpoint, "https://"), true, nil
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimPrefix(endpoint, "http://"), false, nil
	}
	if i := strings.Index(endpoint, "://"); i >= 0 {
		return "", false, fmt.Errorf("%w: the %s.%s value carries the scheme %q, which this backend does "+
			"not know; use http or https, or leave the scheme off", objectstore.ErrInvalidConfig, ModuleName,
			endpointItem, endpoint[:i])
	}
	return endpoint, true, nil
}

// store is this module's product: the byte seam over one S3-compatible bucket.
type store struct {
	// client reaches the service; the SDK's client is safe for concurrent use, so
	// one store serves every caller.
	client *minio.Client
	// bucket is the bucket every object's bytes are kept in.
	bucket string
	// logger is where every call this backend makes is reported: the assembly's
	// logging capability named after this module, or the process default when
	// the assembly delivered none.
	logger *slog.Logger
}

// store must satisfy the seams it delivers.
var (
	_ objectstore.Store     = (*store)(nil)
	_ objectstore.DirectURL = (*store)(nil)
)

// The lifetime bounds a signed address is honoured within. SigV4 presigning
// accepts no less than a second and no more than seven days, and the capability
// keeps the same bounds on the local-directory backend's content route so both
// backends expire a URL by the same rule.
const (
	minPresignTTL = time.Second
	maxPresignTTL = 7 * 24 * time.Hour
)

// Name reports the name this backend answers under. It is a constant: the
// assembly names exactly one backend, and the name identifies it in a diagnostic.
func (*store) Name() string { return backendName }

// Put stores the bytes read from r under key as one object in the bucket.
//
// The size the seam carries is the declared length, which the service records as
// the object's Content-Length; the reader is read once and is not held past the
// call. A call the service refuses, or one that never reaches it, reports the
// SDK's own error, which the metadata module is what wraps up into the
// capability's unavailable sentinel. A second Put to the same key overwrites the
// first, which is what the upload step relies on when a stored byte set is
// retried.
func (s *store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{})
	s.logCall("put", key, time.Since(started), err, "size", size)
	if err != nil {
		return fmt.Errorf("storing the object's bytes: %w", err)
	}
	return nil
}

// objectStream is the read-only stream Open hands back. It reports a body read
// that ends anywhere but at EOF: Open has already written its success line by
// the time the bytes are read, so a transfer that breaks midway would otherwise
// leave no failure line on this side. Exactly one failure line is written per
// stream, however many times a broken reader answers the same error.
//
// The failure line's duration is the body read's own: the clock starts at the
// first Read, which is the call that makes the SDK issue its request, so Open's
// call — which has its own line — and the wait between Open returning and the
// first read are not part of it.
type objectStream struct {
	io.ReadCloser
	store *store
	key   string
	// started is when the first Read issued the body read; the zero value means
	// no read has been made yet.
	started time.Time
	failed  sync.Once
}

// Read reads the body and records the failure of a read that did not end at
// EOF. An end of stream is not a failure: it is the transfer completing.
func (o *objectStream) Read(p []byte) (int, error) {
	if o.started.IsZero() {
		o.started = time.Now()
	}
	n, err := o.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		o.failed.Do(func() {
			o.store.logCall("read", o.key, time.Since(o.started), err)
		})
	}
	return n, err
}

// Open returns a read-only stream over the bytes stored under key. The caller
// closes it.
//
// GetObject issues no request until the stream is read, so an object the bucket
// does not hold would answer no error here and fail on the first read. One Stat
// forces the request, so an absent object is reported from Open — the shape the
// local-directory backend has, where opening a missing file errors at once. The
// object's body stream is handed back on success and the caller closes it.
//
// The success line is written once the object's header is in hand, before any
// body byte is read. The stream itself reports a body read that breaks midway
// (see objectStream), so the S3 side keeps a failure line for a transfer that
// failed after Open returned.
func (s *store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := time.Now()
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		s.logCall("open", key, time.Since(started), err)
		return nil, fmt.Errorf("opening the object's bytes: %w", err)
	}
	info, err := obj.Stat()
	elapsed := time.Since(started)
	if err != nil {
		_ = obj.Close()
		s.logCall("open", key, elapsed, err)
		return nil, fmt.Errorf("opening the object's bytes: %w", err)
	}
	s.logCall("open", key, elapsed, nil, "size", info.Size)
	return &objectStream{ReadCloser: obj, store: s, key: key}, nil
}

// Delete removes the bytes stored under key. It is idempotent: deleting bytes
// that are not there is not an error.
//
// minio's RemoveObject maps to the service's DeleteObject, and an S3-compatible
// service answers 204 for a delete whether or not the object exists; minio treats
// any status other than 204 as the failure and says nothing of a missing object.
// Deleting bytes that are not there is therefore a success, which is the
// idempotence the byte seam's Delete promises. A call the service refuses, or one
// that never reaches it, reports the SDK's own error.
func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	s.logCall("delete", key, time.Since(started), err)
	if err != nil {
		return fmt.Errorf("deleting the object's bytes: %w", err)
	}
	return nil
}

// Stat reports what the bucket holds under key without reading the object's
// body, through a HEAD on the object.
//
// An object the bucket does not hold reports an error wrapping
// objectstore.ErrContentMissing, which is the shape the metadata module's
// completion step reads; any other failure — a refused call, an unreachable
// service — keeps the SDK's own error.
func (s *store) Stat(ctx context.Context, key string) (objectstore.Blob, error) {
	if err := ctx.Err(); err != nil {
		return objectstore.Blob{}, err
	}
	started := time.Now()
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	s.logCall("stat", key, time.Since(started), err)
	if err != nil {
		if missingFromService(err) {
			return objectstore.Blob{}, fmt.Errorf("%w: the bucket holds no object under the key",
				objectstore.ErrContentMissing)
		}
		return objectstore.Blob{}, fmt.Errorf("reading the object's size: %w", err)
	}
	return objectstore.Blob{Size: info.Size}, nil
}

// missingFromService reports whether a failed call says the object is not there:
// the service answered NoSuchKey or NoSuchObject, or a 404.
func missingFromService(err error) bool {
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchObject":
		return true
	}
	return resp.StatusCode == http.StatusNotFound
}

// PresignRead answers a signed address that serves the object's bytes under key
// with the given media type and file name.
//
// The address is signed locally, so no request reaches the service here. The
// media type and file name are carried as response overrides, so the bucket
// serves the bytes with them rather than with whatever the object was stored
// as; an empty file name adds no disposition override.
func (s *store) PresignRead(ctx context.Context, key string, p objectstore.PresignParams) (objectstore.IssuedURL, error) {
	if err := checkPresignTTL(p.TTL); err != nil {
		return objectstore.IssuedURL{}, err
	}
	params := url.Values{}
	if p.ContentType != "" {
		params.Set("response-content-type", p.ContentType)
	}
	if disposition := contentDisposition(p.Filename); disposition != "" {
		params.Set("response-content-disposition", disposition)
	}
	started := time.Now()
	signed, err := s.client.PresignedGetObject(ctx, s.bucket, key, p.TTL, params)
	s.logCall("presign-read", key, time.Since(started), err)
	if err != nil {
		return objectstore.IssuedURL{}, fmt.Errorf("%w: signing a read address: %w", objectstore.ErrURLUnavailable, err)
	}
	return objectstore.IssuedURL{
		URL:     signed.String(),
		Method:  http.MethodGet,
		Expires: started.Add(p.TTL),
	}, nil
}

// PresignWrite answers a signed address a client can PUT the object's bytes to
// under key.
//
// No request header is signed: a presigned PUT binds the method, the object key
// and the expiry, and a Content-Length cannot be expressed in one, so the size
// is what the completion step's Stat checks.
func (s *store) PresignWrite(ctx context.Context, key string, p objectstore.PresignParams) (objectstore.IssuedURL, error) {
	if err := checkPresignTTL(p.TTL); err != nil {
		return objectstore.IssuedURL{}, err
	}
	started := time.Now()
	signed, err := s.client.PresignedPutObject(ctx, s.bucket, key, p.TTL)
	s.logCall("presign-write", key, time.Since(started), err)
	if err != nil {
		return objectstore.IssuedURL{}, fmt.Errorf("%w: signing a write address: %w", objectstore.ErrURLUnavailable, err)
	}
	return objectstore.IssuedURL{
		URL:     signed.String(),
		Method:  http.MethodPut,
		Expires: started.Add(p.TTL),
	}, nil
}

// checkPresignTTL refuses a lifetime a signed address cannot carry, so the
// SDK's own rejection never escapes: the bounds are a second at the least and
// seven days at the most.
func checkPresignTTL(ttl time.Duration) error {
	if ttl < minPresignTTL || ttl > maxPresignTTL {
		return fmt.Errorf("%w: the requested lifetime %s is outside [%s, %s]",
			objectstore.ErrURLUnavailable, ttl, minPresignTTL, maxPresignTTL)
	}
	return nil
}

// contentDisposition renders the header a read address offers the bytes under:
// an inline disposition carrying the file name. The name is escaped by the
// standard library, never spliced in as written. An empty name renders nothing,
// so the header is omitted.
func contentDisposition(filename string) string {
	if filename == "" {
		return ""
	}
	return mime.FormatMediaType("inline", map[string]string{"filename": filename})
}

// logCall records one call the backend made to the service on the module's own
// logger.
//
// The logger is the one the factory took up in New and named after this module,
// not one read from the operation's context: the factory's background sweep
// calls Delete on a context it created, which carries no injected logger, so
// FromContext would put that path's lines on the process default chain rather
// than the destinations the host configured. Reading from the module's logger
// also gives every line the module attribute, whichever context drove the call.
//
// Exactly one line is written per call: outcome "ok" with the operation, the
// bucket, the key's hash and the duration, or outcome "failed" with the same
// fields plus the cause and, when the service answered, its HTTP status. The
// extra arguments follow the common fields — the declared size on a put, the
// observed size on an open. The stat and presign operations pass none: their
// line carries the common fields alone, and a signed address never reaches it. A call the context ended before it was made writes
// no line: no request left the process. The body read that follows Open is the
// exception: it is reported by objectStream as its own "read" call, so a
// transfer that breaks after Open returned still leaves a failure line.
//
// The internal key never reaches the line. key_hash is the first twelve hex
// digits of its SHA-256: enough to correlate the calls that address one object,
// useless for addressing it. A failure's text has the key scrubbed out of it
// before it is logged, because the SDK repeats the key in a service message and
// in the URL a transport error carries.
func (s *store) logCall(operation, key string, elapsed time.Duration, err error, extra ...any) {
	logger := s.logger
	if logger == nil {
		logger = log.Default()
	}
	attrs := make([]any, 0, 10+len(extra))
	attrs = append(attrs,
		"operation", operation,
		"bucket", s.bucket,
		"key_hash", keyHash(key))
	attrs = append(attrs, extra...)
	attrs = append(attrs, "duration_ms", elapsed.Milliseconds())

	if err != nil {
		attrs = append(attrs, "outcome", "failed", "cause", causeOf(err, key))
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.StatusCode != 0 {
			attrs = append(attrs, "http_status", resp.StatusCode)
		}
		logger.Error(ModuleName+": an object store call failed", attrs...)
		return
	}
	attrs = append(attrs, "outcome", "ok")
	logger.Info(ModuleName+": an object store call succeeded", attrs...)
}

// keyHash is how a log line names an object's key without carrying it: the
// first twelve hex digits of the key's SHA-256.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:keyHashLen]
}

// keyHashLen is how many hex digits of the digest a log line carries.
const keyHashLen = 12

// causeOf renders a failed call's cause without the internal key in it.
//
// An S3 service answer is reported by its own error code — AccessDenied,
// NoSuchBucket, InvalidAccessKeyId — which is the classification a failure is
// diagnosed by; its message is not logged, because a service may echo the key
// into it. Every other error keeps its text, with the key scrubbed out: a
// transport failure renders the whole request URL, path included.
func causeOf(err error, key string) string {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		if resp.Code == "" {
			return "service error"
		}
		return scrubKey(resp.Code, key)
	}
	return scrubKey(err.Error(), key)
}

// scrubKey replaces the internal key, in every form a URL can carry it, with a
// fixed marker, so a text that names it can still be logged.
func scrubKey(text, key string) string {
	if key == "" {
		return text
	}
	for _, form := range []string{key, url.PathEscape(key), url.QueryEscape(key)} {
		if form != "" {
			text = strings.ReplaceAll(text, form, "[redacted]")
		}
	}
	return text
}
