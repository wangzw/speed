package log

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const fileMode = 0o600

const archiveStamp = "20060102-150405"

type rotatingFile struct {
	path   string
	dir    string
	prefix string
	ext    string

	maxSize    int64
	maxFiles   int
	maxAgeDays int

	f    *os.File
	size int64

	now                   func() time.Time
	pruneInterval         time.Duration
	lock                  sync.Locker
	stop                  chan struct{}
	done                  chan struct{}
	stopOnce              sync.Once
	pruneFailureReported  bool
	rotateFailureReported bool
}

const backgroundPruneInterval = time.Hour

func openRotatingFile(path string, params fileParams) (*rotatingFile, error) {
	return newRotatingFile(path, int64(params.maxSizeMB)<<20, params.maxFiles, params.maxAgeDays,
		time.Now, backgroundPruneInterval)
}

func newRotatingFile(path string, maxSize int64, maxFiles, maxAgeDays int,
	now func() time.Time, pruneInterval time.Duration,
) (*rotatingFile, error) {
	ext := filepath.Ext(path)
	rf := &rotatingFile{
		path:          path,
		dir:           filepath.Dir(path),
		prefix:        filepath.Base(path[:len(path)-len(ext)]),
		ext:           ext,
		maxSize:       maxSize,
		maxFiles:      maxFiles,
		maxAgeDays:    maxAgeDays,
		now:           now,
		pruneInterval: pruneInterval,
	}
	//nolint:gosec // G304: the path comes from the host's own log configuration and resolveOutput already refused an empty one, so there is no wider input to narrow.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		//nolint:errcheck // the file is being abandoned because Stat failed,
		// and that failure is what the caller is told about. A close error on
		// the way out names a handle the caller never received.
		f.Close()
		return nil, err
	}
	rf.f = f
	rf.size = info.Size()
	rf.prune()
	return rf, nil
}

func (rf *rotatingFile) Write(p []byte) (int, error) {
	if rf.f == nil {
		return 0, fmt.Errorf("log: the active file %s is not open", rf.path)
	}
	if rf.shouldRotate(int64(len(p))) {
		if err := rf.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := rf.f.Write(p)
	rf.size += int64(n)
	return n, err
}

func (rf *rotatingFile) shouldRotate(record int64) bool {
	return rf.maxSize > 0 && rf.size > 0 && rf.size+record > rf.maxSize
}

func (rf *rotatingFile) rotate() error {
	if err := rf.f.Close(); err != nil {
		return err
	}
	rf.f = nil
	if err := os.Rename(rf.path, rf.archiveName()); err != nil {
		f, reopenErr := os.OpenFile(rf.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
		if reopenErr != nil {
			return errors.Join(err, reopenErr)
		}
		rf.f = f
		if !rf.rotateFailureReported {
			rf.rotateFailureReported = true
			report("the active file %s could not be rotated: %v; records go on into it past the size limit "+
				"until a rotation succeeds", rf.path, err)
		}
		return nil
	}
	f, err := os.OpenFile(rf.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	rf.f = f
	rf.size = 0
	rf.rotateFailureReported = false
	rf.prune()
	return nil
}

func (rf *rotatingFile) archiveName() string {
	stamp := rf.now().Format(archiveStamp)
	name := filepath.Join(rf.dir, rf.prefix+"-"+stamp+rf.ext)
	for seq := 1; seq <= maxArchiveSequence; seq++ {
		if _, err := os.Lstat(name); err != nil {
			return name
		}
		name = filepath.Join(rf.dir, rf.prefix+"-"+stamp+"-"+strconv.Itoa(seq)+rf.ext)
	}
	return filepath.Join(rf.dir, fmt.Sprintf("%s-%s-%d%s", rf.prefix, stamp, rf.now().Nanosecond(), rf.ext))
}

const maxArchiveSequence = 1000

func (rf *rotatingFile) release() error {
	if rf.f == nil {
		return nil
	}
	err := rf.f.Close()
	rf.f = nil
	return err
}
