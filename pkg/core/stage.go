package core

import "context"

// Stage names one step of the lifecycle.
// A drive advances from StagePrepare through the stage its Depth names and no further, so the
// values are ordered and comparable: a callback runs when its stage is at most the depth.
type Stage uint8

const (
	// StagePrepare is the stance stage: every participating module states its stance on its exclusive
	// claims, and one that cannot start returns an error here, ending the advance.
	// A module the host supplied an instance for states none: its stance is StateEnabled.
	StagePrepare Stage = iota
	// StageNew constructs the products of the enabled set, in dependency order.
	// A module the host supplied an instance for is passed over: it has its instance already.
	StageNew
	// StageMigrate applies the migrations collected from the enabled set.
	StageMigrate
	// StageInit wires modules together and publishes.
	StageInit
	// StageStart starts each module's own service, without external traffic.
	StageStart
	// StageServe begins accepting external traffic.
	// It is the deepest a drive can go.
	StageServe
)

// String renders the stage the way the callback it drives is named, so a stage failure reads as
// the field the module wrote.
func (s Stage) String() string {
	switch s {
	case StagePrepare:
		return "Prepare"
	case StageNew:
		return "New"
	case StageMigrate:
		return "Migrate"
	case StageInit:
		return "Init"
	case StageStart:
		return "Start"
	case StageServe:
		return "Serve"
	default:
		return "unknown"
	}
}

func callbackFor(m Module, s Stage) func(context.Context, *Registry, any) error {
	switch s {
	case StageMigrate:
		return m.Migrate
	case StageInit:
		return m.Init
	case StageStart:
		return m.Start
	case StageServe:
		return m.Serve
	default:
		return nil
	}
}
