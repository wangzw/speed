// Package nats is the NATS JetStream implementation of the key-value capability pkg/kv declares.
package nats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/retry"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.nats"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	defaultBucket   = "speed_kv"
	casAttempts     = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to.
	// A host that names this implementation has to give it one.
	// Marked sensitive: it carries the credentials.
	URL string `config:"url"`
	// Bucket is the key-value bucket to use.
	Bucket string `config:"bucket"`
	// Timeout bounds connecting and one operation.
	Timeout time.Duration `config:"timeout"`
}

const (
	urlKey     = "url"
	bucketKey  = "bucket"
	timeoutKey = "timeout"
)

const defaultTimeout = 5 * time.Second

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Bucket: defaultBucket, Timeout: defaultTimeout},
		Items: map[string]config.Item{
			urlKey: {
				Sensitive: true,
				Description: "the NATS server to connect to, with any credentials in the URL; an " +
					"assembly that names this implementation has to give it one",
			},
			bucketKey: {
				Description: "the JetStream key-value bucket entries are kept in",
			},
			timeoutKey: {
				Description: "how long connecting and one operation may take, written with a unit",
			},
		},
		Stance:  stance,
		Open:    open,
		Secrets: func(cfg *Config) []string { return []string{cfg.URL} },
	})
}

func stance(cfg *Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation reaches "+
			"NATS at one: with none set there is nothing to start against. Set it, or leave this "+
			"implementation out of the command", ConfigNamespace, urlKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func open(ctx context.Context, cfg *Config) (kv.KV, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.Timeout(cfg.Timeout),
		nats.MaxReconnects(-1),
		nats.ReconnectBufSize(-1))
	if err != nil {
		return nil, fmt.Errorf("%s: connecting: %w", ModuleName, err)
	}
	// The library's own five seconds applies to every JetStream request whose
	// context carries no deadline unless this option replaces it.
	var jsOpts []jetstream.JetStreamOpt
	if cfg.Timeout > 0 {
		jsOpts = append(jsOpts, jetstream.WithDefaultTimeout(cfg.Timeout))
	}
	js, err := jetstream.New(nc, jsOpts...)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("%s: opening JetStream: %w", ModuleName, err)
	}
	bucket, err := bindBucket(ctx, js, cfg.Bucket)
	if err == nil {
		err = allowMessageTTL(ctx, js, cfg.Bucket)
	}
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("%s: opening the bucket %q: %w", ModuleName, cfg.Bucket, err)
	}
	return &store{nc: nc, js: js, bucket: bucket, subjects: "$KV." + cfg.Bucket + "."}, nil
}

func bindBucket(ctx context.Context, js jetstream.JetStream, name string) (jetstream.KeyValue, error) {
	bucket, err := js.KeyValue(ctx, name)
	if !errors.Is(err, jetstream.ErrBucketNotFound) {
		return bucket, err
	}
	bucket, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: name})
	if errors.Is(err, jetstream.ErrBucketExists) {
		return js.KeyValue(ctx, name)
	}
	return bucket, err
}

func allowMessageTTL(ctx context.Context, js jetstream.JetStream, bucket string) error {
	stream, err := js.Stream(ctx, "KV_"+bucket)
	if err != nil {
		return fmt.Errorf("reading the bucket's stream: %w", err)
	}
	cfg := stream.CachedInfo().Config
	if cfg.AllowMsgTTL {
		return nil
	}
	cfg.AllowMsgTTL = true
	updated, err := js.UpdateStream(ctx, cfg)
	if err != nil {
		return fmt.Errorf("the bucket does not allow a per-message time-to-live, which this implementation "+
			"needs to remove expired entries, and switching it on failed: %w", err)
	}
	if !updated.CachedInfo().Config.AllowMsgTTL {
		return errors.New("the server kept the bucket's per-message time-to-live off, and this implementation " +
			"needs it to remove expired entries; it takes NATS Server 2.11 or later")
	}
	return nil
}

type store struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	bucket   jetstream.KeyValue
	subjects string
}

const reclaimMargin = time.Second

func reclaimAfter(expires time.Time) time.Duration {
	if expires.IsZero() {
		return 0
	}
	return max(time.Until(expires), 0) + reclaimMargin
}

func publishOpts(expires time.Time) []jetstream.PublishOpt {
	if ttl := reclaimAfter(expires); ttl > 0 {
		return []jetstream.PublishOpt{jetstream.WithMsgTTL(ttl)}
	}
	return nil
}

var errContended = errors.New(ModuleName + ": another writer changed the key first")

func contended(err error) error {
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence ||
		apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant) {
		return errContended
	}
	return err
}

