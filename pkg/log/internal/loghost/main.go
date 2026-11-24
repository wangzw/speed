// Command loghost is the child-process test host the end-to-end cases in this directory drive; it is not part of the product.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"

	_ "github.com/vislake/speed/pkg/config/format/json"
	_ "github.com/vislake/speed/pkg/config/source/file"
	_ "github.com/vislake/speed/pkg/log"
)

const exclusiveMarker = "loghost: exclusive-violated"

var finish context.CancelFunc

func main() { os.Exit(run()) }

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finish = cancel

	if scenario() == caseRivalProvider {
		core.ProcessRegistry.Register(rivalLoggerModule())
	}

	app := config.NewApp(core.ProcessRegistry)
	root := app.Root()
	root.IncludeAll()
	root.Do(core.StageServe, nil)

	if err := app.Execute(ctx); err != nil {
		if errors.Is(err, core.ErrExclusiveViolated) {
			say(exclusiveMarker)
		}
		return 1
	}
	return 0
}

func say(line string) {
	//nolint:errcheck // dropping the error is what this function is for; the
	// doc comment above says why there is nothing to do with it.
	fmt.Fprintln(os.Stdout, line)
}
