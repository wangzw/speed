package otlp

import (
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// overriddenVariable is one standard variable the upstream exporters read for
// a setting build gives them explicitly, and the input item that gives it.
type overriddenVariable struct {
	name string
	item string
}

// fixedByThisModule stands in for an item where the setting is part of the wire
// shape this module documents and no input item changes it.
const fixedByThisModule = "none, fixed by this module"

// overTLS names a certificate item together with insecure, which loadTLS
// requires to be false wherever a certificate item is set. The upstream reads
// its certificate variables as TLS settings, while insecure is true by default
// here, so the item alone would fail the preparation.
func overTLS(item string) string {
	return item + ", with " + insecureKey + " false"
}

// overriddenVariables are the standard variables that have no effect under
// this module, in the order the warning names them.
//
// The upstream exporters read them in their internal envconfig.go, in
// internal/otlpconfig for otlptracegrpc and otlptracehttp and in
// internal/oconf for otlpmetricgrpc and otlpmetrichttp, each name the suffix
// given there joined to OTEL_EXPORTER_OTLP with an underscore. They apply the
// variables first and the options after, and build gives every one of these
// settings as an option except the protocol: over HTTP the encoding option is
// the protocol and overrides it, and over gRPC the upstream reads the protocol
// variable into a field its client never uses. The metric exporters read no
// protocol variable. The
// timeout and compression variables are not here, because build leaves those
// settings to the upstream and the variables still apply.
var overriddenVariables = []overriddenVariable{
	{"OTEL_EXPORTER_OTLP_ENDPOINT", endpointKey},
	{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpointKey},
	{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", endpointKey},
	{"OTEL_EXPORTER_OTLP_HEADERS", headersKey},
	{"OTEL_EXPORTER_OTLP_TRACES_HEADERS", headersKey},
	{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", headersKey},
	{"OTEL_EXPORTER_OTLP_PROTOCOL", protocolKey},
	{"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", protocolKey},
	{"OTEL_EXPORTER_OTLP_INSECURE", insecureKey},
	{"OTEL_EXPORTER_OTLP_TRACES_INSECURE", insecureKey},
	{"OTEL_EXPORTER_OTLP_METRICS_INSECURE", insecureKey},
	{"OTEL_EXPORTER_OTLP_CERTIFICATE", overTLS(caFileKey)},
	{"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", overTLS(caFileKey)},
	{"OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE", overTLS(caFileKey)},
	{"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", overTLS(clientCertFileKey)},
	{"OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE", overTLS(clientCertFileKey)},
	{"OTEL_EXPORTER_OTLP_METRICS_CLIENT_CERTIFICATE", overTLS(clientCertFileKey)},
	{"OTEL_EXPORTER_OTLP_CLIENT_KEY", overTLS(clientKeyFileKey)},
	{"OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", overTLS(clientKeyFileKey)},
	{"OTEL_EXPORTER_OTLP_METRICS_CLIENT_KEY", overTLS(clientKeyFileKey)},
	{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", fixedByThisModule},
	{"OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", fixedByThisModule},
}

// ignoredVariables lists the overridden variables lookup finds set, each with
// the item to set instead.
//
// A variable counts as set when its value is not empty once whitespace is
// trimmed, which is how the upstream exporters read a blank one: as not given.
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
// Without it a deployment that sets one, a collector's endpoint or its
// credential header, sees the configured value in force and no word about the
// variable. The line names the variables and never repeats a value: a header
// variable carries a credential.
//
// The logger is an optional dependency. Without one the line is not written,
// rather than written through the process root logger.
func warnIgnoredVariables(reg *core.Registry, lookup func(string) (string, bool)) {
	set := ignoredVariables(lookup)
	if len(set) == 0 {
		return
	}
	logger, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	logger.Named(ModuleName).Warn("standard OpenTelemetry variables are set and have no effect: this module "+
		"gives the upstream those settings from its configuration; each entry names a variable and the item "+
		"to set instead", "ignored", set)
}
