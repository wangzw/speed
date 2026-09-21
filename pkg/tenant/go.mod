module github.com/vislake/speed/pkg/tenant

go 1.26.0

// No pkg module has a released version yet, so this module resolves every one
// of them from the sibling checkouts. A replace directive in a dependency is
// ignored by consumers, so it affects this module's own standalone builds only.
replace github.com/vislake/speed/pkg/authn => ../authn

replace github.com/vislake/speed/pkg/config => ../config

replace github.com/vislake/speed/pkg/core => ../core

replace github.com/vislake/speed/pkg/db => ../db

replace github.com/vislake/speed/pkg/http => ../http

replace github.com/vislake/speed/pkg/log => ../log

replace github.com/vislake/speed/pkg/pgpool => ../pgpool

require (
	github.com/glebarez/sqlite v1.11.0
	github.com/vislake/speed/pkg/authn v0.0.0-00010101000000-000000000000
	github.com/vislake/speed/pkg/config v0.0.0
	github.com/vislake/speed/pkg/core v0.0.0
	github.com/vislake/speed/pkg/db v0.0.0
	github.com/vislake/speed/pkg/http v0.0.0
	github.com/vislake/speed/pkg/log v0.0.0
	gorm.io/gorm v1.31.2
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	modernc.org/libc v1.22.5 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/sqlite v1.23.1 // indirect
)
