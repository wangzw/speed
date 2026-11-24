package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

const scenarioEnv = "SPEED_LOG_E2E_CASE"

const (
	caseDefault       = "default"
	caseFallback      = "fallback"
	caseEmptyOutputs  = "empty-outputs"
	caseRivalProvider = "rival-provider"
	caseConcurrent    = "concurrent"
)

const (
	infoMessage      = "default-info"
	debugMessage     = "default-debug"
	orphanMessage    = "orphan-record"
	injectedMessage  = "injected-record"
	silencedMessage  = "silenced-record"
	bootstrapMessage = "bootstrap-record"
	chainMessage     = "chain-record"
)

const (
	sensitiveKey   = "password"
	plaintextValue = "hunter2-PLAINTEXT"
)

const (
	concurrentRecords = 1000
	payloadSize       = 8000
)

func scenario() string { return os.Getenv(scenarioEnv) }

func runScenario(ctx context.Context, logger *slog.Logger) {
	switch scenario() {
	case caseDefault:
		logger.InfoContext(ctx, infoMessage, sensitiveKey, plaintextValue)
		logger.DebugContext(ctx, debugMessage)
	case caseFallback:
		log.FromContext(ctx).InfoContext(ctx, orphanMessage)
		injected := log.WithLogger(ctx, logger)
		log.FromContext(injected).InfoContext(injected, injectedMessage)
	case caseEmptyOutputs:
		logger.InfoContext(ctx, silencedMessage)
	case caseConcurrent:
		writeConcurrently(ctx, logger)
	default:
		say("loghost: no scenario given in " + scenarioEnv)
	}
}

func writeConcurrently(ctx context.Context, logger *slog.Logger) {
	payload := strings.Repeat("x", payloadSize)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range concurrentRecords {
			log.Default().InfoContext(ctx, bootstrapMessage, "payload", payload)
		}
	}()
	go func() {
		defer wg.Done()
		for range concurrentRecords {
			logger.InfoContext(ctx, chainMessage, "payload", payload)
		}
	}()
	wg.Wait()
}

const rivalModuleName = "log.rival"

func rivalLoggerModule() core.Module {
	return core.Module{
		Name:     rivalModuleName,
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
		Prepare: func(_ context.Context, _ *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(_ context.Context, _ *core.Registry) (any, error) {
			return rivalLogger{}, nil
		},
	}
}

type rivalLogger struct{}

var _ log.Logger = rivalLogger{}

func (rivalLogger) Named(string) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func (rivalLogger) Redaction() log.Redaction { return rivalRedaction{} }

type rivalRedaction struct{}

func (rivalRedaction) AddKeys(...string)              {}
func (rivalRedaction) AddPattern(string, log.Matcher) {}
