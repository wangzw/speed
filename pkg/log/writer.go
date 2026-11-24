package log

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var destinationWriters = map[string]*writerEntry{}

type writerEntry struct {
	w      *destWriter
	refs   int
	params fileParams
}

var errWriterClosed = errors.New("log: the writer of this destination is closed")

type sink interface {
	io.Writer
	startBackground(mu sync.Locker)
	stopBackground()
	release() error
}

type destWriter struct {
	id     string
	silent bool

	mu       sync.Mutex
	closed   bool
	failures int
	reported bool
	sink     sink
}

func (w *destWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		w.noteFailure(errWriterClosed)
		return 0, errWriterClosed
	}
	n, err := w.sink.Write(p)
	if err != nil {
		w.noteFailure(err)
		return n, err
	}
	w.noteSuccess()
	return n, nil
}

func (w *destWriter) noteFailure(err error) {
	w.failures++
	if w.reported || w.silent {
		return
	}
	w.reported = true
	report("destination %s did not take a record: %v; further failures stay silent until one write succeeds", w.id, err)
}

func (w *destWriter) noteSuccess() {
	if w.reported {
		report("destination %s took a record again, after %d that it dropped", w.id, w.failures)
		w.reported = false
	}
	w.failures = 0
}

func (w *destWriter) close() {
	w.sink.stopBackground()

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}
	w.closed = true
	if err := w.sink.release(); err != nil {
		report("destination %s did not close cleanly: %v", w.id, err)
	}
}

func report(format string, args ...any) {
	//nolint:errcheck // os.Stderr is the report sink itself, so a failed write
	// has nowhere left to be reported.
	fmt.Fprintf(os.Stderr, "log: "+format+"\n", args...)
}

type streamSink struct{ f *os.File }

func (s streamSink) Write(p []byte) (int, error) { return s.f.Write(p) }

func (streamSink) startBackground(sync.Locker) {}

func (streamSink) stopBackground() {}

func (streamSink) release() error { return nil }

type fileParams struct {
	maxSizeMB  int
	maxFiles   int
	maxAgeDays int
}

func paramsOf(o resolvedOutput) fileParams {
	if o.to != destFile {
		return fileParams{}
	}
	return fileParams{maxSizeMB: o.maxSizeMB, maxFiles: o.maxFiles, maxAgeDays: o.maxAgeDays}
}

func (p fileParams) differencesFrom(other fileParams) string {
	var diffs []string
	for _, d := range [...]struct {
		field      string
		open, want int
	}{
		{"max-size-mb", p.maxSizeMB, other.maxSizeMB},
		{"max-files", p.maxFiles, other.maxFiles},
		{"max-age-days", p.maxAgeDays, other.maxAgeDays},
	} {
		if d.open != d.want {
			diffs = append(diffs, fmt.Sprintf("%s is %d and this output asks for %d", d.field, d.open, d.want))
		}
	}
	return strings.Join(diffs, ", ")
}

func destinationID(o resolvedOutput) string {
	switch o.to {
	case destStdout:
		return "stdout"
	case destStderr:
		return "stderr"
	default:
		return "file:" + resolvePath(o.path)
	}
}

func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir, base := filepath.Split(abs)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return abs
}

func openWriters(outputs []resolvedOutput) ([]*destWriter, func(), error) {
	rootMu.Lock()
	defer rootMu.Unlock()

	taken := make([]*destWriter, 0, len(outputs))
	for _, o := range outputs {
		w, err := acquireWriterLocked(o)
		if err != nil {
			for _, done := range taken {
				releaseWriterLocked(done)
			}
			return nil, nil, err
		}
		taken = append(taken, w)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			rootMu.Lock()
			defer rootMu.Unlock()
			for _, w := range taken {
				releaseWriterLocked(w)
			}
		})
	}
	return taken, release, nil
}

func acquireWriterLocked(o resolvedOutput) (*destWriter, error) {
	id := destinationID(o)
	params := paramsOf(o)

	if entry, ok := destinationWriters[id]; ok {
		if diff := entry.params.differencesFrom(params); diff != "" {
			return nil, fmt.Errorf("%w: destination %s is open with %s", ErrConflictingOutput, id, diff)
		}
		entry.refs++
		return entry.w, nil
	}

	w, err := openWriter(id, o, params)
	if err != nil {
		return nil, err
	}
	w.sink.startBackground(&w.mu)
	destinationWriters[id] = &writerEntry{w: w, refs: 1, params: params}
	return w, nil
}

func openWriter(id string, o resolvedOutput, params fileParams) (*destWriter, error) {
	switch o.to {
	case destStdout:
		return &destWriter{id: id, sink: streamSink{os.Stdout}}, nil
	case destStderr:
		return &destWriter{id: id, silent: true, sink: streamSink{os.Stderr}}, nil
	case destFile:
		f, err := openRotatingFile(o.path, params)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrOutputUnavailable, o.path, err)
		}
		return &destWriter{id: id, sink: f}, nil
	}
	return nil, fmt.Errorf("%w: output gives %q", ErrUnknownDestination, o.to)
}

func releaseWriterLocked(w *destWriter) {
	entry, ok := destinationWriters[w.id]
	if !ok || entry.w != w {
		return
	}
	entry.refs--
	if entry.refs > 0 {
		return
	}
	delete(destinationWriters, w.id)
	w.close()
}

var bootstrapStdoutWriter = registerBootstrapStdout()

func registerBootstrapStdout() *destWriter {
	rootMu.Lock()
	defer rootMu.Unlock()

	id := destinationID(resolvedOutput{to: destStdout})
	w := &destWriter{id: id, sink: streamSink{os.Stdout}}
	destinationWriters[id] = &writerEntry{w: w, refs: 1}
	return w
}
