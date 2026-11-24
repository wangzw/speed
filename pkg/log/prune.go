package log

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type archiveFile struct {
	name  string
	stamp time.Time
	seq   int
}

func (rf *rotatingFile) prune() {
	if rf.maxFiles <= 0 && rf.maxAgeDays <= 0 {
		return
	}
	entries, err := os.ReadDir(rf.dir)
	if err != nil {
		rf.notePruneFailure("the archives of %s could not be listed: %v", rf.path, err)
		return
	}
	current := filepath.Base(rf.path)
	archives := make([]archiveFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == current {
			continue
		}
		if a, ok := rf.parseArchive(entry.Name()); ok {
			archives = append(archives, a)
		}
	}
	slices.SortFunc(archives, func(a, b archiveFile) int {
		if !a.stamp.Equal(b.stamp) {
			return b.stamp.Compare(a.stamp)
		}
		return b.seq - a.seq
	})
	cutoff := rf.now().AddDate(0, 0, -rf.maxAgeDays)
	failed := false
	for i, a := range archives {
		overCount := rf.maxFiles > 0 && i >= rf.maxFiles
		overAge := rf.maxAgeDays > 0 && a.stamp.Before(cutoff)
		if !overCount && !overAge {
			continue
		}
		if err := os.Remove(a.name); err != nil && !os.IsNotExist(err) {
			rf.notePruneFailure("the archive %s could not be deleted: %v", a.name, err)
			failed = true
		}
	}
	if !failed {
		rf.pruneFailureReported = false
	}
}

func (rf *rotatingFile) notePruneFailure(format string, args ...any) {
	if rf.pruneFailureReported {
		return
	}
	rf.pruneFailureReported = true
	report(format, args...)
}

func (rf *rotatingFile) parseArchive(base string) (archiveFile, bool) {
	prefix := rf.prefix + "-"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, rf.ext) {
		return archiveFile{}, false
	}
	middle := base[len(prefix) : len(base)-len(rf.ext)]
	parts := strings.Split(middle, "-")
	if len(parts) != 2 && len(parts) != 3 {
		return archiveFile{}, false
	}
	stamp, err := time.ParseInLocation(archiveStamp, parts[0]+"-"+parts[1], time.Local)
	if err != nil {
		return archiveFile{}, false
	}
	seq := 0
	if len(parts) == 3 {
		if seq, err = strconv.Atoi(parts[2]); err != nil || seq < 0 {
			return archiveFile{}, false
		}
	}
	return archiveFile{name: filepath.Join(rf.dir, base), stamp: stamp, seq: seq}, true
}

func (rf *rotatingFile) startBackground(mu sync.Locker) {
	if rf.maxFiles <= 0 && rf.maxAgeDays <= 0 {
		return
	}
	rf.lock = mu
	rf.stop = make(chan struct{})
	rf.done = make(chan struct{})
	go rf.backgroundPrune()
}

func (rf *rotatingFile) backgroundPrune() {
	defer close(rf.done)

	ticker := time.NewTicker(rf.pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rf.stop:
			return
		case <-ticker.C:
			rf.lock.Lock()
			rf.prune()
			rf.lock.Unlock()
		}
	}
}

func (rf *rotatingFile) stopBackground() {
	if rf.stop == nil {
		return
	}
	rf.stopOnce.Do(func() { close(rf.stop) })
	<-rf.done
}
