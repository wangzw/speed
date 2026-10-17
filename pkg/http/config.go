package http

import (
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/config"
)

// configNamespace is where this module's input items are mounted in the config
// data. It is a constant of its own rather than the module name: input items
// hang from the namespace a schema declares, and the module name takes no part
// in those paths.
const configNamespace = "http"

// configPath is the complete path of the carrier struct, which is what Decode
// is given.
const configPath = configNamespace

// listenersItem is the key of the listener set, relative to the namespace. The
// startup failure for a host that configured none names it, so the reader is
// told which key to write.
const listenersItem = "listeners"

// endpointsItem is the key of the endpoint set, relative to the namespace.
const endpointsItem = "endpoints"

// reservedEndpointName is the endpoint name configuration cannot declare. A
// list of endpoint names elsewhere writes it to mean every declared endpoint,
// and an endpoint of that name would be indistinguishable from the wildcard.
const reservedEndpointName = "*"

// moduleConfig is the set of input items this module accepts. It is not
// exported: the surface carries net/http's types and this package's own, and a
// configuration struct on it would make a host's file shape part of the API.
type moduleConfig struct {
	// Listeners is one entry per listening socket, keyed by the listener name
	// endpoints reference. A listener is one address, one net.Listener and one
	// net/http.Server; the endpoints hanging on it share that socket.
	Listeners map[string]listenerConfig `config:"listeners"`
	// Endpoints is one entry per routing endpoint, keyed by the endpoint name
	// registrants use.
	Endpoints map[string]endpointConfig `config:"endpoints"`
}

// listenerConfig is one listening socket as configuration gives it.
//
// The durations are pointers because the listener set is a container leaf: the
// primary source replaces the whole map rather than merging into it, so a value
// type could not tell "the key is absent" from "the key is 0", and 0 is the
// legal way to switch most of the timeouts off. A duration is written as a
// string time.ParseDuration accepts; config refuses a bare number, because 300
// would read as five minutes and mean three hundred nanoseconds. Two of the
// durations refuse an explicit 0 instead of switching off, and resolve says
// which and why.
type listenerConfig struct {
	// Address is the address to listen on, in net.Listen's form, for
	// example ":8080" or "127.0.0.1:8080".
	Address string `config:"address"`

	// ReadHeaderTimeout bounds how long the request headers may take to
	// arrive. It is one of the two durations that cannot be switched off: a
	// server with no header timeout is held open indefinitely by a client
	// that sends its headers one byte at a time.
	ReadHeaderTimeout *time.Duration `config:"read-header-timeout"`
	// ReadTimeout bounds the whole request read, 0 for no bound.
	ReadTimeout *time.Duration `config:"read-timeout"`
	// WriteTimeout bounds the response write, 0 for no bound.
	WriteTimeout *time.Duration `config:"write-timeout"`
	// IdleTimeout bounds how long a kept-alive connection may sit idle, 0
	// for no bound.
	IdleTimeout *time.Duration `config:"idle-timeout"`
	// DrainTimeout is how long the shutdown waits for this listener's
	// in-flight requests. It is the other duration that cannot be switched
	// off: it is the only bound the drain has, because the context Stop and
	// Close receive has its cancellation stripped.
	DrainTimeout *time.Duration `config:"drain-timeout"`

	// TLSCertFile and TLSKeyFile are where the certificate and its key are
	// read from. Both empty serves plain HTTP; one of them alone is a
	// startup failure. Where the certificate comes from and when it is
	// replaced are outside this module.
	TLSCertFile string `config:"tls-cert-file"`
	TLSKeyFile  string `config:"tls-key-file"`

	// Unknown collects the keys of the entry that name none of the fields
	// above. Configuration checks no key inside a container leaf, so without
	// it a misspelled drain-timeout would vanish and the default would govern
	// a listener its operator believed configured otherwise; resolve refuses
	// the entry instead.
	Unknown map[string]any `config:",remain"`
}

