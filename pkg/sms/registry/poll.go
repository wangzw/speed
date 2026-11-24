package registry

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/sms"
)

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

// stopPoll cancels the poller and waits for its goroutine, so the pass cannot
// outlive Stop.
func (s *registryStore) stopPoll() {
	if s.cancelPoll == nil {
		return
	}
	s.cancelPoll()
	<-s.pollDone
	s.cancelPoll, s.pollDone = nil, nil
}

func (s *registryStore) pollOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.reload(ctx)
	current := s.snapshot.Load()
	if current == nil {
		return
	}
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
			continue
		}
		s.pollTemplate(ctx, rec)
	}
}

func hasPendingLanguage(rec runtimeTemplate) bool {
	for _, locale := range rec.locales {
		if rec.approvals[locale].State == sms.ApprovalPending {
			return true
		}
	}
	return false
}

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

func (s *registryStore) recordVerdictsOnce(ctx context.Context, name string, verdicts map[string]sms.Approval) (retry, changed bool, err error) {
	err = s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var rows []row
		if readErr := tx.Raw("SELECT "+runtimeColumns+" FROM "+tableName+
			" WHERE carrier = ? AND name = ?", s.provider.Name(), name).Scan(&rows).Error; readErr != nil {
			return readErr
		}
		if len(rows) == 0 {
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
