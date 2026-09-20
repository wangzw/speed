module github.com/vislake/speed/examples/authenticated-host

go 1.26.0

// No pkg module has a released version yet, so this host resolves every one of
// them from the sibling checkouts. A consumer of a published example would drop
// the replace directives and depend on the released versions.
replace github.com/vislake/speed/pkg/authn => ../../pkg/authn

replace github.com/vislake/speed/pkg/config => ../../pkg/config

replace github.com/vislake/speed/pkg/core => ../../pkg/core

replace github.com/vislake/speed/pkg/http => ../../pkg/http

replace github.com/vislake/speed/pkg/log => ../../pkg/log

require (
	github.com/vislake/speed/pkg/authn v0.0.0
	github.com/vislake/speed/pkg/config v0.0.0
	github.com/vislake/speed/pkg/core v0.0.0
	github.com/vislake/speed/pkg/http v0.0.0
	github.com/vislake/speed/pkg/log v0.0.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.28.0 // indirect
)
