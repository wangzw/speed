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
package s3

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/vislake/speed/pkg/core"
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
		Open:            open,
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

// open builds the store over the configured service.
func open(_ context.Context, cfg *moduleConfig) (objectstore.Store, error) {
	client, err := clientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &store{client: client, bucket: cfg.Bucket}, nil
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
}

// store must satisfy the seam it delivers.
var _ objectstore.Store = (*store)(nil)

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
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("storing the object's bytes: %w", err)
	}
	return nil
}

// Open returns a read-only stream over the bytes stored under key. The caller
// closes it.
//
// GetObject issues no request until the stream is read, so an object the bucket
// does not hold would answer no error here and fail on the first read. One Stat
// forces the request, so an absent object is reported from Open — the shape the
// local-directory backend has, where opening a missing file errors at once. The
// object's body stream is handed back on success and the caller closes it.
func (s *store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("opening the object's bytes: %w", err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("opening the object's bytes: %w", err)
	}
	return obj, nil
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
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("deleting the object's bytes: %w", err)
	}
	return nil
}