// endpointConfig is one routing endpoint as configuration gives it.
//
// An endpoint hangs on a listener by name and carries the settings that belong
// to its own chain rather than to the socket: the request body limit its body
// limit layer imposes, and the proxy networks and forwarded-address headers its
// gin engine is built with.
type endpointConfig struct {
	// Listener names the listener this endpoint's socket is. The name has to
	// match an entry under the listener set; resolve refuses one that does
	// not.
	Listener string `config:"listener"`

	// Prefixes are the path prefixes this endpoint claims on its listener.
	// The dispatch selects an endpoint by these claims alone: a request falls
	// under the longest claimed prefix, and the endpoint that claimed it
	// answers. Omitting the key claims "/", which makes the endpoint its
	// listener's fallback; "/" does not take part in longest matching. An
	// explicit empty list is refused, because such an endpoint would receive
	// no request at all.
	Prefixes []string `config:"prefixes"`

	// MaxBodyBytes is the request body limit the body limit layer of the
	// chain imposes, 0 for no limit. It applies to every handler on the
	// endpoint, whatever way a handler reads the body.
	MaxBodyBytes *int64 `config:"max-body-bytes"`

	// TrustedProxies are the networks in front of this endpoint, each an IP
	// address or a CIDR block, whose forwarded-address headers are believed
	// when the client address is worked out. Empty trusts nobody, which is the
	// safe default: with nobody trusted, a client cannot choose the address
	// Context.ClientIP reports by writing a header.
	TrustedProxies []string `config:"trusted-proxies"`
	// RemoteIPHeaders name the headers a trusted proxy writes the client
	// address into, in the order they are read. Empty leaves gin's default
	// (X-Forwarded-For then X-Real-IP), which is only consulted for a request
	// whose peer is one of TrustedProxies.
	RemoteIPHeaders []string `config:"remote-ip-headers"`

	// Unknown collects the keys of the entry that name none of the fields
	// above. Configuration checks no key inside a container leaf, so without
	// it a misspelled body limit would vanish and the default would govern an
	// endpoint its operator believed configured otherwise; resolve refuses the
	// entry instead.
	Unknown map[string]any `config:",remain"`
}

// The defaults of the optional per-listener and per-endpoint items, applied
// after decoding rather than laid into the prototype: a container leaf is
// replaced as a whole, so the prototype's values do not survive a host writing
// the listener or endpoint set.
const (
	defaultReadHeaderTimeout = 10 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultDrainTimeout      = 30 * time.Second
	defaultMaxBodyBytes      = int64(1 << 20)
)

// configDefaults is the prototype this module declares. The listener and
// endpoint sets are empty in it: a default listener would open a port on every
// host that never asked for one, and the "no listener is configured" startup
// failure could never be taken.
var configDefaults = moduleConfig{}

// schema is this module's input item declaration.
//
// The listener set and the endpoint set take the primary config source alone.
// Each is a map, and such a leaf has no flat form the environment or the
// command line could give; declaring either of them on such a source is
// ErrInvalidSchema. The two sets are therefore changed in the config file or the
// remote config centre, never by an environment variable.
func schema() config.Schema {
	return config.Schema{
		Namespace: configNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			listenersItem: {
				Origins:     config.OriginPrimary,
				Description: "the listening sockets, each with its own address, timeouts and TLS",
			},
			endpointsItem: {
				Origins:     config.OriginPrimary,
				Description: "the routing endpoints, each hanging on a listener and carrying its chain settings",
			},
		},
	}
}

// listenerSettings is one listener's configuration, validated and completed:
// every optional item carries a number, and a 0 means that item is switched
// off, which is also what an explicit 0 in the configuration means. The header
// timeout and the drain timeout never arrive as 0 — an explicit 0 for either is
// refused before any of this is built.
type listenerSettings struct {
	name    string
	address string

	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	drainTimeout      time.Duration

	tlsCertFile string
	tlsKeyFile  string
}

// tls reports whether this listener serves over TLS.
func (s listenerSettings) tls() bool { return s.tlsCertFile != "" || s.tlsKeyFile != "" }

// endpointSettings is one endpoint's configuration, validated and completed:
// the body limit carries a number, and the proxy settings are as written. The
// endpoint remembers the listener it hangs on by name.
type endpointSettings struct {
	name         string
	listenerName string

	prefixes []string

	maxBodyBytes int64

	trustedProxies  []string
	remoteIPHeaders []string
}

