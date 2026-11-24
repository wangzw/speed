package main

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

const envPrefix = "LOGHOST"

const appModuleName = "app"

func init() {
	core.ProcessRegistry.Register(hostModule())
	core.ProcessRegistry.Register(appModule())
}

func hostModule() core.Module {
	return core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}

func appModule() core.Module {
	return core.Module{
		Name:     appModuleName,
		Requires: []core.Requirement{{Token: (*log.Logger)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			capability, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			capability.Redaction().AddKeys(sensitiveKey)
			return &application{logger: capability.Named(appModuleName)}, nil
		},
		Serve: func(ctx context.Context, _ *core.Registry, instance any) error {
			//nolint:errcheck // the instance is what this module's own New
			// returned, so a different type would be a defect in the host,
			// not a case to handle: the panic is the intended report.
			a := instance.(*application)
			runScenario(ctx, a.logger)
			finish()
			return nil
		},
	}
}

type application struct {
	logger *slog.Logger
}
