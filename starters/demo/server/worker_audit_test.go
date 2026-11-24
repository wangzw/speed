package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	auditjournal "github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/log"
)

const workerAuditAction = "test.worker.audit"

func workerAuditWriterModule() core.Module {
	return core.Module{
		Name:     "test.worker-audit-writer",
		Requires: []core.Requirement{{Token: (*audit.Audit)(nil)}},
		Start: func(ctx context.Context, reg *core.Registry, _ any) error {
			recorder, err := core.Resolve[audit.Audit](reg)
			if err != nil {
				return err
			}
			return recorder.Record(ctx, audit.Entry{
				Action:  workerAuditAction,
				Object:  "worker",
				Outcome: "success",
			})
		},
	}
}

// TestTheWorkerBaselineRecordsTheAuditRow holds that the worker roster records a seam entry with no
// router.
func TestTheWorkerBaselineRecordsTheAuditRow(t *testing.T) {
	t.Parallel()
	if !slices.Contains(baselineWorker, auditjournal.ModuleName) {
		t.Fatalf("baselineWorker does not name %q", auditjournal.ModuleName)
	}

	registry := core.New()
	registry.Register(config.Module())
	registry.Register(sqlite.Module())
	registry.Register(log.Module())
	registry.Register(eventmemory.Module())

	names := slices.Clone(baselineWorker)
	var descriptors []core.Module
	for _, module := range core.ProcessRegistry.Modules() {
		if !slices.Contains(names, module.Name) {
			continue
		}
		descriptors = append(descriptors, module)
		registry.Register(module)
	}
	registry.Register(workerAuditWriterModule())

	dsn := filepath.Join(t.TempDir(), "worker.db")
	body := fmt.Sprintf(
		`{"db":{"sqlite":{"dsn":%q}},"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`,
		dsn, filepath.Join(t.TempDir(), "worker-log.json"))
	reader := authntest.ConfigReaderAll(t, body,
		append([]core.Module{sqlite.Module(), log.Module(), eventmemory.Module()}, descriptors...)...)
	registry.Supply(reader, config.ModuleName)

	participants := append(slices.Clone(names),
		sqlite.ModuleName, log.ModuleName, eventmemory.ModuleName, workerAuditWriterModule().Name)
	err := registry.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: participants,
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			var rows int64
			if err := database.DB().
				Raw("SELECT COUNT(*) FROM audit_entries WHERE action = ?", workerAuditAction).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("reading the worker's audit row: %w", err)
			}
			if rows != 1 {
				return fmt.Errorf("the worker recorded %d audit row(s), want one", rows)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the worker baseline: %v", err)
	}
}
