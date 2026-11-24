// Command rootimport is the fixture the root package's test starts: a program that
// imports this module's root package and nothing else from pkg/http.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type observation struct {
	Modules []string `json:"modules"`
	Routers []string `json:"routers"`
	// Exclusive is true when every one of those deliveries claims Router exclusively, and
	// false when there is none.
	Exclusive bool `json:"exclusive"`
}

func main() {
	seen := observation{Modules: []string{}, Routers: []string{}}
	exclusive := true
	for _, module := range core.ProcessRegistry.Modules() {
		seen.Modules = append(seen.Modules, module.Name)
		for _, provision := range module.Provides {
			if reflect.TypeOf(provision.Token) == reflect.TypeFor[*speedhttp.Router]() {
				seen.Routers = append(seen.Routers, module.Name)
				exclusive = exclusive && provision.Exclusive
			}
		}
	}
	seen.Exclusive = exclusive && len(seen.Routers) > 0
	sort.Strings(seen.Modules)
	sort.Strings(seen.Routers)

	if err := json.NewEncoder(os.Stdout).Encode(seen); err != nil {
		fmt.Fprintln(os.Stderr, "reporting the process registry:", err)
		os.Exit(1)
	}
}
