package settings

import (
	"context"
	"time"
)

func (s *settingsStore) startRefresh(ctx context.Context) {
	if s.config.refresh <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancelRefresh, s.refreshDone = cancel, done
	interval := s.config.refresh
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reload(ctx)
			}
		}
	}()
}

// stopRefresh cancels the ticker and waits for its goroutine, so a reload cannot
// outlive Stop and meet the closing database handle.
func (s *settingsStore) stopRefresh() {
	if s.cancelRefresh == nil {
		return
	}
	s.cancelRefresh()
	<-s.refreshDone
	s.cancelRefresh, s.refreshDone = nil, nil
}
