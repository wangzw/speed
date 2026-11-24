// Package s3 delivers the object-store byte seam over an S3-compatible object store, as one exclusive backend module.
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

const backendName = "s3"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
// Exclusive is set on the byte seam, which is what makes a command name exactly one backend: two
// of them enabled at once fail in resolution.
func Module() core.Module {
	return objectstore.NewModule(objectstore.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Direct:          true,
		Open:            open,
	})
}

func stance(*moduleConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKey, cfg.SecretKey}
}

func open(_ context.Context, cfg *moduleConfig, logger *slog.Logger) (objectstore.Store, error) {
	client, err := clientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &store{client: client, bucket: cfg.Bucket, logger: logger}, nil
}

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

type store struct {
	client *minio.Client
	bucket string
	logger *slog.Logger
}

var (
	_ objectstore.Store     = (*store)(nil)
	_ objectstore.DirectURL = (*store)(nil)
)

const (
	minPresignTTL = time.Second
	maxPresignTTL = 7 * 24 * time.Hour
)

func (*store) Name() string { return backendName }

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

type objectStream struct {
	io.ReadCloser
	store   *store
	key     string
	started time.Time
	failed  sync.Once
}

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

func missingFromService(err error) bool {
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchObject":
		return true
	}
	return resp.StatusCode == http.StatusNotFound
}

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

func checkPresignTTL(ttl time.Duration) error {
	if ttl < minPresignTTL || ttl > maxPresignTTL {
		return fmt.Errorf("%w: the requested lifetime %s is outside [%s, %s]",
			objectstore.ErrURLUnavailable, ttl, minPresignTTL, maxPresignTTL)
	}
	return nil
}

func contentDisposition(filename string) string {
	if filename == "" {
		return ""
	}
	return mime.FormatMediaType("inline", map[string]string{"filename": filename})
}

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

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:keyHashLen]
}

const keyHashLen = 12

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
