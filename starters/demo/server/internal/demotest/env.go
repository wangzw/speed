package demotest

import "os"

// childEnvNames is the whole of what a child process starts with. A name joins
// this list only by name, never as a prefix or a pattern, and only with a case
// that fails without it.
var childEnvNames = []string{
	"HOME",
	"TMPDIR",
	"TZ",
	"ZONEINFO",
	"LANG",
	"LC_ALL",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"GODEBUG",
	"GOMAXPROCS", // keeps a child inside the gate's GATE_JOBS budget
	"GOGC",
	"GOMEMLIMIT",
	"GOTRACEBACK",
	"GORACE",
	"GOCOVERDIR",
	"GATE_TIME_SCALE",
}

// ChildEnv returns the environment a child process starts with: only the
// variables in its list, each read with os.LookupEnv so the testing package
// records the read, and an unset name counts as a value of its own.
func ChildEnv() []string {
	env := make([]string, 0, len(childEnvNames))
	for _, name := range childEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