// resolve validates the configuration and fills in the optional items,
// returning the listeners and the endpoints, each in name order. The order is
// fixed so that a startup failure, a diagnostic line and the order in which
// addresses are bound do not depend on Go's map iteration. Every endpoint's
// listener reference is checked here, against the listeners the same
// configuration declared.
func (c moduleConfig) resolve() ([]listenerSettings, []endpointSettings, error) {
	listeners := make([]listenerSettings, 0, len(c.Listeners))
	listenerNames := make(map[string]struct{}, len(c.Listeners))
	for _, name := range slices.Sorted(maps.Keys(c.Listeners)) {
		resolved, err := c.Listeners[name].resolve(name)
		if err != nil {
			return nil, nil, err
		}
		listeners = append(listeners, resolved)
		listenerNames[name] = struct{}{}
	}

	endpoints := make([]endpointSettings, 0, len(c.Endpoints))
	for _, name := range slices.Sorted(maps.Keys(c.Endpoints)) {
		resolved, err := c.Endpoints[name].resolve(name, listenerNames, slices.Sorted(maps.Keys(c.Listeners)))
		if err != nil {
			return nil, nil, err
		}
		endpoints = append(endpoints, resolved)
	}
	// One prefix belongs to one endpoint on a listener. The check is across
	// entries, so it cannot live in either single-entry resolve, and it sees
	// the defaulted claims: two endpoints that both omitted prefixes both
	// claim "/" and are refused here.
	if err := checkClaims(endpoints); err != nil {
		return nil, nil, err
	}
	return listeners, endpoints, nil
}

// checkClaims refuses a prefix claimed by two endpoints on one listener, the
// root included. Endpoints arrive in name order, so the pair the message names
// is the same one every run.
func checkClaims(endpoints []endpointSettings) error {
	claimed := make(map[string]map[string]string, len(endpoints))
	for _, e := range endpoints {
		byPrefix := claimed[e.listenerName]
		if byPrefix == nil {
			byPrefix = make(map[string]string)
			claimed[e.listenerName] = byPrefix
		}
		for _, prefix := range e.prefixes {
			if previous, taken := byPrefix[prefix]; taken {
				// The example has to be a fix, so it depends on the prefix:
				// "/" is what an endpoint that omits prefixes claims, and
				// claiming "/api" instead moves it off the root; any other
				// prefix is already written down, so the example moves one of
				// the two endpoints deeper rather than repeating the value that
				// conflicts. A valid non-root prefix accepts another segment,
				// so the example is always a legal claim.
				if prefix == "/" {
					return fmt.Errorf(
						"%w: listener %q: the endpoints %q and %q both claim the path prefix %q. "+
							"A listener selects the endpoint by the longest prefix a request falls "+
							"under, so one prefix belongs to one endpoint. Give every endpoint its own "+
							"prefixes, for example write %s.%s.%s.prefixes: [\"/api\"]; an endpoint that "+
							"omits prefixes claims \"/\", so at most one endpoint on a listener may omit it",
						ErrClaimConflict, e.listenerName, previous, e.name, prefix,
						configNamespace, endpointsItem, e.name)
				}
				return fmt.Errorf(
					"%w: listener %q: the endpoints %q and %q both claim the path prefix %q. "+
						"A listener selects the endpoint by the longest prefix a request falls "+
						"under, so one prefix belongs to one endpoint. Give every endpoint its own "+
						"prefixes: write a different prefix on one of them, for example "+
						"%s.%s.%s.prefixes: [%q]",
					ErrClaimConflict, e.listenerName, previous, e.name, prefix,
					configNamespace, endpointsItem, e.name, prefix+"/b")
			}
			byPrefix[prefix] = e.name
		}
	}
	return nil
}