func (s *store) put(ctx context.Context, encoded string, stored []byte, expires time.Time) error {
	_, err := s.js.PublishMsg(ctx, &nats.Msg{Subject: s.subjects + encoded, Data: stored}, publishOpts(expires)...)
	return err
}

func (s *store) update(ctx context.Context, encoded string, stored []byte, expires time.Time, revision uint64) error {
	opts := append(publishOpts(expires), jetstream.WithExpectLastSequencePerSubject(revision))
	_, err := s.js.PublishMsg(ctx, &nats.Msg{Subject: s.subjects + encoded, Data: stored}, opts...)
	return contended(err)
}

func (s *store) create(ctx context.Context, encoded string, stored []byte, expires time.Time) error {
	var opts []jetstream.KVCreateOpt
	if ttl := reclaimAfter(expires); ttl > 0 {
		opts = append(opts, jetstream.KeyTTL(ttl))
	}
	_, err := s.bucket.Create(ctx, encoded, stored, opts...)
	return contended(err)
}

func (s *store) Close(context.Context) error {
	s.nc.Close()
	return nil
}

func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	entry, err := s.bucket.Get(ctx, encodeKey(key))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value, live := expiry.Unwrap(entry.Value())
	if !live {
		return nil, false, nil
	}
	return value, true, nil
}

func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	expires := expiry.Of(ttl)
	return s.put(ctx, encodeKey(key), expiry.Wrap(value, expires), expires)
}

func (s *store) Delete(ctx context.Context, key string) error {
	err := s.bucket.Purge(ctx, encodeKey(key), jetstream.PurgeTTL(reclaimMargin))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	return err
}

func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
	encoded := encodeKey(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entry, err := s.bucket.Get(ctx, encoded)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			next := delta
			expires := time.Time{}
			if withTTL {
				expires = expiry.Of(ttl)
			}
			if createErr := s.create(ctx, encoded, expiry.Wrap([]byte(formatFloat(next)), expires), expires); createErr != nil {
				if errors.Is(createErr, errContended) {
					retry.Backoff(attempt)
					continue
				}
				return 0, createErr
			}
			return next, nil
		}
		if err != nil {
			return 0, err
		}
		value, expires, live := expiry.UnwrapFull(entry.Value())
		var current float64
		if live {
			parsed, err := strconv.ParseFloat(string(value), 64)
			if err != nil {
				return 0, fmt.Errorf("%w (key %q)", kv.ErrNotNumeric, key)
			}
			current = parsed
		} else {
			expires = time.Time{}
			if withTTL {
				expires = expiry.Of(ttl)
			}
		}
		next := current + delta
		if err := counter.CheckResult(ModuleName, key, next); err != nil {
			return 0, err
		}
		stored := expiry.Wrap([]byte(formatFloat(next)), expires)
		if updateErr := s.update(ctx, encoded, stored, expires, entry.Revision()); updateErr != nil {
			if errors.Is(updateErr, errContended) {
				retry.Backoff(attempt)
				continue
			}
			return 0, updateErr
		}
		return next, nil
	}
	return 0, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	return s.writeIf(ctx, key, func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool) {
		if !live {
			value, expires = nil, time.Time{}
		}
		if !bytes.Equal(value, old) {
			return nil, time.Time{}, false
		}
		return newVal, expires, true
	})
}

func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.writeIf(ctx, key, func(_ []byte, _ time.Time, live bool) ([]byte, time.Time, bool) {
		if live {
			return nil, time.Time{}, false
		}
		return value, expiry.Of(ttl), true
	})
}

type decision func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool)

func (s *store) writeIf(ctx context.Context, key string, decide decision) (bool, error) {
	encoded := encodeKey(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		entry, err := s.bucket.Get(ctx, encoded)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound):
			value, expires, ok := decide(nil, time.Time{}, false)
			if !ok {
				return false, nil
			}
			err = s.create(ctx, encoded, expiry.Wrap(value, expires), expires)
		case err != nil:
			return false, err
		default:
			current, currentExpires, live := expiry.UnwrapFull(entry.Value())
			value, expires, ok := decide(current, currentExpires, live)
			if !ok {
				return false, nil
			}
			err = s.update(ctx, encoded, expiry.Wrap(value, expires), expires, entry.Revision())
		}
		if errors.Is(err, errContended) {
			retry.Backoff(attempt)
			continue
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

const maxPlainKeyLen = 1024

const digestPrefix = "sha256."

func encodeKey(key string) string {
	if key != "" && 2*len(key) <= maxPlainKeyLen {
		return hex.EncodeToString([]byte(key))
	}
	sum := sha256.Sum256([]byte(key))
	return digestPrefix + hex.EncodeToString(sum[:])
}
