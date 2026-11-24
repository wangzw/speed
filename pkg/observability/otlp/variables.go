package otlp

import (
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

type overriddenVariable struct {
	name string
	item string
}

const fixedByThisModule = "none, fixed by this module"

// overTLS names a certificate item together with insecure, which loadTLS
// requires to be false wherever a certificate item is set.
func overTLS(item string) string {
	return item + ", with " + insecureKey + " false"
}

// overriddenVariables are the standard variables that have no effect
// under this module, in the order the warning names them.
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

func ignoredVariables(lookup func(string) (string, bool)) []string {
	var set []string
	for _, v := range overriddenVariables {
		if value, ok := lookup(v.name); ok && strings.TrimSpace(value) != "" {
			set = append(set, v.name+": "+v.item)
		}
	}
	return set
}

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