// resolve validates one listener. Every message names the listener and the key
// to change: a startup failure that only says "missing address" leaves the host
// to work out which of its listeners it came from.
func (l listenerConfig) resolve(name string) (listenerSettings, error) {
	if len(l.Unknown) > 0 {
		unknown := slices.Sorted(maps.Keys(l.Unknown))
		return listenerSettings{}, fmt.Errorf(
			"%w: listener %q gives %s, which %s not among the keys a listener takes: %s. "+
				"Correct the spelling under %s.%s.%s",
			ErrInvalidConfig, name, quotedList(unknown), pluralVerb(len(unknown)),
			quotedList(listenerKeys()), configNamespace, listenersItem, name)
	}
	if l.Address == "" {
		return listenerSettings{}, fmt.Errorf(
			"%w: listener %q gives no address to listen on. Write %s.%s.%s.address, "+
				"for example \":8080\"", ErrInvalidConfig, name, configNamespace, listenersItem, name)
	}

	// A negative duration is not the same mistake as 0: 0 switches the item
	// off on purpose, while a negative one is a typo that net/http would
	// treat as an already-expired deadline and fail every request with.
	durations := [...]struct {
		key   string
		given *time.Duration
	}{
		{"read-header-timeout", l.ReadHeaderTimeout},
		{"read-timeout", l.ReadTimeout},
		{"write-timeout", l.WriteTimeout},
		{"idle-timeout", l.IdleTimeout},
		{"drain-timeout", l.DrainTimeout},
	}
	for _, d := range durations {
		if d.given != nil && *d.given < 0 {
			return listenerSettings{}, fmt.Errorf(
				"%w: listener %q gives %s the value %s; 0 switches the bound off and a "+
					"positive duration sets it", ErrInvalidConfig, name, d.key, *d.given)
		}
	}
	// Two of the durations refuse an explicit 0 rather than reading it as
	// "switch this bound off". The criterion is who carries the consequence
	// of switching the item off: for the rest it falls on the endpoint that
	// asked for it, and for these two it falls on the host's ability to go
	// on serving and to stop at all.
	cannotBeSwitchedOff := [...]struct {
		key    string
		given  *time.Duration
		def    time.Duration
		reason string
	}{
		{
			"read-header-timeout", l.ReadHeaderTimeout, defaultReadHeaderTimeout,
			"without it a client that sends its headers one byte at a time holds a " +
				"connection open indefinitely, and the listener goes on reporting that " +
				"it accepts requests while the connections pile up",
		},
		{
			"drain-timeout", l.DrainTimeout, defaultDrainTimeout,
			"without it the shutdown waits for ever on an in-flight request that cannot " +
				"finish: the context Stop and Close receive has its cancellation " +
				"stripped and the registry imposes no shutdown timeout of its own, so a " +
				"host blocked in Run is left with nothing to interrupt it with",
		},
	}
	for _, d := range cannotBeSwitchedOff {
		if d.given != nil && *d.given == 0 {
			return listenerSettings{}, fmt.Errorf(
				"%w: listener %q switches %s off with 0, and it is one of the two bounds "+
					"that have to stay on: %s. Give %s.%s.%s.%s a positive duration, or drop "+
					"the key to take the default of %s",
				ErrInvalidConfig, name, d.key, d.reason, configNamespace, listenersItem, name,
				d.key, d.def)
		}
	}
	if (l.TLSCertFile == "") != (l.TLSKeyFile == "") {
		return listenerSettings{}, fmt.Errorf(
			"%w: listener %q gives only one of tls-cert-file and tls-key-file. Give both to "+
				"serve over TLS, or neither to serve plain HTTP", ErrInvalidConfig, name)
	}

	return listenerSettings{
		name:              name,
		address:           l.Address,
		readHeaderTimeout: valueOrDefault(l.ReadHeaderTimeout, defaultReadHeaderTimeout),
		readTimeout:       valueOrDefault(l.ReadTimeout, defaultReadTimeout),
		writeTimeout:      valueOrDefault(l.WriteTimeout, defaultWriteTimeout),
		idleTimeout:       valueOrDefault(l.IdleTimeout, defaultIdleTimeout),
		drainTimeout:      valueOrDefault(l.DrainTimeout, defaultDrainTimeout),
		tlsCertFile:       l.TLSCertFile,
		tlsKeyFile:        l.TLSKeyFile,
	}, nil
}

