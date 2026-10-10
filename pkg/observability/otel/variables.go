package otel

import (
	"log/slog"
	"strings"
)

// overriddenVariable is one standard variable the upstream SDK reads for a
// setting this module gives it explicitly, and the input item that gives it.
type overriddenVariable struct {
	name string
	item string
}

// overriddenVariables are the standard variables that have no effect under
// this module, in the order the warning names them.
//
// The upstream SDK reads them in go.opentelemetry.io/otel/sdk: the sampler
// pair in trace/sampler_env.go, the batch processor's four in
// trace/internal/env/env.go, and the metric reader's two in
// go.opentelemetry.io/otel/sdk/metric's env.go. It applies them first and the
// options after, and newProviders gives every one of those settings as an
// option. The variables the SDK reads for what this module leaves to it are
// not here, because they still apply: OTEL_SERVICE_NAME where service-name is
// not configured, OTEL_RESOURCE_ATTRIBUTES beneath the attributes this module
// gives, the span attribute, event and link limits, the exemplar filter and the
// cardinality limit. A configured service-name replaces the name either of the
// first two gives, and that is not warned about.
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

// ignoredVariables lists the overridden variables lookup finds set, each with
// the item to set instead.
//
// A variable counts as set when its value is not empty once whitespace is
// trimmed. What the SDK makes of a blank value differs by variable, a report
// of an invalid one included; the configured setting is in force whichever it
// is.
func ignoredVariables(lookup func(string) (string, bool)) []string {
	var set []string
	for _, v := range overriddenVariables {
		if value, ok := lookup(v.name); ok && strings.TrimSpace(value) != "" {
			set = append(set, v.name+": "+v.item)
		}
	}
	return set
}

// warnIgnoredVariables writes one line naming the overridden variables that
// are set, and nothing when none is.
//
// Without it a deployment that sets one sees the configured value in force and
// no word about the variable. The line names the variables and never repeats
// a value: what a variable holds is the deployment's, and the name is enough
// to find it.
func warnIgnoredVariables(lg *slog.Logger, lookup func(string) (string, bool)) {
	set := ignoredVariables(lookup)
	if len(set) == 0 {
		return
	}
	lg.Warn("standard OpenTelemetry variables are set and have no effect: this module gives the upstream "+
		"those settings from its configuration; each entry names a variable and the item to set instead",
		"ignored", set)
}
