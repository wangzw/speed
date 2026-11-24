package log

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testArchiveStamp = "20060102-150405"

func makeArchive(t *testing.T, dir string, stamp time.Time, seq int, content string) string {
	t.Helper()
	name := "app-" + stamp.Format(testArchiveStamp)
	if seq > 0 {
		name += fmt.Sprintf("-%d", seq)
	}
	name += ".log"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the archive %s failed: %v", name, err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestPruneRunsOnOpen pins the first of the three occasions.
func TestPruneRunsOnOpen(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := makeArchive(t, dir, now.AddDate(0, 0, -40), 0, "stale\n")
	fresh := makeArchive(t, dir, now.Add(-time.Hour), 0, "fresh\n")

	newTestFile(t, filepath.Join(dir, "app.log"), 0, 0, 30, time.Now)

	if exists(stale) {
		t.Error("an archive 40 days past a 30 day retention survived the open")
	}
	if !exists(fresh) {
		t.Error("an archive written an hour ago was deleted on the open")
	}
}

// TestPruneRunsAfterEveryRotation pins the second occasion: the count limit takes effect the
// moment an archive appears, rather than at the next background pass an hour later.
func TestPruneRunsAfterEveryRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	rf := newTestFile(t, path, 24, 2, 0, time.Now)

	for i := range 6 {
		write(t, rf, fmt.Sprintf("record number %d, long enough to fill the file\n", i))
		if archives := otherFilesIn(t, dir, "app.log"); len(archives) > 2 {
			t.Fatalf("after record %d the directory holds %v, want at most the 2 archives that are kept", i, archives)
		}
	}
	if archives := otherFilesIn(t, dir, "app.log"); len(archives) != 2 {
		t.Errorf("the run ended with %v, want the 2 newest archives", archives)
	}
}

// TestBackgroundPruneRunsWhenRotationIsDisabled checks that the background prune deletes an archive
// once it is past the retention age even with rotation off.
func TestBackgroundPruneRunsWhenRotationIsDisabled(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	var offset atomic.Int64
	clock := func() time.Time { return base.Add(time.Duration(offset.Load())) }

	archive := makeArchive(t, dir, base.Add(-time.Hour), 0, "an hour old\n")
	rf, err := newRotatingFile(filepath.Join(dir, "app.log"), 0, 0, 1, clock, time.Millisecond)
	if err != nil {
		t.Fatalf("opening the file destination failed: %v", err)
	}
	var mu sync.Mutex
	rf.startBackground(&mu)
	t.Cleanup(func() {
		rf.stopBackground()
		rf.release()
	})

	if !exists(archive) {
		t.Fatal("an archive an hour old was deleted under a retention of a day")
	}
	offset.Store(int64(48 * time.Hour))
	waitFor(t, "the background prune to delete an archive that has gone over age", func() bool { return !exists(archive) })
}

// TestAgeIsJudgedByArchiveNameNotMtime pins which clock the retention reads.
func TestAgeIsJudgedByArchiveNameNotMtime(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	oldName := makeArchive(t, dir, now.AddDate(0, 0, -10), 0, "old name\n")
	if err := os.Chtimes(oldName, now, now); err != nil {
		t.Fatalf("setting the modification time failed: %v", err)
	}
	freshName := makeArchive(t, dir, now.Add(-time.Minute), 0, "fresh name\n")
	if err := os.Chtimes(freshName, now.AddDate(0, 0, -10), now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("setting the modification time failed: %v", err)
	}

	newTestFile(t, filepath.Join(dir, "app.log"), 0, 0, 1, time.Now)

	if exists(oldName) {
		t.Error("an archive whose name is 10 days old survived, so the age was judged by the modification time")
	}
	if !exists(freshName) {
		t.Error("an archive named a minute ago was deleted, so the age was judged by the modification time")
	}
}

// TestPruneLeavesForeignFilesAlone pins that this module deletes only what it produced.
func TestPruneLeavesForeignFilesAlone(t *testing.T) {
	dir := t.TempDir()
	foreign := []string{
		"other.log",
		"app.log.1",
		"app-notatimestamp.log",
		"app-20260913.log",
		"app-20260913-120000.txt",
		"app-20260913-120000-x.log",
	}
	for _, name := range foreign {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not ours\n"), 0o600); err != nil {
			t.Fatalf("writing %s failed: %v", name, err)
		}
	}
	ours := makeArchive(t, dir, time.Now().AddDate(0, 0, -40), 0, "ours\n")

	newTestFile(t, filepath.Join(dir, "app.log"), 0, 1, 30, time.Now)

	for _, name := range foreign {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("%s was deleted, and this module did not produce it", name)
		}
	}
	if exists(ours) {
		t.Error("the over-age archive this module did produce survived, so nothing was pruned at all")
	}
}