// resolve validates one endpoint. Every message names the endpoint and the key
// to change, and an endpoint naming a listener the configuration never declared
// is refused here, naming the endpoint and the listeners that do exist, because
// picking one of them is the fixing action.
func (e endpointConfig) resolve(name string, listeners map[string]struct{}, listenerNames []string) (endpointSettings, error) {
	if name == reservedEndpointName {
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q carries a name this module reserves: a list of endpoint names "+
				"elsewhere writes it to mean every declared endpoint, so no endpoint can be "+
				"called that. Rename the entry %q under %s.%s",
			ErrInvalidConfig, name, name, configNamespace, endpointsItem)
	}
	if len(e.Unknown) > 0 {
		unknown := slices.Sorted(maps.Keys(e.Unknown))
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q gives %s, which %s not among the keys an endpoint takes: %s. "+
				"Correct the spelling under %s.%s.%s",
			ErrInvalidConfig, name, quotedList(unknown), pluralVerb(len(unknown)),
			quotedList(endpointKeys()), configNamespace, endpointsItem, name)
	}
	if e.Listener == "" {
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q names no listener to hang on. Write %s.%s.%s.listener, one of "+
				"the names under %s.%s", ErrInvalidConfig, name, configNamespace, endpointsItem,
			name, configNamespace, listenersItem)
	}
	if _, ok := listeners[e.Listener]; !ok {
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q hangs on the listener %q, which %s.%s does not declare. The "+
				"listeners this configuration declares are %s. Either point the endpoint at one "+
				"of them or add %q under %s.%s",
			ErrUnknownListener, name, e.Listener, configNamespace, listenersItem,
			declaredListeners(listenerNames), e.Listener, configNamespace, listenersItem)
	}
	if e.MaxBodyBytes != nil && *e.MaxBodyBytes < 0 {
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q gives max-body-bytes the value %d; 0 removes the limit and a "+
				"positive number sets it", ErrInvalidConfig, name, *e.MaxBodyBytes)
	}
	// The trusted proxy networks are validated here rather than only being handed
	// to gin, so a mistyped one names the endpoint and the key it was written
	// under instead of surfacing as gin's parse error from inside the engine.
	// The check is gin's own rule — an IP address or a CIDR — and the values
	// themselves go to gin untouched; this module parses no request header
	// itself.
	for i, network := range e.TrustedProxies {
		if validTrustedProxy(network) {
			continue
		}
		return endpointSettings{}, fmt.Errorf(
			"%w: endpoint %q gives trusted-proxies[%d] the value %q, which is neither an IP "+
				"address nor a CIDR block. Write each entry as an address or a block, for example "+
				"\"10.0.0.0/8\", or drop the key to trust no proxy",
			ErrInvalidConfig, name, i, network)
	}

	prefixes, err := e.claimPrefixes(name)
	if err != nil {
		return endpointSettings{}, err
	}

	return endpointSettings{
		name:            name,
		listenerName:    e.Listener,
		prefixes:        prefixes,
		maxBodyBytes:    valueOrDefault(e.MaxBodyBytes, defaultMaxBodyBytes),
		trustedProxies:  e.TrustedProxies,
		remoteIPHeaders: e.RemoteIPHeaders,
	}, nil
}

// claimPrefixes validates and completes one endpoint's claim list. An omitted
// key claims "/", which makes the endpoint its listener's fallback; an explicit
// empty list is refused, because that endpoint would receive no request at all.
// The rules on a single prefix keep it literal and matchable: the dispatch
// compares the decoded request path at segment boundaries, so a trailing slash,
// a dynamic segment or an escaped character would produce a claim nothing ever
// falls under.
func (e endpointConfig) claimPrefixes(name string) ([]string, error) {
	prefixes := e.Prefixes
	if prefixes == nil {
		return []string{"/"}, nil
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf(
			"%w: endpoint %q gives %s.%s.%s.prefixes the value [], which claims no path at all, "+
				"so the endpoint would receive no request. Give it at least one prefix, or drop "+
				"the key to claim \"/\"",
			ErrInvalidConfig, name, configNamespace, endpointsItem, name)
	}
	seen := make(map[string]int, len(prefixes))
	for i, prefix := range prefixes {
		key := fmt.Sprintf("%s.%s.%s.prefixes[%d]", configNamespace, endpointsItem, name, i)
		if reason := prefixSummary(prefix); reason != "" {
			return nil, fmt.Errorf(
				"%w: endpoint %q gives %s the value %q, which is not a usable path prefix: %s. "+
					"A prefix begins with \"/\" and holds literal segments only: no trailing \"/\" "+
					"unless it is \"/\" itself, no empty, \".\" or \"..\" segment, no segment "+
					"beginning with \":\" or \"*\", and no %%, ?, #, whitespace or control character. "+
					"Requests are matched after decoding, so a prefix carrying \"%%\" would never match",
				ErrInvalidConfig, name, key, prefix, reason)
		}
		if previous, duplicate := seen[prefix]; duplicate {
			return nil, fmt.Errorf(
				"%w: endpoint %q gives %s the value %q, which the list already claims at index %d. "+
					"Drop one of the two", ErrInvalidConfig, name, key, prefix, previous)
		}
		seen[prefix] = i
	}
	for i, prefix := range prefixes {
		if prefix == "/" {
			continue
		}
		for j, other := range prefixes {
			if i == j || other == "/" || other == prefix {
				continue
			}
			if fallsUnder(other, prefix) {
				return nil, fmt.Errorf(
					"%w: endpoint %q gives %s the value %q, which already lies under the claimed "+
						"prefix %q, so the longer claim changes nothing. Claim the shorter prefix "+
						"alone, or replace it with the longer one",
					ErrInvalidConfig, name,
					fmt.Sprintf("%s.%s.%s.prefixes[%d]", configNamespace, endpointsItem, name, j),
					other, prefix)
			}
		}
	}
	return prefixes, nil
}

