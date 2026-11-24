package otel

import (
	"log/slog"
	"strings"
)

type overriddenVariable struct {
	name string
	item string
}

// overriddenVariables are the standard variables that have no effect
// under this module, in the order the warning names them.
var overriddenVariables = []overriddenVariable{
	{"OTEL_TRACES_SAMPLER", ModuleName + ".sample-ratio"},
	{"OTEL_TRACES_SAMPLER_ARG", ModuleName + ".sample-ratio"},
	{"OTEL_BSP_SCHEDULE_DELAY", ModuleName + ".batch.batch-timeout"},
	{"OTEL_BSP_EXPORT_TIMEOUT", ModuleName + ".batch.export-timeout"},
	{"OTEL_BSP_MAX_QUEUE_SIZE", ModuleName + ".batch.max-queue-size"},
	{"OTEL_BSP_MAX_EXPORT_BATCH_SIZE", ModuleName + ".batch.max-export-batch-size"},
	{"OTEL_METRIC_EXPORT_INTERVAL", ModuleName + ".metric.interval"},
	{"OTEL_METRIC_EXPORT_TIMEOUT", ModuleName + ".metric.timeout"},
}

func ignoredVariables(lookup func(string) (string, bool)) []string {
	var set []string
	for _, v := range overriddenVariables {
		if value, ok := lookup(v.name); ok && strings.TrimSpace(value) != "" {
			set = append(set, v.name+": "+v.item)
		}
	}
	return set
}

func warnIgnoredVariables(lg *slog.Logger, lookup func(string) (string, bool)) {
	set := ignoredVariables(lookup)
	if len(set) == 0 {
		return
	}
	lg.Warn("standard OpenTelemetry variables are set and have no effect: this module gives the upstream "+
		"those settings from its configuration; each entry names a variable and the item to set instead",
		"ignored", set)
}