// TestPruneNeverTouchesTheCurrentFile pins that the file being written to is never a candidate: it
// is the configured path, it carries no timestamp, and it falls outside the archive names.
func TestPruneNeverTouchesTheCurrentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	now := time.Now()
	for i := range 4 {
		makeArchive(t, dir, now.AddDate(0, 0, -40-i), 0, "stale\n")
	}
	rf := newTestFile(t, path, 0, 1, 1, time.Now)

	write(t, rf, "a record after the prune\n")
	rf.prune()

	if !exists(path) {
		t.Fatal("the active file was deleted by the prune")
	}
	if got := read(t, path); got != "a record after the prune\n" {
		t.Errorf("the active file holds %q, want the record written into it", got)
	}
}

// TestCountLimitKeepsTheNewest pins the ordering the count limit uses.
func TestCountLimitKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	plain := makeArchive(t, dir, stamp, 0, "first of the second\n")
	sequenced := makeArchive(t, dir, stamp, 1, "second of the second\n")
	older := makeArchive(t, dir, stamp.Add(-time.Hour), 0, "an hour earlier\n")

	newTestFile(t, filepath.Join(dir, "app.log"), 0, 1, 0, func() time.Time { return stamp })

	if !exists(sequenced) {
		t.Error("the newest archive of the second was deleted, so the count limit ordered the archives by name")
	}
	if exists(plain) {
		t.Error("the first archive of the second survived a count limit of one")
	}
	if exists(older) {
		t.Error("the oldest archive survived a count limit of one")
	}
}

// TestCloseStopsAndAwaitsBackgroundPrune pins that closing the writer ends the background pruning
// and waits for the pass in flight.
func TestCloseStopsAndAwaitsBackgroundPrune(t *testing.T) {
	dir := t.TempDir()
	rf, err := newRotatingFile(filepath.Join(dir, "app.log"), 0, 0, 1, time.Now, time.Millisecond)
	if err != nil {
		t.Fatalf("opening the file destination failed: %v", err)
	}
	w := &destWriter{id: "file:" + filepath.Join(dir, "app.log"), sink: rf}
	rf.startBackground(&w.mu)

	stale := makeArchive(t, dir, time.Now().AddDate(0, 0, -40), 0, "stale\n")
	waitFor(t, "the background prune to run once", func() bool { return !exists(stale) })

	w.close()

	select {
	case <-rf.done:
	default:
		t.Fatal("the close returned while the background prune was still running")
	}

	survivor := makeArchive(t, dir, time.Now().AddDate(0, 0, -40), 1, "written after the close\n")
	time.Sleep(50 * time.Millisecond)
	if !exists(survivor) {
		t.Error("an archive created after the close was deleted, so the background prune is still running")
	}
}

// TestBothLimitsOffStartsNoBackgroundPrune pins that a destination with nothing to prune starts no
// goroutine.
func TestBothLimitsOffStartsNoBackgroundPrune(t *testing.T) {
	dir := t.TempDir()
	rf := newTestFile(t, filepath.Join(dir, "app.log"), 1024, 0, 0, time.Now)

	var mu sync.Mutex
	rf.startBackground(&mu)
	if rf.stop != nil || rf.done != nil {
		t.Error("a destination with both pruning limits off started a background prune")
	}
	rf.stopBackground()
}

// TestPruneFailureIsReportedOnce pins that a prune which cannot even list the directory says so,
// once.
func TestPruneFailureIsReportedOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the log directory failed: %v", err)
	}
	rf := newTestFile(t, filepath.Join(dir, "app.log"), 0, 1, 0, time.Now)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the log directory failed: %v", err)
	}

	reported := captureStderr(t, func() {
		rf.prune()
		rf.prune()
		rf.prune()
	})
	if got := strings.Count(reported, "\n"); got != 1 {
		t.Errorf("three failed prunes wrote %d lines to standard error, want one:\n%s", got, reported)
	}
	if !strings.Contains(reported, rf.path) {
		t.Errorf("the report %q does not name the destination", reported)
	}
	requireOnePrefix(t, reported)
}

func requireOnePrefix(t *testing.T, reported string) {
	t.Helper()
	if !strings.HasPrefix(reported, "log: ") || strings.Contains(reported, "log: log:") {
		t.Errorf("the report %q does not start with the module's prefix exactly once", reported)
	}
}

// TestAnArchiveThatCannotBeDeletedIsReportedOnce is the other failure a prune can meet: the
// directory lists, and an archive in it cannot be removed.
func TestAnArchiveThatCannotBeDeletedIsReportedOnce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the superuser deletes from a directory without write permission")
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the log directory failed: %v", err)
	}
	rf := newTestFile(t, filepath.Join(dir, "app.log"), 0, 0, 1, time.Now)
	archive := filepath.Join(dir, "app-20200101-000000.log")
	if err := os.WriteFile(archive, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("creating the archive failed: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	reported := captureStderr(t, func() {
		rf.prune()
		rf.prune()
	})
	if got := strings.Count(reported, "\n"); got != 1 {
		t.Errorf("two failed deletions wrote %d lines to standard error, want one:\n%s", got, reported)
	}
	if !strings.Contains(reported, archive) {
		t.Errorf("the report %q does not name the archive", reported)
	}
	requireOnePrefix(t, reported)
}