// prefixSummary describes the first rule a claim prefix breaks, or returns ""
// when it is usable. The rules keep the claim literal: the dispatch compares it
// with the decoded request path at segment boundaries and treats ":" and "*" as
// literals, so a non-literal segment would silently never match.
func prefixSummary(prefix string) string {
	switch {
	case prefix == "":
		return "it is empty"
	case prefix[0] != '/':
		return "it does not begin with \"/\""
	}
	if prefix == "/" {
		return ""
	}
	if strings.HasSuffix(prefix, "/") {
		return "it ends with \"/\", so it would never match a request for the path without it"
	}
	for _, segment := range strings.Split(prefix[1:], "/") {
		switch {
		case segment == "":
			return "it has an empty segment"
		case segment == "." || segment == "..":
			return fmt.Sprintf("it has the reserved segment %q", segment)
		case strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*"):
			return fmt.Sprintf("it has the dynamic segment %q, and a claim holds literal "+
				"segments only", segment)
		}
	}
	if found := strings.ContainsAny(prefix, "%?#"); found {
		return "it contains \"%\", \"?\" or \"#\""
	}
	for _, r := range prefix {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "it contains whitespace or a control character"
		}
	}
	return ""
}

// declaredListeners renders the listener names for a message, saying so plainly
// when there are none.
func declaredListeners(names []string) string {
	if len(names) == 0 {
		return "none: the configuration declares no listener at all"
	}
	return quotedList(names)
}

// validTrustedProxy reports whether a trusted-proxies entry is a form gin
// accepts: an IP address or a CIDR block. It mirrors gin's own rule, so a value
// refused here is exactly one SetTrustedProxies would refuse, and the message
// the reader gets names the endpoint and the key rather than only the value.
func validTrustedProxy(network string) bool {
	if _, _, err := net.ParseCIDR(network); err == nil {
		return true
	}
	return net.ParseIP(network) != nil
}

// listenerKeys lists the keys a listener entry takes, read off the carrier's
// own tags so the message cannot drift from the fields.
func listenerKeys() []string {
	return keysOf[listenerConfig]()
}

// endpointKeys lists the keys an endpoint entry takes, read off the carrier's
// own tags so the message cannot drift from the fields.
func endpointKeys() []string {
	return keysOf[endpointConfig]()
}

// keysOf reads the config key of each field of a carrier type, so a message that
// lists the keys a carrier takes cannot drift from the fields.
func keysOf[T any]() []string {
	t := reflect.TypeFor[T]()
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("config"), ",")
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// quotedList renders names for a message, each quoted.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, strconv.Quote(name))
	}
	return strings.Join(quoted, ", ")
}

// pluralVerb is the verb agreeing with a count of keys.
func pluralVerb(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// valueOrDefault reads an optional item in its three states: absent takes the
// default, an explicit 0 switches the item off, and any other value is taken as
// given. The two items that cannot be switched off never reach it with a 0:
// resolve has refused such a configuration before it gets here.
func valueOrDefault[T any](given *T, def T) T {
	if given == nil {
		return def
	}
	return *given
}
