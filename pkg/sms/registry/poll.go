package registry

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/sms"
)

// The background pass does two jobs on one interval.
//
// The first is the backstop under the change events: events are the fast path,
// a write reloads its own replica at once and the bus carries the news to the
// others, but the bus promises no delivery count, so a replica that never
// receives one would otherwise serve a stale set until it restarted. The
// second is the vendor's review: a runtime template is taken up pending in every
// language, and each language's state changes on the vendor's own schedule, so
// the pass reads it back.
//
// A poll interval of zero turns the pass off. That is a deliberate choice a
// host can make: it accepts convergence through events alone, and with it the
// possibility that a change this replica never hears about, or a review that
// comes back, stays invisible until the process restarts.

// startPoll begins the background pass unless the configured interval is zero.
// It runs its first pass at once rather than after one interval, so a template
// this replica already holds pending does not wait a whole interval for its
// first read.
func (s *registryStore) startPoll(ctx context.Context) {
	if s.config.poll <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancelPoll, s.pollDone = cancel, done
	go func() {
		defer close(done)
		s.pollOnce(ctx)
		ticker := time.NewTicker(s.config.poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pollOnce(ctx)
			}
		}
	}()
}

// stopPoll cancels the poller and waits for its goroutine to return.
//
// The wait is what keeps the pass from outliving Stop: a vendor call or a read
// in flight at that point would otherwise meet the database handle being closed
// or the provider being released, and report a failure on an ordinary shutdown.
func (s *registryStore) stopPoll() {
	if s.cancelPoll == nil {
		return
	}
	s.cancelPoll()
	<-s.pollDone
	s.cancelPoll, s.pollDone = nil, nil
}

// pollOnce runs one pass: reload the set, then read the vendor's state for
// every runtime template that still has a language awaiting its verdict.
func (s *registryStore) pollOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.reload(ctx)
	current := s.snapshot.Load()
	if current == nil {
		return
	}
	// The names are collected first so a reload the pass itself triggers does
	// not change the set it is walking.
	names := make([]string, 0, len(current.byName))
	for name, rec := range current.byName {
		if hasPendingLanguage(rec) {
			names = append(names, name)
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		rec, ok := s.snapshot.Load().byName[name]
		if !ok || !hasPendingLanguage(rec) {
			// A write since the pass began already settled every language.
			continue
		}
		s.pollTemplate(ctx, rec)
	}
}

// hasPendingLanguage reports whether any language a template names is still
// awaiting the vendor's verdict. An untracked state is not pending: the process
// does not read back a review it never took up. NotRequired is not pending
// either: the vendor has no such review, so there is nothing to read.
func hasPendingLanguage(rec runtimeTemplate) bool {
	for _, locale := range rec.locales {
		if rec.approvals[locale].State == sms.ApprovalPending {
			return true
		}
	}
	return false
}

// pollTemplate reads the vendor's review state for every language of one runtime
// template that is still pending, and records each verdict.
//
// The review is per language, so every pending language is asked about on its own
// and its verdict written back on its own: a language approved does not wait for
// another still running. A vendor call that fails leaves that language's state as
// it is — the failure is this replica's to retry, and recording a state the
// vendor did not answer would refuse a template the vendor holds or, worse,
// approve one it does not — while the other languages are still asked about. A
// verdict equal to the stored one writes nothing, so a review still running does
// not churn the row every interval.
func (s *registryStore) pollTemplate(ctx context.Context, rec runtimeTemplate) {
	verdicts := make(map[string]sms.Approval)
	for _, locale := range rec.locales {
		if ctx.Err() != nil {
			return
		}
		if rec.approvals[locale].State != sms.ApprovalPending {
			continue
		}
		state, err := s.provider.Approval(ctx, rec.codes[locale])
		if err != nil {
			s.logger.Error("sms.registry: reading a template's review state from the carrier failed; "+
				"the state stays as it is", "template", rec.name, "language", locale, "error", err)
			continue
		}
		verdicts[locale] = state
	}
	if len(verdicts) == 0 {
		return
	}
	s.recordVerdicts(ctx, rec.name, verdicts)
}

// recordVerdicts writes back the verdicts a pass read, one language at a time,
// merging into the row under a version check and retrying a lost race.
func (s *registryStore) recordVerdicts(ctx context.Context, name string, verdicts map[string]sms.Approval) {
	for attempt := 0; attempt < upsertAttempts; attempt++ {
		retry, changed, err := s.recordVerdictsOnce(ctx, name, verdicts)
		if err != nil {
			s.logger.Error("sms.registry: recording a template's review state failed", "template", name, "error", err)
			return
		}
		if retry {
			continue
		}
		if changed {
			s.after(ctx, name, false)
		}
		return
	}
	s.logger.Error("sms.registry: recording a template's review state kept losing the write race", "template", name)
}

// recordVerdictsOnce is one attempt of recordVerdicts. It reads the row, applies
// the verdicts whose language the row still holds and whose state differs from
// the stored one, and writes them back conditionally on the version read. retry
// is true when the write matched no row; changed is true when a verdict differed
// and the write landed.
func (s *registryStore) recordVerdictsOnce(ctx context.Context, name string, verdicts map[string]sms.Approval) (retry, changed bool, err error) {
	err = s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var rows []row
		if readErr := tx.Raw("SELECT "+runtimeColumns+" FROM "+tableName+
			" WHERE carrier = ? AND name = ?", s.provider.Name(), name).Scan(&rows).Error; readErr != nil {
			return readErr
		}
		if len(rows) == 0 {
			// The template was removed while the read was in flight.
			return nil
		}
		rec, decodeErr := decodeRuntime(rows[0])
		if decodeErr != nil {
			return decodeErr
		}
		merged := cloneApprovals(rec.approvals)
		for locale, verdict := range verdicts {
			_, hasCode := rec.codes[locale]
			_, hasBody := rec.bodies[locale]
			if !hasCode && !hasBody {
				// The language was not there when the verdict was read.
				continue
			}
			if existing, held := merged[locale]; held && existing == verdict {
				continue
			}
			merged[locale] = verdict
			changed = true
		}
		if !changed {
			return nil
		}
		now := time.Now().UTC()
		sql := "UPDATE " + tableName + " SET approvals = ?, version = version + 1, updated_at = ? " +
			"WHERE carrier = ? AND name = ? AND version = ? RETURNING version"
		_, written, writeErr := insertVersion(tx, sql, encodeApprovals(merged), now, s.provider.Name(), name,
			rec.version)
		if writeErr != nil {
			return writeErr
		}
		if !written {
			retry, changed = true, false
		}
		return nil
	})
	return retry, changed, err
}
